package main

import (
	"fmt"
	"sort"
	"strings"
	"time"

	git "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/plumbing/storer"
	"golang.org/x/mod/semver"
)

type releaseCommit struct {
	Short   string
	Subject string
	When    time.Time
}

// enrichRelease compares tip to the latest release tag: newest semver (vX.Y.Z), else newest tag by date.
func enrichRelease(r *git.Repository, tip plumbing.Hash, st *repoStatus, detailed bool) {
	tagName, tagHash, ok, err := latestReleaseTag(r)
	if err != nil {
		st.Errs = append(st.Errs, userError("release", err))
		return
	}
	if !ok {
		st.SinceRelease = -1
		return
	}
	if err := setReleaseTagMeta(r, st, tagName, tagHash); err != nil {
		st.Errs = append(st.Errs, userError("release", err))
		return
	}

	if tip == tagHash {
		st.SinceRelease = 0
		return
	}

	tagCommit, err := r.CommitObject(tagHash)
	if err != nil {
		st.Errs = append(st.Errs, userError("release", err))
		return
	}
	tipCommit, err := r.CommitObject(tip)
	if err != nil {
		st.Errs = append(st.Errs, userError("release", err))
		return
	}
	bases, err := tipCommit.MergeBase(tagCommit)
	if err != nil {
		st.Errs = append(st.Errs, userError("release", err))
		return
	}
	if len(bases) == 0 || bases[0].Hash != tagHash {
		st.Errs = append(st.Errs, fmt.Sprintf("release: tag %s is not an ancestor of HEAD", tagName))
		st.SinceRelease = -1
		return
	}

	n, err := countCommitsUntil(r, tip, tagHash)
	if err != nil {
		st.Errs = append(st.Errs, userError("release", err))
		return
	}
	st.SinceRelease = n

	if n == 0 {
		return
	}

	paths, err := changedPathsBetween(r, tagHash, tip)
	if err != nil {
		st.Errs = append(st.Errs, userError("release", err))
		return
	}
	st.SinceReleaseFiles = len(paths)

	if detailed {
		st.ReleasePaths = paths
		commits, err := listCommitsSince(r, tip, tagHash)
		if err != nil {
			st.Errs = append(st.Errs, userError("release", err))
			return
		}
		st.ReleaseCommits = commits
	}
}

func latestReleaseTag(r *git.Repository) (displayName string, commit plumbing.Hash, ok bool, err error) {
	name, hash, ok, err := latestSemverTag(r)
	if err != nil || ok {
		return name, hash, ok, err
	}
	return latestTagByDate(r)
}

func latestSemverTag(r *git.Repository) (displayName string, commit plumbing.Hash, ok bool, err error) {
	iter, err := r.Tags()
	if err != nil {
		return "", plumbing.ZeroHash, false, err
	}

	bestCanon := ""
	bestDisplay := ""
	bestHash := plumbing.ZeroHash

	err = iter.ForEach(func(ref *plumbing.Reference) error {
		short := ref.Name().Short()
		canon := canonicalSemverTag(short)
		if canon == "" {
			return nil
		}
		if bestCanon != "" && semver.Compare(canon, bestCanon) <= 0 {
			return nil
		}
		hash, err := tagCommitHash(r, ref)
		if err != nil {
			return nil
		}
		bestCanon = canon
		bestDisplay = short
		bestHash = hash
		return nil
	})
	if err != nil {
		return "", plumbing.ZeroHash, false, err
	}
	if bestCanon == "" {
		return "", plumbing.ZeroHash, false, nil
	}
	return bestDisplay, bestHash, true, nil
}

func latestTagByDate(r *git.Repository) (displayName string, commit plumbing.Hash, ok bool, err error) {
	iter, err := r.Tags()
	if err != nil {
		return "", plumbing.ZeroHash, false, err
	}

	var bestName string
	bestHash := plumbing.ZeroHash
	var bestWhen time.Time

	err = iter.ForEach(func(ref *plumbing.Reference) error {
		short := ref.Name().Short()
		hash, err := tagCommitHash(r, ref)
		if err != nil {
			return nil
		}
		when, err := tagTime(r, ref, hash)
		if err != nil {
			return nil
		}
		if bestName == "" || when.After(bestWhen) || (when.Equal(bestWhen) && short > bestName) {
			bestName = short
			bestHash = hash
			bestWhen = when
		}
		return nil
	})
	if err != nil {
		return "", plumbing.ZeroHash, false, err
	}
	if bestName == "" {
		return "", plumbing.ZeroHash, false, nil
	}
	return bestName, bestHash, true, nil
}

func tagTime(r *git.Repository, ref *plumbing.Reference, commitHash plumbing.Hash) (time.Time, error) {
	tagObj, err := r.TagObject(ref.Hash())
	if err == nil {
		return tagObj.Tagger.When, nil
	}
	c, err := r.CommitObject(commitHash)
	if err != nil {
		return time.Time{}, err
	}
	return c.Committer.When, nil
}

func canonicalSemverTag(name string) string {
	t := strings.TrimSpace(name)
	if t == "" {
		return ""
	}
	if !strings.HasPrefix(t, "v") {
		t = "v" + t
	}
	if !semver.IsValid(t) {
		return ""
	}
	return semver.Canonical(t)
}

func tagCommitHash(r *git.Repository, ref *plumbing.Reference) (plumbing.Hash, error) {
	tagObj, err := r.TagObject(ref.Hash())
	if err == nil {
		return tagObj.Target, nil
	}
	return ref.Hash(), nil
}

func listCommitsSince(r *git.Repository, from, stop plumbing.Hash) ([]releaseCommit, error) {
	cIter, err := r.Log(&git.LogOptions{From: from})
	if err != nil {
		return nil, err
	}

	var out []releaseCommit
	err = cIter.ForEach(func(c *object.Commit) error {
		if c.Hash == stop {
			return storer.ErrStop
		}
		out = append(out, releaseCommit{
			Short:   c.Hash.String()[:7],
			Subject: strings.TrimSpace(firstLine(c.Message)),
			When:    c.Committer.When,
		})
		return nil
	})
	if err != nil && err != storer.ErrStop {
		return nil, err
	}
	return out, nil
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

func changedPathsBetween(r *git.Repository, older, newer plumbing.Hash) ([]string, error) {
	oldCommit, err := r.CommitObject(older)
	if err != nil {
		return nil, err
	}
	newCommit, err := r.CommitObject(newer)
	if err != nil {
		return nil, err
	}
	oldTree, err := oldCommit.Tree()
	if err != nil {
		return nil, err
	}
	newTree, err := newCommit.Tree()
	if err != nil {
		return nil, err
	}
	changes, err := oldTree.Diff(newTree)
	if err != nil {
		return nil, err
	}
	seen := make(map[string]struct{})
	for _, ch := range changes {
		for _, name := range []string{ch.From.Name, ch.To.Name} {
			if name == "" {
				continue
			}
			seen[name] = struct{}{}
		}
	}
	paths := make([]string, 0, len(seen))
	for p := range seen {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	return paths, nil
}

func setReleaseTagMeta(r *git.Repository, st *repoStatus, tagName string, tagHash plumbing.Hash) error {
	st.LastReleaseTag = tagName
	ref, err := r.Reference(plumbing.NewTagReferenceName(tagName), true)
	if err == nil {
		when, err := tagTime(r, ref, tagHash)
		if err != nil {
			return err
		}
		st.LastRelease = when
		st.HasLastRelease = true
		return nil
	}
	when, err := commitTime(r, tagHash)
	if err != nil {
		return err
	}
	st.LastRelease = when
	st.HasLastRelease = true
	return nil
}
