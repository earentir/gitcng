package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"golang.org/x/term"
)

const reportTimeLayout = "2006-01-02 15:04"

const metricsSep = "   "

const (
	ansiReset = "\033[0m"
	ansiGood  = "\033[32m"       // green — pull/push succeeded
	ansiNote  = "\033[33m"       // yellow — listed / skip reasons (not metric highlights)
	ansiMuted = "\033[90m"       // gray — low-severity / informational status
	ansiWarn  = "\033[38;5;208m" // orange — dirty / uncommitted / behind / ahead metrics
	ansiErr   = "\033[91m"       // bright red — merge conflicts and hard errors
)

var ansiStrip = regexp.MustCompile("\033\\[[0-9;]*m")

var (
	statusMu            sync.Mutex
	scanProgressTotal   int
	scanProgressStarted int
)

func setScanProgress(total int) {
	statusMu.Lock()
	scanProgressTotal = total
	scanProgressStarted = 0
	statusMu.Unlock()
}

func clearScanProgress() {
	statusMu.Lock()
	scanProgressTotal = 0
	scanProgressStarted = 0
	statusMu.Unlock()
}

func markRepoCheckStarted() {
	statusMu.Lock()
	scanProgressStarted++
	statusMu.Unlock()
}

func termCols() int {
	for _, fd := range []int{int(os.Stdout.Fd()), int(os.Stderr.Fd())} {
		w, _, err := term.GetSize(fd)
		if err == nil && w > 0 {
			return w
		}
	}
	return 80
}

func colourWarn(s string) string {
	if !useColour() {
		return s
	}
	return ansiWarn + s + ansiReset
}

func colourErr(s string) string {
	if !useColour() {
		return s
	}
	return ansiErr + s + ansiReset
}

func colourGood(s string) string {
	if !useColour() {
		return s
	}
	return ansiGood + s + ansiReset
}

func colourNote(s string) string {
	if !useColour() {
		return s
	}
	return ansiNote + s + ansiReset
}

func colourMuted(s string) string {
	if !useColour() {
		return s
	}
	return ansiMuted + s + ansiReset
}

func colourNum(width, val int, tint func(string) string) string {
	plain := fmt.Sprintf("%*d", width, val)
	if val == 0 || tint == nil {
		return plain
	}
	trimmed := strings.TrimSpace(plain)
	pad := width - len(trimmed)
	if pad < 0 {
		pad = 0
	}
	return strings.Repeat(" ", pad) + tint(trimmed)
}

func colourYesNo(yes bool) string {
	if yes {
		return colourWarn("yes")
	}
	return " no"
}

func visibleLen(s string) int {
	return len([]rune(ansiStrip.ReplaceAllString(s, "")))
}

func trimVisible(s string, width int) string {
	if width < 1 {
		return ""
	}
	if visibleLen(s) <= width {
		return s
	}
	plain := ansiStrip.ReplaceAllString(s, "")
	r := []rune(plain)
	if len(r) <= width {
		return s
	}
	if width == 1 {
		return "…"
	}
	return string(r[:width-1]) + "…"
}

func writeStatus(action, path string) {
	if quietOutput || !stderrIsTTY() {
		return
	}
	statusMu.Lock()
	defer statusMu.Unlock()
	prefix := ""
	if scanProgressTotal > 1 {
		n := scanProgressStarted
		if n < 1 {
			n = 1
		}
		if n > scanProgressTotal {
			n = scanProgressTotal
		}
		prefix = fmt.Sprintf("[%d/%d] ", n, scanProgressTotal)
	}
	msg := trimRunes(prefix+action+" "+path, termCols()-1)
	fmt.Fprintf(os.Stderr, "\r%s\033[K", msg)
}

func clearStatus() {
	if !stderrIsTTY() {
		return
	}
	fmt.Fprint(os.Stderr, "\r\033[K")
}

// severityActionable: needs work (sync, conflicts, errors).
func (r repoStatus) severityActionable() bool {
	return r.DirtyFiles > 0 || r.Ahead > 0 || r.Behind > 0 || r.Conflicts > 0 || len(r.Errs) > 0
}

// severityInformational: in sync with origin but release tagging lags (or missing).
func (r repoStatus) severityInformational() bool {
	if r.severityActionable() {
		return false
	}
	if r.NoReleaseTags && r.SinceRelease > 0 {
		return true
	}
	return r.HasLastRelease && r.SinceRelease > 0
}

func (r repoStatus) hasChanges() bool {
	return r.severityActionable() || r.severityInformational()
}

func (r repoStatus) uncommitted() bool {
	return r.DirtyFiles > 0
}

func (r repoStatus) diverged() bool {
	return r.Ahead > 0 && r.Behind > 0
}

