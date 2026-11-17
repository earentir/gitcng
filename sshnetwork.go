package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"strings"
	"time"

	git "github.com/go-git/go-git/v5"
)

// ssh-agent and many SSH servers do not tolerate unbounded parallel handshakes.
const maxConcurrentSSHNetwork = 4

var (
	// Dead internal git hosts must not occupy the same slots as GitHub/GitLab/etc.
	sshInternalNetworkSem = make(chan struct{}, maxConcurrentSSHNetwork)
	sshPublicNetworkSem   = make(chan struct{}, maxConcurrentSSHNetwork)
)

var errSSHPoolBusy = errors.New("ssh connection pool busy")

var proxyEnvKeys = []string{
	"HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY",
	"http_proxy", "https_proxy", "all_proxy",
}

// go-git dials SSH over raw TCP via proxy.Dial, which honors ALL_PROXY. That
// misroutes port 22 and can block on corporate proxies when internal DNS is down.
func withoutHTTPProxyForRawTCP(fn func() error) error {
	saved := make(map[string]string, len(proxyEnvKeys))
	for _, k := range proxyEnvKeys {
		saved[k] = os.Getenv(k)
		os.Unsetenv(k)
	}
	defer func() {
		for k, v := range saved {
			if v == "" {
				os.Unsetenv(k)
			} else {
				os.Setenv(k, v)
			}
		}
	}()
	return fn()
}

// networkOperationBudget is a wall-clock cap for one fetch/pull/push attempt
// (go-git SSH connect ignores FetchContext until TCP handshake completes).
func networkOperationBudget() time.Duration {
	return networkTimeout() + 3*time.Second
}

func sshSlotWaitBudget() time.Duration {
	return networkOperationBudget() * time.Duration(maxConcurrentSSHNetwork)
}

func sshNetworkSemFor(originURL string, usesSSH bool) chan struct{} {
	if !usesSSH {
		return nil
	}
	if isWellKnownPublicGitHost(originURL) {
		return sshPublicNetworkSem
	}
	return sshInternalNetworkSem
}

// installBoundedNetworkResolver caps libc/Go DNS dial time (broken internal DNS can hang here).
func installBoundedNetworkResolver() {
	d := networkTimeout()
	net.DefaultResolver.Dial = func(ctx context.Context, network, address string) (net.Conn, error) {
		var dialer net.Dialer
		dialer.Timeout = d
		return dialer.DialContext(ctx, network, address)
	}
}

func acquireSSHNetworkSlot(sem chan struct{}) error {
	if sem == nil {
		return nil
	}
	timer := time.NewTimer(sshSlotWaitBudget())
	defer timer.Stop()
	select {
	case sem <- struct{}{}:
		return nil
	case <-timer.C:
		return fmt.Errorf("%w", errSSHPoolBusy)
	}
}

func runWithNetworkBudget(fn func() error) error {
	ch := make(chan error, 1)
	go func() {
		ch <- fn()
	}()
	select {
	case err := <-ch:
		return err
	case <-time.After(networkOperationBudget()):
		return context.DeadlineExceeded
	}
}

func withSSHNetworkLimit(usesSSH bool, originURL string, fn func() error) error {
	run := func() error {
		if usesSSH {
			return withoutHTTPProxyForRawTCP(fn)
		}
		return fn()
	}
	if !usesSSH {
		return runWithNetworkBudget(run)
	}

	sem := sshNetworkSemFor(originURL, usesSSH)
	if err := acquireSSHNetworkSlot(sem); err != nil {
		return err
	}
	defer func() { <-sem }()

	ch := make(chan error, 1)
	go func() {
		ch <- run()
	}()

	timer := time.NewTimer(networkOperationBudget())
	defer timer.Stop()
	select {
	case err := <-ch:
		return err
	case <-timer.C:
		return context.DeadlineExceeded
	}
}

func retryableSSHTransportErr(err error) bool {
	if err == nil {
		return false
	}
	if errorsIsDeadline(err) {
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

func errorsIsDeadline(err error) bool {
	if err == nil {
		return false
	}
	if err == context.DeadlineExceeded {
		return true
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "deadline exceeded") || strings.Contains(msg, "context canceled")
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

func fetchOriginGoGit(repoPath, originURL string) error {
	r, err := git.PlainOpen(repoPath)
	if err != nil {
		return err
	}
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
		return withSSHNetworkLimit(usesSSH, originURL, func() error {
			ctx, cancel := context.WithTimeout(context.Background(), networkTimeout())
			defer cancel()
			return r.FetchContext(ctx, opts)
		})
	})
}