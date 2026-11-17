package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	git "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
)

var (
	gitBin     string
	gitBinOnce sync.Once
	gitBinErr  error
)

func gitOnPath() (string, error) {
	gitBinOnce.Do(func() {
		gitBin, gitBinErr = exec.LookPath("git")
	})
	return gitBin, gitBinErr
}

func gitNetworkEnv(originURL string, usesSSH bool) []string {
	direct := usesSSH || isWellKnownPublicGitHost(originURL)
	out := make([]string, 0, len(os.Environ())+2)
	for _, e := range os.Environ() {
		key, _, ok := strings.Cut(e, "=")
		if !ok {
			continue
		}
		if direct && httpProxyEnvKey(key) {
			continue
		}
		out = append(out, e)
	}
	out = append(out, "GIT_TERMINAL_PROMPT=0")
	if usesSSH {
		sec := int(networkTimeout().Seconds())
		if sec < 1 {
			sec = defaultNetworkTimeoutSec
		}
		// Faster fail on bad hosts; still loads normal ~/.ssh/config (keys, Host blocks).
		out = append(out, fmt.Sprintf("GIT_SSH_COMMAND=ssh -o ConnectTimeout=%d -o ConnectionAttempts=1", sec))
	}
	return out
}

func httpProxyEnvKey(key string) bool {
	switch strings.ToLower(key) {
	case "http_proxy", "https_proxy", "all_proxy":
		return true
	default:
		return false
	}
}

func runGitWithTimeout(repoPath, originURL string, budget time.Duration, usesSSH bool, args ...string) error {
	bin, err := gitOnPath()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), budget)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Dir = repoPath
	cmd.Env = gitNetworkEnv(originURL, usesSSH)
	out, err := cmd.CombinedOutput()
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return context.DeadlineExceeded
	}
	if err == nil {
		return nil
	}
	return gitCLIError(err, out)
}

func gitCLIError(err error, out []byte) error {
	text := strings.TrimSpace(string(out))
	lower := strings.ToLower(text + " " + err.Error())
	if strings.Contains(lower, "already up-to-date") || strings.Contains(lower, "everything up-to-date") {
		return git.NoErrAlreadyUpToDate
	}
	if text != "" {
		line, _, _ := strings.Cut(text, "\n")
		text = strings.TrimSpace(line)
		return fmt.Errorf("%s", text)
	}
	return err
}

func fetchOriginAt(repoPath, originURL string) error {
	if _, err := gitOnPath(); err != nil {
		return fetchOriginGoGit(repoPath, originURL)
	}
	usesSSH := isSSHRemoteURL(originURL)
	return withSSHTransportRetries(func() error {
		return withSSHNetworkLimit(usesSSH, originURL, func() error {
			return runGitWithTimeout(repoPath, originURL, networkOperationBudget(), usesSSH, "fetch", "--prune", "origin")
		})
	})
}

func runPullGit(path, branch string, originURL string, usesSSH bool) error {
	if _, err := gitOnPath(); err != nil {
		return err
	}
	return withSSHTransportRetries(func() error {
		return withSSHNetworkLimit(usesSSH, originURL, func() error {
			err := runGitWithTimeout(path, originURL, networkOperationBudget(), usesSSH, "pull", "--ff-only", "origin", branch)
			if err == git.NoErrAlreadyUpToDate {
				return nil
			}
			return err
		})
	})
}

type gitCompareResult struct {
	ahead, behind int
	ref           plumbing.ReferenceName
}

// gitCompareAheadBehind tries each remote-tracking ref and uses the one with the most commits to pull (behind).
func gitCompareAheadBehind(repoPath string, candidates []plumbing.ReferenceName) (gitCompareResult, error) {
	var best gitCompareResult
	var found bool
	var lastErr error
	for _, ref := range candidates {
		a, b, err := gitRevListAheadBehind(repoPath, ref)
		if err != nil {
			lastErr = err
			continue
		}
		if !found || b > best.behind || (b == best.behind && a > best.ahead) {
			best = gitCompareResult{ahead: a, behind: b, ref: ref}
			found = true
		}
	}
	if found {
		return best, nil
	}
	if lastErr != nil {
		return gitCompareResult{}, lastErr
	}
	return gitCompareResult{}, fmt.Errorf("no remote refs to compare")
}

// gitRevListAheadBehind counts commits vs a remote-tracking ref the way git does (left=behind, right=ahead).
func gitRevListAheadBehind(repoPath string, remoteTracking plumbing.ReferenceName) (ahead, behind int, err error) {
	bin, err := gitOnPath()
	if err != nil {
		return 0, 0, err
	}
	spec := remoteTracking.String() + "...HEAD"
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, "-C", repoPath, "rev-list", "--left-right", "--count", spec)
	cmd.Env = os.Environ()
	out, err := cmd.CombinedOutput()
	if err != nil {
		return 0, 0, fmt.Errorf("%s", strings.TrimSpace(string(out)))
	}
	fields := strings.Fields(strings.TrimSpace(string(out)))
	if len(fields) != 2 {
		return 0, 0, fmt.Errorf("unexpected rev-list output: %q", strings.TrimSpace(string(out)))
	}
	behind, err = strconv.Atoi(fields[0])
	if err != nil {
		return 0, 0, err
	}
	ahead, err = strconv.Atoi(fields[1])
	if err != nil {
		return 0, 0, err
	}
	return ahead, behind, nil
}

func runPushGit(path, branch, originURL string, usesSSH bool) error {
	if _, err := gitOnPath(); err != nil {
		return err
	}
	return withSSHTransportRetries(func() error {
		return withSSHNetworkLimit(usesSSH, originURL, func() error {
			return runGitWithTimeout(path, originURL, networkOperationBudget(), usesSSH, "push", "origin", branch)
		})
	})
}