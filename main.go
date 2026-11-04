// Package main provides a command line tool to check for git repositories in a directory tree
package main

import (
	"bufio"
	"bytes"
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

const defaultNetworkTimeoutSec = 10

type repoStatus struct {
	Path       string
	RepoName   string // origin project name for display; else derived from path
	Branch     string
	DirtyFiles int
	Ahead      int
	Behind     int
	Conflicts  int
	LastCommit time.Time
	LastPush   time.Time
	HasPush    bool
	Errs       []string
	PullNote    string
	PullOutcome pullOutcome
	PushNote    string
	PushOutcome pushOutcome

	LastReleaseTag    string
	LastRelease       time.Time
	HasLastRelease    bool
	SinceRelease      int // commits after LastReleaseTag; -1 if none/unknown
	SinceReleaseFiles int
	ReleaseCommits    []releaseCommit
	ReleasePaths      []string
}

var (
	appVersion      = "0.1.25"
	maxDepth        int
	showAll         bool
	reportCompact   bool
	countMetric     string
	onlyMetric      string
	listPaths       bool
	quietOutput     bool
	pullAll         bool
	mergeOnPull     bool
	pushAll         bool
	fetchTimeoutSec int
	releaseDiff     bool
	parallelJobs    int
	outputFormat    string
	jsonOutput      bool
	promptMode      bool
	hereOnly        bool
	noFetch         bool
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
	st.Conflicts = countConflicts(status)

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
		enrichRelease(r, headRef.Hash(), &st, releaseDiff)
		return st
	}
	if originURL == "" {
		st.Errs = append(st.Errs, "origin has no URL")
		enrichRelease(r, headRef.Hash(), &st, releaseDiff)
		return st
	}
	st.RepoName = originRepoName(originURL)

	if !noFetch {
		tryFetch := func() {
			writeStatus("Fetching", path)
			err := fetchOrigin(r, originURL)
			if err != nil && err != git.NoErrAlreadyUpToDate {
				kind := "fetch"
				if isOriginAuthError(err) {
					kind = "auth"
				}
				st.Errs = append(st.Errs, userError(kind, err))
			}
		}
		tryFetch()
	}

	remoteRef, err := r.Reference(plumbing.NewRemoteReferenceName("origin", st.Branch), true)
	if err != nil {
		st.Errs = append(st.Errs, originRefError(st.Branch, err))
		enrichRelease(r, headRef.Hash(), &st, releaseDiff)
		return st
	}

	writeStatus("Comparing", path)
	ahead, behind, err := countAheadBehind(r, headRef, remoteRef)
	if err != nil {
		st.Errs = append(st.Errs, userError("compare", err))
		enrichRelease(r, remoteRef.Hash(), &st, releaseDiff)
		return st
	}
	st.Ahead = ahead
	st.Behind = behind

	if t, err := commitTime(r, remoteRef.Hash()); err == nil {
		st.LastPush = t
		st.HasPush = true
	}

	enrichRelease(r, remoteRef.Hash(), &st, releaseDiff)
	return st
}

func scanExitCode(repos []repoStatus) int {
	for _, r := range repos {
		if len(r.Errs) > 0 {
			return 1
		}
	}
	return 0
}

func normalizeOutputFormat(format string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(format)) {
	case "", "text", "human":
		return "text", nil
	case "json", "jsonl":
		return "json", nil
	default:
		return "", fmt.Errorf("unknown output format %q (use text or json)", format)
	}
}

func runScan(root string) int {
	repos = nil

	if jsonOutput {
		outputFormat = "json"
	}
	if promptMode {
		hereOnly = true
		quietOutput = true
		colourMode = "never"
	}

	format, err := normalizeOutputFormat(outputFormat)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 2
	}
	outputFormat = format

	if countMetric != "" && onlyMetric != "" {
		fmt.Fprintf(os.Stderr, "error: use either --count or --only, not both\n")
		return 2
	}
	if countMetric != "" && onlyMetric != "" {
		fmt.Fprintf(os.Stderr, "error: use either --count or --only, not both\n")
		return 2
	}
	if mergeOnPull && !pullAll {
		fmt.Fprintf(os.Stderr, "error: --merge requires --pull\n")
		return 2
	}
	if promptMode && (countMetric != "" || onlyMetric != "" || listPaths || pullAll || pushAll || outputFormat == "json") {
		fmt.Fprintf(os.Stderr, "error: --prompt cannot be used with --count, --only, --list, --pull, --push, or --format json\n")
		return 2
	}
	if outputFormat == "json" && (countMetric != "" || listPaths || promptMode) {
		fmt.Fprintf(os.Stderr, "error: --format json cannot be used with --count, --list, or --prompt\n")
		return 2
	}
	if (pullAll || pushAll) && (countMetric != "" || listPaths || outputFormat == "json") {
		fmt.Fprintf(os.Stderr, "error: --pull/--push cannot be used with --count, --list, or --format json\n")
		return 2
	}
	var err2 error
	countMetric, err2 = normalizeMetric(countMetric)
	if err2 != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err2)
		return 2
	}
	onlyMetric, err2 = normalizeMetric(onlyMetric)
	if err2 != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err2)
		return 2
	}

	initSSHAgent()
	if err := initFilteredKnownHosts(); err != nil && !quietOutput {
		fmt.Fprintf(os.Stderr, "warning: %s (SSH fetch may fail)\n", userError("known_hosts", err))
	}

	paths, err := collectGitRepoPaths(root, 0, hereOnly)
	if err != nil {
		clearStatus()
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 2
	}
	repos = checkRepositoriesParallel(paths)

	clearStatus()
	if pullAll {
		applyPullAll(mergeOnPull)
		clearStatus()
	}
	if pushAll {
		applyPushAll()
		clearStatus()
	}
	rows := reposForDisplay(repos, showAll, onlyMetric, pullAll || pushAll)

	if promptMode {
		fmt.Println(countMatching(repos, metricAttention))
		return scanExitCode(repos)
	}

	if countMetric != "" {
		printCountReport(os.Stdout, repos, countMetric, quietOutput)
		return scanExitCode(repos)
	}

	if listPaths {
		for _, r := range rows {
			fmt.Println(r.Path)
		}
		return scanExitCode(repos)
	}

	if outputFormat == "json" {
		printJSONReport(os.Stdout, rows)
		return scanExitCode(repos)
	}

	printReport(os.Stdout, rows)
	return scanExitCode(repos)
}

