package main

import (
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
	"time"

	"golang.org/x/sys/unix"
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

func termCols() int {
	for _, fd := range []int{int(os.Stdout.Fd()), int(os.Stderr.Fd())} {
		w, _, err := term.GetSize(fd)
		if err == nil && w > 0 {
			return w
		}
		ws, err := unix.IoctlGetWinsize(fd, unix.TIOCGWINSZ)
		if err == nil && ws.Col > 0 {
			return int(ws.Col)
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
	msg := trimRunes(action+" "+path, termCols()-1)
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
		(r.Ahead > 0 && r.Behind > 0) || len(r.Errs) > 0
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

func printReport(w io.Writer, rows []repoStatus) {
	if len(rows) == 0 {
		fmt.Fprintln(w, "No repositories with local or remote changes")
		return
	}

	cols := termCols()
	localW, remoteW, conflictW := 1, 1, 1
	maxPath := 0
	for _, r := range rows {
		if n := len(r.Path); n > maxPath {
			maxPath = n
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
	}

	line1SuffixLen := visibleLen(rowLine1Plain(repoStatus{}, localW, remoteW, conflictW, true))
	compactSuffixLen := visibleLen(compactMetricsPlain(localW, remoteW, conflictW))
	suffixLen := line1SuffixLen
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
		path := fitPath(r.Path, pathCol)
		if visibleLen(path)+suffixLen > cols {
			allow := cols - suffixLen
			if allow < 1 {
				allow = 1
			}
			path = trimRunes(r.Path, allow)
		}

		if reportCompact {
			line := formatRowCompact(r, path, localW, remoteW, conflictW)
			if note := formatPullNote(r); note != "" {
				line += " | " + note
			}
			if note := formatPushNote(r); note != "" {
				line += " | " + note
			}
			fmt.Fprintln(w, trimVisible(line, cols))
			continue
		}

		line1 := path + formatRowLine1(r, localW, remoteW, conflictW)
		if r.Ahead > 0 {
			line1 += fmt.Sprintf(" | Ahead: %s", colourNum(digitWidth(r.Ahead), r.Ahead, colourWarn))
		}
		if len(r.Errs) > 0 {
			line1 += " | " + colourErr(r.Errs[0])
		}
		fmt.Fprintln(w, trimVisible(line1, cols))

		line2 := rowLine2(r, pathCol)
		fmt.Fprintln(w, trimVisible(line2, cols))

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

func rowLine1Plain(r repoStatus, localW, remoteW, conflictW int, sizing bool) string {
	local, remote, conflicts := 0, 0, 0
	uncomm, diverged := " no", " no"
	if !sizing {
		local, remote, conflicts = r.DirtyFiles, r.Behind, r.Conflicts
		if r.uncommitted() {
			uncomm = "yes"
		}
		if r.diverged() {
			diverged = "yes"
		}
	} else {
		uncomm, diverged = "yes", "yes"
	}
	return fmt.Sprintf("%sLocal: %*d | Remote: %*d | Uncommitted: %3s | Conflicts: %*d | Diverged: %3s",
		metricsSep, localW, local, remoteW, remote, uncomm, conflictW, conflicts, diverged)
}

func formatRowLine1(r repoStatus, localW, remoteW, conflictW int) string {
	localNum := colourNum(localW, r.DirtyFiles, colourWarn)
	remoteNum := colourNum(remoteW, r.Behind, colourWarn)
	conflictNum := colourNum(conflictW, r.Conflicts, colourErr)

	return fmt.Sprintf("%sLocal: %s | Remote: %s | Uncommitted: %s | Conflicts: %s | Diverged: %s",
		metricsSep,
		localNum, remoteNum,
		colourYesNo(r.uncommitted()),
		conflictNum,
		colourYesNo(r.diverged()),
	)
}

func rowLine2(r repoStatus, pathCol int) string {
	indent := strings.Repeat(" ", pathCol+len(metricsSep))
	commit := formatReportTime(r.LastCommit)
	push := "-"
	if r.HasPush {
		push = formatReportTime(r.LastPush)
	}
	return indent + fmt.Sprintf("Commit: %s | Push: %s", commit, push)
}

func compactMetricsPlain(localW, remoteW, conflictW int) string {
	sample := repoStatus{HasPush: true, DirtyFiles: 1, Behind: 1, Conflicts: 1}
	ts := formatReportTime(time.Now())
	return formatRowLine1(sample, localW, remoteW, conflictW) +
		fmt.Sprintf(" | Commit: %s | Push: %s", ts, ts)
}

func formatRowCompact(r repoStatus, path string, localW, remoteW, conflictW int) string {
	commit := formatReportTime(r.LastCommit)
	push := "-"
	if r.HasPush {
		push = formatReportTime(r.LastPush)
	}
	line := path + formatRowLine1(r, localW, remoteW, conflictW) +
		fmt.Sprintf(" | Commit: %s | Push: %s", commit, push)
	if r.Ahead > 0 {
		line += fmt.Sprintf(" | Ahead: %s", colourNum(digitWidth(r.Ahead), r.Ahead, colourWarn))
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
