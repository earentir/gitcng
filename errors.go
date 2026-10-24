package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

func userError(kind string, err error) string {
	if err == nil {
		return kind + ": failed"
	}
	if msg := classifyError(kind, err); msg != "" {
		return kind + ": " + msg
	}
	return fmt.Sprintf("%s: %v", kind, err)
}

func classifyError(kind string, err error) string {
	msg := strings.ToLower(err.Error())

	switch kind {
	case "fetch":
		return classifyFetch(msg, err)
	case "ssh-agent":
		return classifySSHAgent(msg)
	case "auth":
		return classifyAuth(msg)
	case "known_hosts":
		return classifyKnownHosts(msg)
	case "remote ref":
		if strings.Contains(msg, "reference not found") || strings.Contains(msg, "not found") {
			return "no upstream branch on origin (push or set upstream)"
		}
	case "compare":
		if strings.Contains(msg, "no merge base") {
			return "local and remote histories unrelated"
		}
	}

	if errors.Is(err, context.DeadlineExceeded) || strings.Contains(msg, "deadline exceeded") {
		return fmt.Sprintf("timed out (%s)", networkTimeout())
	}
	if strings.Contains(msg, "i/o timeout") {
		return "network timeout"
	}
	if strings.Contains(msg, "connection refused") {
		return "connection refused"
	}
	if strings.Contains(msg, "connection reset") {
		return "connection reset by peer"
	}
	if strings.Contains(msg, "no route to host") {
		return "no route to host"
	}
	if strings.Contains(msg, "no such host") || (strings.Contains(msg, "lookup") && strings.Contains(msg, "timeout")) {
		return "cannot resolve remote host (DNS timeout)"
	}
	if strings.Contains(msg, "network is unreachable") {
		return "network unreachable"
	}

	return ""
}

func classifyFetch(msg string, err error) string {
	if errors.Is(err, context.DeadlineExceeded) || strings.Contains(msg, "deadline exceeded") {
		return fmt.Sprintf("timed out (%s)", networkTimeout())
	}
	if strings.Contains(msg, "cannot create known hosts callback") || strings.Contains(msg, "knownhosts") {
		return "SSH host key setup failed (check known_hosts)"
	}
	if strings.Contains(msg, "permission denied") && (strings.Contains(msg, "publickey") || strings.Contains(msg, "ssh")) {
		return "SSH authentication failed"
	}
	if strings.Contains(msg, "host key") {
		return "host key verification failed"
	}
	if strings.Contains(msg, "lookup") && strings.Contains(msg, "timeout") {
		return "cannot reach remote (DNS timeout)"
	}
	if strings.Contains(msg, "lookup") {
		return "cannot resolve remote host"
	}
	if strings.Contains(msg, "i/o timeout") {
		return "cannot reach remote (network timeout)"
	}
	if strings.Contains(msg, "connection refused") {
		return "remote refused connection"
	}
	if strings.Contains(msg, "no such remote") {
		return "remote origin not configured"
	}
	if err == context.Canceled || errors.Is(err, context.Canceled) {
		return "canceled"
	}
	return ""
}

func classifySSHAgent(msg string) string {
	if strings.Contains(msg, "ssh_auth_sock") || strings.Contains(msg, "not set") {
		return "SSH agent not available (SSH_AUTH_SOCK)"
	}
	if strings.Contains(msg, "no ssh keys") {
		return "no keys loaded in ssh-agent"
	}
	if strings.Contains(msg, "connection refused") {
		return "cannot connect to ssh-agent"
	}
	return ""
}

func classifyAuth(msg string) string {
	if strings.Contains(msg, "ssh agent") {
		return "cannot connect to ssh-agent"
	}
	return ""
}

func originRefError(branch string, err error) string {
	if msg := classifyError("remote ref", err); msg != "" {
		return fmt.Sprintf("origin/%s: %s", branch, msg)
	}
	return fmt.Sprintf("origin/%s: %v", branch, err)
}

func classifyKnownHosts(msg string) string {
	if strings.Contains(msg, "unsupported dsa") {
		return "known_hosts contains unsupported DSA keys (remove or fix the file)"
	}
	if strings.Contains(msg, "no usable known_hosts") {
		return "no valid known_hosts entries"
	}
	return ""
}