func formatReportTime(t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	return t.Local().Format(reportTimeLayout)
}

func formatPushNote(r repoStatus) string {
	if r.PushNote == "" {
		return ""
	}
	switch r.PushOutcome {
	case pushDone:
		return colourGood(r.PushNote)
	case pushSkipped:
		return colourNote(r.PushNote)
	case pushFailed:
		return colourErr(r.PushNote)
	default:
		return r.PushNote
	}
}

func formatPullNote(r repoStatus) string {
	if r.PullNote == "" {
		return ""
	}
	switch r.PullOutcome {
	case pullDone:
		return colourGood(r.PullNote)
	case pullSkipped:
		return colourNote(r.PullNote)
	case pullUpToDate:
		return colourMuted(r.PullNote)
	case pullFailed:
		return colourErr(r.PullNote)
	default:
		return r.PullNote
	}
}

func releaseStatusPart(r repoStatus) string {
	if r.NoReleaseTags && r.SinceRelease > 0 {
		return formatNoReleaseTag(r)
	}
	if !r.HasLastRelease || r.SinceRelease <= 0 {
		return ""
	}
	if !r.severityActionable() {
		return colourMuted(formatSinceLastReleasePlain(r))
	}
	return formatSinceLastRelease(r)
}

func syncStatusParts(r repoStatus) []string {
	var parts []string
	if r.DirtyFiles == 1 {
		parts = append(parts, colourNote("1 uncommitted change"))
	} else if r.DirtyFiles > 1 {
		parts = append(parts, colourNote(fmt.Sprintf("%d uncommitted changes", r.DirtyFiles)))
	}
	if r.Behind > 0 {
		if r.Behind == 1 {
			parts = append(parts, colourNote("1 commit behind origin"))
		} else {
			parts = append(parts, colourNote(fmt.Sprintf("%d commits behind origin", r.Behind)))
		}
	}
	if r.Ahead > 0 && r.Behind > 0 {
		parts = append(parts, colourNote("diverged from origin"))
	} else if r.Ahead > 0 {
		if r.Ahead == 1 {
			parts = append(parts, colourNote("1 commit ahead of origin"))
		} else {
			parts = append(parts, colourNote(fmt.Sprintf("%d commits ahead of origin", r.Ahead)))
		}
	}
	if r.Conflicts == 1 {
		parts = append(parts, colourNote("1 merge conflict"))
	} else if r.Conflicts > 1 {
		parts = append(parts, colourNote(fmt.Sprintf("%d merge conflicts", r.Conflicts)))
	}
	if len(r.Errs) > 0 {
		parts = append(parts, colourErr("scan error(s)"))
	}
	return parts
}

func rowCombinedStatusLine(r repoStatus, pathCol int, didPull, didPush, showAll bool) string {
	var parts []string
	if didPull || didPush {
		if n := formatPullNote(r); n != "" {
			parts = append(parts, n)
		}
		if n := formatPushNote(r); n != "" {
			parts = append(parts, n)
		}
	} else {
		if p := releaseStatusPart(r); p != "" {
			parts = append(parts, p)
		}
		parts = append(parts, syncStatusParts(r)...)
	}
	if len(parts) == 0 {
		if showAll && !didPull && !didPush && !r.severityActionable() && !r.severityInformational() {
			indent := strings.Repeat(" ", pathCol+len(metricsSep))
			return indent + colourMuted("up to date with origin")
		}
		return ""
	}
	indent := strings.Repeat(" ", pathCol+len(metricsSep))
	return indent + strings.Join(parts, " | ")
}

func repoDisplayPath(path string) string {
	if filepath.Clean(path) == "." {
		if wd, err := os.Getwd(); err == nil {
			return filepath.Base(wd)
		}
	}
	return path
}

func repoDisplayName(r repoStatus) string {
	if r.RepoName != "" {
		return r.RepoName
	}
	return repoDisplayPath(r.Path)
}

