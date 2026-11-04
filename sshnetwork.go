package main

import (
	"context"
	"strings"
	"time"

	git "github.com/go-git/go-git/v5"
)

// ssh-agent and many SSH servers do not tolerate unbounded parallel handshakes.
const maxConcurrentSSHNetwork = 4

var sshNetworkSem = make(chan struct{}, maxConcurrentSSHNetwork)

func withSSHNetworkLimit(usesSSH bool, fn func() error) error {
	if !usesSSH {
		return fn()
	}
	sshNetworkSem <- struct{}{}
	defer func() { <-sshNetworkSem }()
	return fn()
}

func retryableSSHTransportErr(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	if strings.Contains(msg, "handshake failed") {
		return true
	}
	if strings.Contains(msg, "ssh agent") || strings.Contains(msg, "agent: ") {
		return true
	}
	if strings.Contains(msg, "connection reset") {
		return true
	}
	if strings.Contains(msg, "connection refused") && strings.Contains(msg, "agent") {
		return true
	}
	return false
}

func withSSHTransportRetries(fn func() error) error {
	const maxTries = 3
	var lastErr error
	for try := 0; try < maxTries; try++ {
		if try > 0 {
			if !retryableSSHTransportErr(lastErr) {
				return lastErr
			}
			time.Sleep(time.Duration(try*150) * time.Millisecond)
		}
		lastErr = fn()
		if lastErr == nil || lastErr == git.NoErrAlreadyUpToDate {
			return nil
		}
	}
	return lastErr
}

func isOriginAuthError(err error) bool {
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "ssh-agent") ||
		strings.Contains(msg, "ssh agent") ||
		strings.Contains(msg, "known_hosts") ||
		strings.Contains(msg, "no ssh keys")
}

func fetchOrigin(r *git.Repository, originURL string) error {
	opts := &git.FetchOptions{RemoteName: "origin"}
	auth, err := authForOriginURL(originURL)
	if err != nil {
		return err
	}
	usesSSH := auth != nil
	if usesSSH {
		opts.Auth = &sshAuthTimeout{inner: auth, timeout: networkTimeout()}
	}
	return withSSHTransportRetries(func() error {
		return withSSHNetworkLimit(usesSSH, func() error {
			ctx, cancel := context.WithTimeout(context.Background(), networkTimeout())
			defer cancel()
			return r.FetchContext(ctx, opts)
		})
	})
}
