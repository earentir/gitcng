package main

import (
	"fmt"
	"net/url"
	"path"
	"strings"
	"time"

	git "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/plumbing/storer"
)

func commitTime(r *git.Repository, hash plumbing.Hash) (time.Time, error) {
	c, err := r.CommitObject(hash)
	if err != nil {
		return time.Time{}, err
	}
	return c.Committer.When, nil
}

func countChanges(status git.Status) int {
	changes := 0
	for _, s := range status {
		if s.Staging != git.Unmodified || s.Worktree != git.Unmodified {
			changes++
		}
	}
	return changes
}

func countConflicts(status git.Status) int {
	n := 0
	for _, s := range status {
		if s.Staging == git.UpdatedButUnmerged || s.Worktree == git.UpdatedButUnmerged {
			n++
		}
	}
	return n
}

func countAheadBehind(r *git.Repository, headRef, remoteRef *plumbing.Reference) (ahead, behind int, err error) {
	if headRef.Hash() == remoteRef.Hash() {
		return 0, 0, nil
	}

	headCommit, err := r.CommitObject(headRef.Hash())
	if err != nil {
		return 0, 0, err
	}
	remoteCommit, err := r.CommitObject(remoteRef.Hash())
	if err != nil {
		return 0, 0, err
	}

	bases, err := headCommit.MergeBase(remoteCommit)
	if err != nil {
		return 0, 0, err
	}
	if len(bases) == 0 {
		return 0, 0, fmt.Errorf("no merge base between %s and %s", headRef.Hash(), remoteRef.Hash())
	}

	baseHash := bases[0].Hash
	ahead, err = countCommitsUntil(r, headRef.Hash(), baseHash)
	if err != nil {
		return 0, 0, err
	}
	behind, err = countCommitsUntil(r, remoteRef.Hash(), baseHash)
	if err != nil {
		return 0, 0, err
	}
	return ahead, behind, nil
}

func countCommitsUntil(r *git.Repository, from, stop plumbing.Hash) (int, error) {
	if from == stop {
		return 0, nil
	}

	cIter, err := r.Log(&git.LogOptions{From: from})
	if err != nil {
		return 0, err
	}

	count := 0
	err = cIter.ForEach(func(c *object.Commit) error {
		if c.Hash == stop {
			return storer.ErrStop
		}
		count++
		return nil
	})
	if err != nil && err != storer.ErrStop {
		return 0, err
	}
	return count, nil
}

func countCommitsFromTip(r *git.Repository, from plumbing.Hash) (int, error) {
	cIter, err := r.Log(&git.LogOptions{From: from})
	if err != nil {
		return 0, err
	}
	count := 0
	err = cIter.ForEach(func(c *object.Commit) error {
		count++
		return nil
	})
	return count, err
}

// originTrackingRef is the remote-tracking ref for the current branch (from branch.*.merge, else origin/<local>).
func originTrackingRef(r *git.Repository, localBranch string) plumbing.ReferenceName {
	cfg, err := r.Config()
	if err == nil {
		if b, ok := cfg.Branches[localBranch]; ok && b.Merge != "" {
			remote := b.Remote
			if remote == "" {
				remote = "origin"
			}
			merge := plumbing.ReferenceName(b.Merge)
			if merge.IsBranch() {
				return plumbing.NewRemoteReferenceName(remote, merge.Short())
			}
		}
	}
	return plumbing.NewRemoteReferenceName("origin", localBranch)
}

func remoteCompareCandidates(r *git.Repository, localBranch string) []plumbing.ReferenceName {
	seen := map[plumbing.ReferenceName]bool{}
	var out []plumbing.ReferenceName
	add := func(n plumbing.ReferenceName) {
		if n == "" || seen[n] {
			return
		}
		seen[n] = true
		out = append(out, n)
	}
	add(originTrackingRef(r, localBranch))
	add(plumbing.NewRemoteReferenceName("origin", localBranch))
	head, err := r.Reference(plumbing.NewRemoteReferenceName("origin", "HEAD"), true)
	if err == nil && head.Type() == plumbing.SymbolicReference {
		add(head.Target())
	}
	return out
}

func resolveOriginRemoteRef(r *git.Repository, localBranch string) (*plumbing.Reference, error) {
	for _, name := range remoteCompareCandidates(r, localBranch) {
		ref, err := r.Reference(name, true)
		if err == nil {
			return ref, nil
		}
	}
	return nil, fmt.Errorf("no origin ref for branch %q (try: git fetch origin)", localBranch)
}

func remoteBranchShortFromRef(ref *plumbing.Reference) string {
	name := ref.Name()
	if !name.IsRemote() {
		return name.Short()
	}
	const prefix = "refs/remotes/"
	s := name.String()
	if !strings.HasPrefix(s, prefix) {
		return name.Short()
	}
	rest := strings.TrimPrefix(s, prefix)
	if i := strings.Index(rest, "/"); i >= 0 {
		return rest[i+1:]
	}
	return rest
}

func originRemoteURL(remotes []*git.Remote) (url string, hasOrigin bool) {
	for _, remote := range remotes {
		if remote.Config().Name != "origin" {
			continue
		}
		hasOrigin = true
		urls := remote.Config().URLs
		if len(urls) == 0 {
			return "", true
		}
		return urls[0], true
	}
	return "", false
}

// originRepoName is the repository slug from origin (e.g. pkg-auth from …/pkg-auth.git).
func originRepoName(originURL string) string {
	u := strings.TrimSpace(originURL)
	if u == "" {
		return ""
	}
	if strings.HasSuffix(u, ".git") {
		u = u[:len(u)-4]
	}
	if parsed, err := url.Parse(u); err == nil && parsed.Path != "" && parsed.Host != "" {
		base := path.Base(strings.Trim(parsed.Path, "/"))
		if base != "" && base != "." {
			return base
		}
	}
	if i := strings.LastIndex(u, "/"); i >= 0 {
		return u[i+1:]
	}
	if i := strings.LastIndex(u, ":"); i >= 0 {
		rest := u[i+1:]
		if j := strings.LastIndex(rest, "/"); j >= 0 {
			return rest[j+1:]
		}
		return rest
	}
	return ""
}

func originHostLabel(originURL string) string {
	u := strings.TrimSpace(originURL)
	if u == "" {
		return ""
	}
	if strings.HasPrefix(u, "git@") {
		host, _, _ := strings.Cut(strings.TrimPrefix(u, "git@"), ":")
		return host
	}
	parsed, err := url.Parse(u)
	if err == nil && parsed.Host != "" {
		return parsed.Host
	}
	return u
}

func isWellKnownPublicGitHost(originURL string) bool {
	u := strings.ToLower(originURL)
	for _, h := range []string{
		"github.com", "ssh.github.com",
		"gitlab.com", "bitbucket.org", "codeberg.org",
	} {
		if strings.Contains(u, h) {
			return true
		}
	}
	return false
}

func isSSHRemoteURL(remoteURL string) bool {
	if strings.HasPrefix(remoteURL, "git@") {
		return true
	}
	u, err := url.Parse(remoteURL)
	if err != nil {
		return false
	}
	return u.Scheme == "ssh" || u.Scheme == "git"
}

func sshUsernameFromURL(remoteURL string) string {
	if strings.HasPrefix(remoteURL, "git@") {
		parts := strings.SplitN(remoteURL, "@", 2)
		if len(parts) > 0 && parts[0] != "" {
			return parts[0]
		}
	}
	u, err := url.Parse(remoteURL)
	if err != nil {
		return "git"
	}
	if u.User != nil {
		if name := u.User.Username(); name != "" {
			return name
		}
	}
	return "git"
}