func printReport(w io.Writer, rows []repoStatus, didPull, didPush, showAll bool) {
	if len(rows) == 0 {
		fmt.Fprintln(w, "No repositories with local or remote changes")
		return
	}

	cols := termCols()
	localW, remoteW, conflictW, aheadW := 1, 1, 1, 1
	maxPath := 0
	for _, r := range rows {
		shown := repoDisplayName(r)
		if n := len(shown); n > maxPath {
			maxPath = n
		}
		if r.LastReleaseTag != "" {
			if n := len(r.LastReleaseTag) + 1; n > maxPath {
				maxPath = n
			}
		}
		if n := digitWidth(r.DirtyFiles); n > localW {
			localW = n
		}
		if n := digitWidth(r.Behind); n > remoteW {
			remoteW = n
		}
		if n := digitWidth(r.Conflicts); n > conflictW {
			conflictW = n
		}
		if n := digitWidth(r.Ahead); n > aheadW {
			aheadW = n
		}
	}

	line0SuffixLen := visibleLen(rowLine0Plain(localW, remoteW, true))
	line1SuffixLen := visibleLen(rowLine1Plain(conflictW, aheadW, true))
	compactSuffixLen := visibleLen(compactMetricsPlain(localW, remoteW, conflictW, aheadW))
	suffixLen := line0SuffixLen
	if line1SuffixLen > suffixLen {
		suffixLen = line1SuffixLen
	}
	if reportCompact {
		suffixLen = compactSuffixLen
	}
	pathCol := maxPath
	if pathCol+suffixLen > cols {
		pathCol = cols - suffixLen
		if pathCol < 12 {
			pathCol = 12
		}
	}

	for i, r := range rows {
		shownPath := repoDisplayName(r)
		path := fitPath(shownPath, pathCol)
		if visibleLen(path)+suffixLen > cols {
			allow := cols - suffixLen
			if allow < 1 {
				allow = 1
			}
			path = trimRunes(shownPath, allow)
		}

		if reportCompact {
			line := formatRowCompact(r, path, localW, remoteW, conflictW, aheadW)
			fmt.Fprintln(w, trimVisible(line, cols))
			for _, diffLine := range rowReleaseDiffLines(r, pathCol) {
				fmt.Fprintln(w, trimVisible(diffLine, cols))
			}
			if status := rowCombinedStatusLine(r, pathCol, didPull, didPush, showAll); status != "" {
				fmt.Fprintln(w, trimVisible(status, cols))
			}
			if i < len(rows)-1 {
				fmt.Fprintln(w)
			}
			continue
		}

		line0 := rowLine0(r, path, localW, remoteW)
		fmt.Fprintln(w, trimVisible(line0, cols))

		line1 := rowLine1(r, pathCol, conflictW, aheadW)
		fmt.Fprintln(w, trimVisible(line1, cols))

		line2 := rowLine2(r, pathCol)
		fmt.Fprintln(w, trimVisible(line2, cols))

		for _, diffLine := range rowReleaseDiffLines(r, pathCol) {
			fmt.Fprintln(w, trimVisible(diffLine, cols))
		}
		if status := rowCombinedStatusLine(r, pathCol, didPull, didPush, showAll); status != "" {
			fmt.Fprintln(w, trimVisible(status, cols))
		}
		if i < len(rows)-1 {
			fmt.Fprintln(w)
		}
	}
}

func rowLine0Plain(localW, remoteW int, sizing bool) string {
	uncomm := " no"
	if sizing {
		uncomm = "yes"
	}
	return fmt.Sprintf("%sLocal: %*d | Remote: %*d | Uncommitted: %3s",
		metricsSep, localW, 0, remoteW, 0, uncomm)
}

func rowLine0(r repoStatus, path string, localW, remoteW int) string {
	line := path + formatRowLine0(r, localW, remoteW)
	if len(r.Errs) > 0 {
		line += " | " + colourErr(r.Errs[0])
	}
	return line
}

func formatRowLine0(r repoStatus, localW, remoteW int) string {
	return fmt.Sprintf("%sLocal: %s | Remote: %s | Uncommitted: %s",
		metricsSep,
		colourNum(localW, r.DirtyFiles, colourWarn),
		colourNum(remoteW, r.Behind, colourWarn),
		colourYesNo(r.uncommitted()),
	)
}

func rowLine1Plain(conflictW, aheadW int, sizing bool) string {
	div := " no"
	if sizing {
		div = "yes"
	}
	return fmt.Sprintf("%sConflicts: %*d | Diverged: %3s | Ahead: %*d",
		metricsSep, conflictW, 0, div, aheadW, 0)
}

func rowLine1(r repoStatus, pathCol, conflictW, aheadW int) string {
	prefix := strings.Repeat(" ", pathCol)
	if r.LastReleaseTag != "" {
		inner := pathCol - 1
		if inner < 1 {
			inner = 1
		}
		prefix = " " + fitPath(r.LastReleaseTag, inner)
	}
	return prefix + formatRowLine1Metrics(r, conflictW, aheadW)
}

func formatRowLine1Metrics(r repoStatus, conflictW, aheadW int) string {
	return fmt.Sprintf("%sConflicts: %s | Diverged: %s | Ahead: %s",
		metricsSep,
		colourNum(conflictW, r.Conflicts, colourErr),
		colourYesNo(r.diverged()),
		colourNum(aheadW, r.Ahead, colourWarn),
	)
}

