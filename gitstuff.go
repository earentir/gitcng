package main

import (
	"fmt"
	"net/url"
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
