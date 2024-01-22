package main

import (
	"github.com/go-git/go-git/plumbing/storer"
	git "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
)

func countRemoteChanges(r *git.Repository, headRef *plumbing.Reference, remoteRef *plumbing.Reference) (int, error) {
	// No remote changes if the hashes are the same
	if headRef.Hash() == remoteRef.Hash() {
		return 0, nil
	}

	cIter, err := r.Log(&git.LogOptions{From: remoteRef.Hash()})
	if err != nil {
		return 0, err
	}

	remoteChanges := 0
	err = cIter.ForEach(func(c *object.Commit) error {
		if c.Hash == headRef.Hash() {
			// Stop iteration when we reach the local HEAD
			return storer.ErrStop
		}
		remoteChanges++
		return nil
	})

	if err != nil && err != storer.ErrStop {
		return 0, err
	}

	return remoteChanges, nil
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