func rowLine2(r repoStatus, pathCol int) string {
	indent := strings.Repeat(" ", pathCol+len(metricsSep))
	commit := formatReportTime(r.LastCommit)
	push := "-"
	if r.HasPush {
		push = formatReportTime(r.LastPush)
	}
	line := indent + fmt.Sprintf("Commit: %s | Push: %s", commit, push)
	if r.HasLastRelease {
		line += fmt.Sprintf(" | Last Release: %s", formatReportTime(r.LastRelease))
	}
	return line
}

func formatSinceLastReleasePlain(r repoStatus) string {
	commitWord := "commits"
	if r.SinceRelease == 1 {
		commitWord = "commit"
	}
	part := fmt.Sprintf("Since %s: %d %s", r.LastReleaseTag, r.SinceRelease, commitWord)
	if r.SinceRelease > 0 && r.SinceReleaseFiles > 0 {
		fileWord := "files"
		if r.SinceReleaseFiles == 1 {
			fileWord = "file"
		}
		part += fmt.Sprintf(", %d %s", r.SinceReleaseFiles, fileWord)
	}
	return part
}

func formatNoReleaseTag(r repoStatus) string {
	commitWord := "commits"
	if r.SinceRelease == 1 {
		commitWord = "commit"
	}
	return colourMuted(fmt.Sprintf("No release tag: %d %s", r.SinceRelease, commitWord))
}

func formatSinceLastRelease(r repoStatus) string {
	commitWord := "commits"
	if r.SinceRelease == 1 {
		commitWord = "commit"
	}
	n := colourNum(digitWidth(r.SinceRelease), r.SinceRelease, colourWarn)
	part := fmt.Sprintf("Since %s: %s %s", r.LastReleaseTag, n, commitWord)
	if r.SinceRelease > 0 && r.SinceReleaseFiles > 0 {
		fw := colourNum(digitWidth(r.SinceReleaseFiles), r.SinceReleaseFiles, colourWarn)
		fileWord := "files"
		if r.SinceReleaseFiles == 1 {
			fileWord = "file"
		}
		part += fmt.Sprintf(", %s %s", fw, fileWord)
	}
	return part
}

func rowReleaseDiffLines(r repoStatus, pathCol int) []string {
	if !releaseDiff || r.LastReleaseTag == "" || r.SinceRelease <= 0 {
		return nil
	}
	indent := strings.Repeat(" ", pathCol+len(metricsSep))
	var lines []string
	for _, c := range r.ReleaseCommits {
		lines = append(lines, indent+fmt.Sprintf("%s  %s  %s",
			c.Short, formatReportTime(c.When), c.Subject))
	}
	if len(r.ReleasePaths) == 0 {
		return lines
	}
	lines = append(lines, indent+fmt.Sprintf("files changed (%d):", len(r.ReleasePaths)))
	for _, p := range r.ReleasePaths {
		lines = append(lines, indent+"  "+p)
	}
	return lines
}

func compactMetricsPlain(localW, remoteW, conflictW, aheadW int) string {
	ts := formatReportTime(time.Now())
	return rowLine0Plain(localW, remoteW, true) + " | " +
		rowLine1Plain(conflictW, aheadW, true) +
		fmt.Sprintf(" | Commit: %s | Push: %s", ts, ts)
}

func formatRowCompact(r repoStatus, path string, localW, remoteW, conflictW, aheadW int) string {
	commit := formatReportTime(r.LastCommit)
	push := "-"
	if r.HasPush {
		push = formatReportTime(r.LastPush)
	}
	line := path + formatRowLine0(r, localW, remoteW) + " | " +
		formatRowLine1Metrics(r, conflictW, aheadW) +
		fmt.Sprintf(" | Commit: %s | Push: %s", commit, push)
	if r.HasLastRelease {
		line += fmt.Sprintf(" | Last Release: %s", formatReportTime(r.LastRelease))
	}
	if len(r.Errs) > 0 {
		line += " | " + colourErr(r.Errs[0])
	}
	return line
}

func digitWidth(n int) int {
	if n < 0 {
		n = -n
	}
	w := 1
	for n >= 10 {
		n /= 10
		w++
	}
	return w
}

func fitPath(path string, width int) string {
	if width < 1 {
		width = 1
	}
	r := []rune(path)
	if len(r) <= width {
		return path + strings.Repeat(" ", width-len(r))
	}
	return trimRunes(path, width)
}

func trimRunes(s string, width int) string {
	if width < 1 {
		return ""
	}
	r := []rune(s)
	if len(r) <= width {
		return s
	}
	if width == 1 {
		return "…"
	}
	return string(r[:width-1]) + "…"
}
