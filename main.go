// Package main provides a command line tool to check for git repositories in a directory tree
package main

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	git "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	gogitssh "github.com/go-git/go-git/v5/plumbing/transport/ssh"
	"github.com/spf13/cobra"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
	xknownhosts "golang.org/x/crypto/ssh/knownhosts"
)

const defaultNetworkTimeoutSec = 3

type repoStatus struct {
	Path       string
	Branch     string
	DirtyFiles int
	Ahead      int
	Behind     int
	LastCommit time.Time
	LastPush   time.Time
	HasPush    bool
	Errs       []string
}

var (
	appVersion      = "0.0.17"
	maxDepth        int
	showAll         bool
	fetchTimeoutSec int
	repos           []repoStatus

	sshAgentOK  bool
	sshAgentErr error

	hostKeyOnce sync.Once
	hostKeyCb   ssh.HostKeyCallback
	hostKeyErr  error
)

// sshAuthTimeout caps SSH dial/handshake time (go-git uses ClientConfig.Timeout).
type sshAuthTimeout struct {
	inner   gogitssh.AuthMethod
	timeout time.Duration
}

func (a *sshAuthTimeout) Name() string   { return a.inner.Name() }
func (a *sshAuthTimeout) String() string { return a.inner.String() }

func (a *sshAuthTimeout) ClientConfig() (*ssh.ClientConfig, error) {
	cfg, err := a.inner.ClientConfig()
	if err != nil {
		return nil, err
	}
	cfg.Timeout = a.timeout
	return cfg, nil
}

func networkTimeout() time.Duration {
	if fetchTimeoutSec > 0 {
		return time.Duration(fetchTimeoutSec) * time.Second
	}
	return time.Duration(defaultNetworkTimeoutSec) * time.Second
}

func initFilteredKnownHosts() error {
	hostKeyOnce.Do(func() {
		path, err := filteredKnownHosts()
		if err != nil {
			hostKeyErr = err
			return
		}
		// So go-git’s SSH fallback (if any) also uses the filtered file.
		os.Setenv("SSH_KNOWN_HOSTS", path)
		hostKeyCb, hostKeyErr = xknownhosts.New(path)
	})
	return hostKeyErr
}

func initSSHAgent() {
	signers, err := getSignersFromAgent()
	if err != nil {
		sshAgentErr = err
		return
	}
	if len(signers) == 0 {
		sshAgentErr = fmt.Errorf("no SSH keys loaded in ssh-agent")
		return
	}
	sshAgentOK = true
}