func main() {
	rootCmd := &cobra.Command{
		Use:   "gitcng [path]",
		Short: "Scan a tree of git repos and print local/remote status",
		Long: strings.TrimSpace(`
Scan a directory tree for git repositories and print local/remote status.

Default output is a multi-line block per repo (see Report below).
Use --compact for one line per repo. Use --all to include clean repos.

Script mode:
  gitcng --count remote ..           # summary + table
  gitcng -q --count remote ..        # one repo: Remote count only; several: path + Remote per line
  gitcng --only ahead --list ..      # paths of repos with unpushed commits

METRIC for --count and --only:
  uncommitted, local — dirty files
  remote             — commits to pull from origin
  ahead              — commits to push
  conflicts          — merge conflict files
  diverged           — ahead and behind at once
  errors             — fetch/auth/etc.
  attention          — anything that needs action
  repos              — every repo found

--count and --only cannot be used together.
`),
		Example: strings.TrimSpace(`
  gitcng ../web
  gitcng --compact --all ..
  gitcng -q --count uncommitted ../web
  if [ "$(gitcng -q --count remote ..)" -gt 0 ]; then git pull; fi
  gitcng --only errors --list ..
`),
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
	rootCmd.Flags().BoolVar(&reportCompact, "compact", false, "print each repository on one line (default is two lines with spacing)")
	rootCmd.Flags().StringVar(&countMetric, "count", "",
		"count repos matching METRIC and list each with its value; -q: one repo prints value only, several print path+value; METRIC: "+metricNamesListed)
	rootCmd.Flags().StringVar(&onlyMetric, "only", "",
		"report only repos matching METRIC; METRIC is one of: "+metricNamesListed)
	rootCmd.Flags().BoolVar(&listPaths, "list", false, "print matching repository paths, one per line")
	rootCmd.Flags().BoolVarP(&quietOutput, "quiet", "q", false, "suppress scan progress on stderr")
	rootCmd.Flags().BoolVar(&pullAll, "pull", false, "git pull: fast-forward from origin when only remote commits are pending (clean working tree)")
	rootCmd.Flags().BoolVar(&mergeOnPull, "merge", false, "with --pull: merge local and remote commits when they do not conflict, then push to origin")
	rootCmd.Flags().BoolVar(&pushAll, "push", false, "git push: push local commits to origin when the working tree is clean and nothing is left to pull")
	rootCmd.Flags().IntVar(&fetchTimeoutSec, "fetch-timeout", defaultNetworkTimeoutSec, "max seconds for each origin fetch and SSH dial (0 uses 10)")
	rootCmd.Flags().BoolVar(&releaseDiff, "release-diff", false, "list commits and changed files since the latest release tag (semver, else newest tag by date)")
	rootCmd.Flags().IntVar(&parallelJobs, "jobs", 0, "check repositories in parallel (0 = auto, up to 16 workers)")
	rootCmd.Flags().StringVar(&outputFormat, "format", "text", "output format: text (default) or json (one JSON object per repo)")
	rootCmd.Flags().BoolVar(&jsonOutput, "json", false, "same as --format json")
	rootCmd.Flags().BoolVar(&promptMode, "prompt", false, "print one-line attention count for shell prompts (implies -q, --colour never, --here)")
	rootCmd.Flags().BoolVar(&hereOnly, "here", false, "only check path if it is a git repo; do not scan subdirectories")
	rootCmd.Flags().BoolVar(&noFetch, "no-fetch", false, "skip origin fetch and use cached refs (faster; remote counts may be stale)")
	rootCmd.Flags().StringVar(&colourMode, "colour", "auto", "colourise output: auto, always, or never (also NO_COLOR/NO_COLOUR, FORCE_COLOR/FORCE_COLOUR)")

	registerCompletions(rootCmd)

	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}
