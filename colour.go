package main

import (
	"os"
	"strings"

	"golang.org/x/term"
)

// colourMode: auto (default), always, never
var colourMode string

func useColour() bool {
	switch strings.ToLower(colourMode) {
	case "never", "off", "false", "0":
		return false
	case "always", "on", "true", "1":
		return true
	default:
		if envDisableColour() {
			return false
		}
		if envForceColour() {
			return true
		}
		return term.IsTerminal(int(os.Stdout.Fd())) || term.IsTerminal(int(os.Stderr.Fd()))
	}
}

func envDisableColour() bool {
	for _, key := range []string{"NO_COLOR", "NO_COLOUR"} {
		if os.Getenv(key) != "" {
			return true
		}
	}
	return false
}

func envForceColour() bool {
	for _, key := range []string{"FORCE_COLOR", "FORCE_COLOUR"} {
		v := os.Getenv(key)
		if v != "" && v != "0" && strings.ToLower(v) != "false" {
			return true
		}
	}
	return false
}

func stderrIsTTY() bool {
	return term.IsTerminal(int(os.Stderr.Fd()))
}
