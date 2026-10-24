package main

import (
	"os"
	"strings"

	"golang.org/x/term"
)

// colorMode: auto (default), always, never
var colorMode string

func useColor() bool {
	switch strings.ToLower(colorMode) {
	case "never", "off", "false", "0":
		return false
	case "always", "on", "true", "1":
		return true
	default:
		if os.Getenv("NO_COLOR") != "" {
			return false
		}
		if v := os.Getenv("FORCE_COLOR"); v != "" && v != "0" && strings.ToLower(v) != "false" {
			return true
		}
		return term.IsTerminal(int(os.Stdout.Fd())) || term.IsTerminal(int(os.Stderr.Fd()))
	}
}

func stderrIsTTY() bool {
	return term.IsTerminal(int(os.Stderr.Fd()))
}