func knownHostsFiles() []string {
	if v := os.Getenv("SSH_KNOWN_HOSTS"); v != "" {
		return filepath.SplitList(v)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	return []string{
		filepath.Join(home, ".ssh", "known_hosts"),
		"/etc/ssh/ssh_known_hosts",
	}
}

// filteredKnownHosts writes a temporary known_hosts that omits lines Go cannot
// parse, such as a 512-bit DSA key. One bad line otherwise rejects the file.
func filteredKnownHosts() (string, error) {
	var b strings.Builder
	var kept int
	for _, name := range knownHostsFiles() {
		data, err := os.ReadFile(name)
		if err != nil {
			continue
		}
		sc := bufio.NewScanner(bytes.NewReader(data))
		sc.Buffer(make([]byte, 64*1024), 1024*1024)
		for sc.Scan() {
			trim := strings.TrimSpace(sc.Text())
			if trim == "" || strings.HasPrefix(trim, "#") {
				continue
			}
			if _, _, _, _, _, err := ssh.ParseKnownHosts([]byte(trim + "\n")); err != nil {
				continue
			}
			b.WriteString(trim)
			b.WriteByte('\n')
			kept++
		}
		if err := sc.Err(); err != nil {
			return "", err
		}
	}
	if kept == 0 {
		return "", fmt.Errorf("no usable known_hosts entries")
	}
	f, err := os.CreateTemp("", "gitcng-known-hosts-*")
	if err != nil {
		return "", err
	}
	if _, err := f.WriteString(b.String()); err != nil {
		f.Close()
		os.Remove(f.Name())
		return "", err
	}
	if err := f.Close(); err != nil {
		os.Remove(f.Name())
		return "", err
	}
	return f.Name(), nil
}

func getSignersFromAgent() ([]ssh.Signer, error) {
	socket := os.Getenv("SSH_AUTH_SOCK")
	if socket == "" {
		return nil, fmt.Errorf("SSH_AUTH_SOCK is not set")
	}
	conn, err := net.Dial("unix", socket)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	agentClient := agent.NewClient(conn)
	return agentClient.Signers()
}

func checkRepository(path string, r *git.Repository) repoStatus {
	st := repoStatus{Path: path}

	w, err := r.Worktree()
	if err != nil {
		st.Errs = append(st.Errs, userError("worktree", err))
		return st
	}

	status, err := w.Status()
	if err != nil {
		st.Errs = append(st.Errs, userError("status", err))
		return st
	}
	st.DirtyFiles = countChanges(status)

	headRef, err := r.Head()
	if err != nil {
		st.Errs = append(st.Errs, userError("head", err))
		return st
	}
	st.Branch = headRef.Name().Short()

	if t, err := commitTime(r, headRef.Hash()); err == nil {
		st.LastCommit = t
	}

	remotes, err := r.Remotes()
	if err != nil {
		st.Errs = append(st.Errs, userError("remotes", err))
		return st
	}

	originURL, hasOrigin := originRemoteURL(remotes)
	if !hasOrigin {
		return st
	}
	if originURL == "" {
		st.Errs = append(st.Errs, "origin has no URL")
		return st
	}

	tryFetch := func() {
		opts := &git.FetchOptions{RemoteName: "origin"}
		if isSSHRemoteURL(originURL) {
			if !sshAgentOK {
				st.Errs = append(st.Errs, userError("ssh-agent", sshAgentErr))
				return
			}
			auth, err := gogitssh.NewSSHAgentAuth(sshUsernameFromURL(originURL))
			if err != nil {
				st.Errs = append(st.Errs, userError("auth", err))
				return
			}
			if hostKeyCb == nil {
				st.Errs = append(st.Errs, "known_hosts: no host key callback")
				return
			}
			auth.HostKeyCallback = hostKeyCb
			opts.Auth = &sshAuthTimeout{inner: auth, timeout: networkTimeout()}
		}
		writeStatus("Fetching", path)
		ctx, cancel := context.WithTimeout(context.Background(), networkTimeout())
		defer cancel()
		err := r.FetchContext(ctx, opts)
		if err != nil && err != git.NoErrAlreadyUpToDate {
			st.Errs = append(st.Errs, userError("fetch", err))
		}
	}
	tryFetch()

	remoteRef, err := r.Reference(plumbing.NewRemoteReferenceName("origin", st.Branch), true)
	if err != nil {
		st.Errs = append(st.Errs, originRefError(st.Branch, err))
		return st
	}

	writeStatus("Comparing", path)
	ahead, behind, err := countAheadBehind(r, headRef, remoteRef)
	if err != nil {
		st.Errs = append(st.Errs, userError("compare", err))
		return st
	}
	st.Ahead = ahead
	st.Behind = behind

	if t, err := commitTime(r, remoteRef.Hash()); err == nil {
		st.LastPush = t
		st.HasPush = true
	}
	return st
}

func visitAndCheckGit(path string, depth int) error {
	if depth > maxDepth {
		return nil
	}

	writeStatus("Visiting", path)

	r, err := git.PlainOpen(path)
	if err == nil {
		writeStatus("Checking", path)
		st := checkRepository(path, r)
		repos = append(repos, st)
		return nil
	}

	files, err := os.ReadDir(path)
	if err != nil {
		if depth == 0 {
			return err
		}
		return nil
	}

	for _, f := range files {
		if f.Name() == ".git" {
			continue
		}
		if f.Type().IsDir() && f.Type()&os.ModeSymlink != os.ModeSymlink {
			if err := visitAndCheckGit(filepath.Join(path, f.Name()), depth+1); err != nil {
				return err
			}
		}
	}
	return nil
}

func runScan(root string) int {
	repos = nil
	initSSHAgent()
	if err := initFilteredKnownHosts(); err != nil {
		fmt.Fprintf(os.Stderr, "warning: %s (SSH fetch may fail)\n", userError("known_hosts", err))
	}

	if err := visitAndCheckGit(root, 0); err != nil {
		clearStatus()
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 2
	}

	clearStatus()
	printReport(os.Stdout, repos, showAll)
	for _, r := range repos {
		if len(r.Errs) > 0 {
			return 1
		}
	}
	return 0
}

func main() {
	rootCmd := &cobra.Command{
		Use:     "gitcng [path]",
		Short:   "Check for git repositories in a directory tree",
		Version: appVersion,
		Args:    cobra.MaximumNArgs(1),
		Run: func(_ *cobra.Command, args []string) {
			root := "."
			if len(args) > 0 {
				root = args[0]
			}
			os.Exit(runScan(root))
		},
	}

	rootCmd.Flags().IntVar(&maxDepth, "depth", 4, "the maximum depth")
	rootCmd.Flags().BoolVar(&showAll, "all", false, "list every repository, including ones with no local or remote changes")
	rootCmd.Flags().IntVar(&fetchTimeoutSec, "fetch-timeout", defaultNetworkTimeoutSec, "max seconds for each origin fetch and SSH dial (0 uses 3)")
	rootCmd.Flags().StringVar(&colorMode, "color", "auto", "colorize output: auto, always, or never")

	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}
