package main

import (
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"golang.org/x/sys/unix"
)

const reportTimeLayout = "2006-01-02 15:04"

func termCols() int {
	for _, fd := range []int{int(os.Stdout.Fd()), int(os.Stderr.Fd())} {
		ws, err := unix.IoctlGetWinsize(fd, unix.TIOCGWINSZ)
		if err == nil && ws.Col > 0 {
			return int(ws.Col)
		}
	}
	return 80
}

func stderrIsTTY() bool {
	_, err := unix.IoctlGetWinsize(int(os.Stderr.Fd()), unix.TIOCGWINSZ)
	return err == nil
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

	suffixLen := len(rowSuffix(repoStatus{}, localW, remoteW, false))
	pathCol := maxPath
	if pathCol+suffixLen > cols {
		pathCol = cols - suffixLen
		if pathCol < 12 {
			pathCol = 12
		}
	}

	for _, r := range rows {
		suffix := rowSuffix(r, localW, remoteW, true)
		path := fitPath(r.Path, pathCol)
		if len(path)+len(suffix) > cols {
			allow := cols - len(suffix)
			if allow < 1 {
				allow = 1
			}
			path = trimRunes(r.Path, allow)
		}
		line := path + suffix
		if r.Ahead > 0 {
			line += fmt.Sprintf(" | Ahead: %d", r.Ahead)
		}
		if len(r.Errs) > 0 {
			line += " | " + r.Errs[0]
		}
		fmt.Fprintln(w, trimRunes(line, cols))
	}
}

func rowSuffix(r repoStatus, localW, remoteW int, real bool) string {
	uncomm := " no"
	if real && r.uncommitted() {
		uncomm = "yes"
	} else if !real {
		uncomm = "yes"
	}
	commit := formatReportTime(time.Time{})
	push := "-"
	if real {
		commit = formatReportTime(r.LastCommit)
		if r.HasPush {
			push = formatReportTime(r.LastPush)
		}
	} else {
		commit = formatReportTime(time.Now())
		push = formatReportTime(time.Now())
	}
	return fmt.Sprintf(" | Local: %*d | Remote: %*d | Uncommitted: %s | Commit: %s | Push: %s",
		localW, r.DirtyFiles, remoteW, r.Behind, uncomm, commit, push)
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
