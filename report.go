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
	ansiWarn  = "\033[38;5;208m" // orange — dirty / uncommitted / behind / ahead
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

func (r repoStatus) hasChanges() bool {
	return r.DirtyFiles > 0 || r.Ahead > 0 || r.Behind > 0 || r.Conflicts > 0 ||
		(r.Ahead > 0 && r.Behind > 0) || len(r.Errs) > 0 || r.SinceRelease > 0
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
	note := r.PushNote
	switch r.PushOutcome {
	case pushSkipped:
		note = colourWarn(note)
	case pushFailed:
		note = colourErr(note)
	}
	return note
}

func rowPushLine(r repoStatus, pathCol int) string {
	note := formatPushNote(r)
	if note == "" {
		return ""
	}
	indent := strings.Repeat(" ", pathCol+len(metricsSep))
	return indent + note
}

func formatPullNote(r repoStatus) string {
	if r.PullNote == "" {
		return ""
	}
	note := r.PullNote
	switch r.PullOutcome {
	case pullSkipped:
		note = colourWarn(note)
	case pullFailed:
		note = colourErr(note)
	}
	return note
}

func rowPullLine(r repoStatus, pathCol int) string {
	note := formatPullNote(r)
	if note == "" {
		return ""
	}
	indent := strings.Repeat(" ", pathCol+len(metricsSep))
	return indent + note
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

func printReport(w io.Writer, rows []repoStatus) {
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
			if note := formatPullNote(r); note != "" {
				line += " | " + note
			}
			if note := formatPushNote(r); note != "" {
				line += " | " + note
			}
			fmt.Fprintln(w, trimVisible(line, cols))
			for _, diffLine := range rowReleaseDiffLines(r, pathCol) {
				fmt.Fprintln(w, trimVisible(diffLine, cols))
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

		if sinceLine := rowSinceReleaseLine(r, pathCol); sinceLine != "" {
			fmt.Fprintln(w, trimVisible(sinceLine, cols))
		}

		for _, diffLine := range rowReleaseDiffLines(r, pathCol) {
			fmt.Fprintln(w, trimVisible(diffLine, cols))
		}
		if pullLine := rowPullLine(r, pathCol); pullLine != "" {
			fmt.Fprintln(w, trimVisible(pullLine, cols))
		}
		if pushLine := rowPushLine(r, pathCol); pushLine != "" {
			fmt.Fprintln(w, trimVisible(pushLine, cols))
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

func rowSinceReleaseLine(r repoStatus, pathCol int) string {
	if !r.HasLastRelease || r.SinceRelease < 0 {
		return ""
	}
	indent := strings.Repeat(" ", pathCol+len(metricsSep))
	return indent + formatSinceLastRelease(r)
}

func formatSinceLastRelease(r repoStatus) string {
	commitWord := "commits"
	if r.SinceRelease == 1 {
		commitWord = "commit"
	}
	n := colourNum(digitWidth(r.SinceRelease), r.SinceRelease, colourWarn)
	part := fmt.Sprintf("Since last release: %s %s", n, commitWord)
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
	if r.HasLastRelease && r.SinceRelease >= 0 {
		line += " | " + formatSinceLastRelease(r)
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
