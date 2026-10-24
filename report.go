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

const (
	ansiReset = "\033[0m"
	ansiWarn  = "\033[38;5;208m" // orange — dirty / uncommitted / behind / ahead
	ansiErr   = "\033[91m"       // bright red — fetch and other hard errors
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

func colorWarn(s string) string {
	if !useColor() {
		return s
	}
	return ansiWarn + s + ansiReset
}

func colorErr(s string) string {
	if !useColor() {
		return s
	}
	return ansiErr + s + ansiReset
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
	if !stderrIsTTY() {
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
	return r.DirtyFiles > 0 || r.Ahead > 0 || r.Behind > 0 || len(r.Errs) > 0
}

func (r repoStatus) uncommitted() bool {
	return r.DirtyFiles > 0
}

func formatReportTime(t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	return t.Local().Format(reportTimeLayout)
}

func printReport(w io.Writer, repos []repoStatus, showAll bool) {
	var rows []repoStatus
	for _, r := range repos {
		if showAll || r.hasChanges() {
			rows = append(rows, r)
		}
	}
	if len(rows) == 0 {
		fmt.Fprintln(w, "No repositories with local or remote changes")
		return
	}

	cols := termCols()
	localW, remoteW := 1, 1
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
	}

	suffixLen := visibleLen(rowSuffixPlain(repoStatus{}, localW, remoteW, false))
	pathCol := maxPath
	if pathCol+suffixLen > cols {
		pathCol = cols - suffixLen
		if pathCol < 12 {
			pathCol = 12
		}
	}

	for _, r := range rows {
		path := fitPath(r.Path, pathCol)
		if visibleLen(path)+suffixLen > cols {
			allow := cols - suffixLen
			if allow < 1 {
				allow = 1
			}
			path = trimRunes(r.Path, allow)
		}
		line := formatRowLine(r, path, localW, remoteW)
		if r.Ahead > 0 {
			ahead := fmt.Sprintf(" | Ahead: %d", r.Ahead)
			if useColor() {
				ahead = " | Ahead: " + colorWarn(fmt.Sprintf("%d", r.Ahead))
			}
			line += ahead
		}
		if len(r.Errs) > 0 {
			line += " | " + colorErr(r.Errs[0])
		}
		fmt.Fprintln(w, trimVisible(line, cols))
	}
}

func rowSuffixPlain(r repoStatus, localW, remoteW int, sizing bool) string {
	uncomm := " no"
	if !sizing && r.uncommitted() {
		uncomm = "yes"
	} else if sizing {
		uncomm = "yes"
	}
	commit := formatReportTime(time.Time{})
	push := "-"
	if !sizing {
		commit = formatReportTime(r.LastCommit)
		if r.HasPush {
			push = formatReportTime(r.LastPush)
		}
	} else {
		commit = formatReportTime(time.Now())
		push = formatReportTime(time.Now())
	}
	return fmt.Sprintf(" | Local: %*d | Remote: %*d | Uncommitted: %3s | Commit: %s | Push: %s",
		localW, r.DirtyFiles, remoteW, r.Behind, uncomm, commit, push)
}

func formatRowLine(r repoStatus, path string, localW, remoteW int) string {
	localPart := fmt.Sprintf("Local: %*d", localW, r.DirtyFiles)
	if r.DirtyFiles > 0 {
		localPart = colorWarn(localPart)
	}

	remotePart := fmt.Sprintf("Remote: %*d", remoteW, r.Behind)
	if r.Behind > 0 {
		remotePart = colorWarn(remotePart)
	}

	uncomm := " no"
	if r.uncommitted() {
		uncomm = colorWarn("yes")
	}

	commit := formatReportTime(r.LastCommit)
	push := "-"
	if r.HasPush {
		push = formatReportTime(r.LastPush)
	}

	return fmt.Sprintf("%s | %s | %s | Uncommitted: %s | Commit: %s | Push: %s",
		path, localPart, remotePart, uncomm, commit, push)
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
