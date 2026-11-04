package main

import (
	"context"
	"fmt"
	"path"
	"sort"
	"strings"

	git "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/filemode"
	"github.com/go-git/go-git/v5/plumbing/object"
	gogitssh "github.com/go-git/go-git/v5/plumbing/transport/ssh"
)

type pullOutcome int

const (
	pullNone pullOutcome = iota
	pullSkipped
	pullDone
	pullFailed
)

type pushOutcome int

const (
	pushNone pushOutcome = iota
	pushSkipped
	pushDone
	pushFailed
)

func openRepoOrigin(path string) (*git.Repository, string, gogitssh.AuthMethod, error) {
	r, err := git.PlainOpen(path)
	if err != nil {
		return nil, "", nil, err
	}
	remotes, err := r.Remotes()
	if err != nil {
		return nil, "", nil, err
	}
	originURL, ok := originRemoteURL(remotes)
	if !ok {
		return nil, "", nil, fmt.Errorf("no origin remote")
	}
	if originURL == "" {
		return nil, "", nil, fmt.Errorf("origin has no URL")
	}
	auth, err := authForOriginURL(originURL)
	if err != nil {
		return nil, "", nil, err
	}
	return r, originURL, auth, nil
}

func sshWrappedAuth(auth gogitssh.AuthMethod) gogitssh.AuthMethod {
	if auth == nil {
		return nil
	}
	return &sshAuthTimeout{inner: auth, timeout: networkTimeout()}
}

func authForOriginURL(originURL string) (gogitssh.AuthMethod, error) {
	if !isSSHRemoteURL(originURL) {
		return nil, nil
	}
	if !sshAgentOK {
		if sshAgentErr != nil {
			return nil, sshAgentErr
		}
		return nil, fmt.Errorf("no SSH keys loaded in ssh-agent")
	}
	auth, err := gogitssh.NewSSHAgentAuth(sshUsernameFromURL(originURL))
	if err != nil {
		return nil, err
	}
	if hostKeyCb == nil {
		return nil, fmt.Errorf("known_hosts: no host key callback")
	}
	auth.HostKeyCallback = hostKeyCb
	return auth, nil
}

func runPullContext(path string, auth gogitssh.AuthMethod) error {
	r, err := git.PlainOpen(path)
	if err != nil {
		return err
	}
	w, err := r.Worktree()
	if err != nil {
		return err
	}
	opts := &git.PullOptions{RemoteName: "origin"}
	usesSSH := auth != nil
	if usesSSH {
		opts.Auth = sshWrappedAuth(auth)
	}
	return withSSHTransportRetries(func() error {
		return withSSHNetworkLimit(usesSSH, func() error {
			ctx, cancel := context.WithTimeout(context.Background(), networkTimeout())
			defer cancel()
			err := w.PullContext(ctx, opts)
			if err == git.NoErrAlreadyUpToDate {
				return nil
			}
			return err
		})
	})
}

func runPush(path, branch string, auth gogitssh.AuthMethod) error {
	r, err := git.PlainOpen(path)
	if err != nil {
		return err
	}
	remote, err := r.Remote("origin")
	if err != nil {
		return err
	}
	ref := plumbing.NewBranchReferenceName(branch)
	opts := &git.PushOptions{
		RemoteName: "origin",
		RefSpecs:   []config.RefSpec{config.RefSpec(ref.String() + ":" + ref.String())},
	}
	usesSSH := auth != nil
	if usesSSH {
		opts.Auth = sshWrappedAuth(auth)
	}
	return withSSHTransportRetries(func() error {
		return withSSHNetworkLimit(usesSSH, func() error {
			ctx, cancel := context.WithTimeout(context.Background(), networkTimeout())
			defer cancel()
			err := remote.PushContext(ctx, opts)
			if err == git.NoErrAlreadyUpToDate {
				return nil
			}
			return err
		})
	})
}

func (r repoStatus) syncBlockReason() string {
	if len(r.Errs) > 0 {
		return "fix scan errors first"
	}
	if r.Conflicts > 0 {
		return fmt.Sprintf("%d merge conflict(s) locally", r.Conflicts)
	}
	if r.DirtyFiles > 0 {
		return fmt.Sprintf("%d uncommitted change(s)", r.DirtyFiles)
	}
	return ""
}

func refreshRepoStatus(i int) {
	r, err := git.PlainOpen(repos[i].Path)
	if err != nil {
		repos[i].Errs = append(repos[i].Errs, userError("open", err))
		return
	}
	note := repos[i].PullNote
	pushNote := repos[i].PushNote
	pullOut := repos[i].PullOutcome
	pushOut := repos[i].PushOutcome
	repos[i] = checkRepository(repos[i].Path, r)
	repos[i].PullNote = note
	repos[i].PushNote = pushNote
	repos[i].PullOutcome = pullOut
	repos[i].PushOutcome = pushOut
}

func applyPullAll(mergeAndPush bool) {
	for i := range repos {
		st := repos[i]
		if st.Behind == 0 {
			continue
		}
		if reason := st.syncBlockReason(); reason != "" {
			repos[i].PullNote = fmt.Sprintf("not pulled: %s", reason)
			repos[i].PullOutcome = pullSkipped
			continue
		}

		_, _, auth, err := openRepoOrigin(st.Path)
		if err != nil {
			repos[i].PullNote = "pull failed: " + userError("pull", err)
			repos[i].PullOutcome = pullFailed
			repos[i].Errs = append(repos[i].Errs, userError("pull", err))
			continue
		}

		if st.Ahead == 0 {
			writeStatus("Pulling", st.Path)
			err = runPullContext(st.Path, auth)
			if err == git.ErrNonFastForwardUpdate {
				repos[i].PullNote = fmt.Sprintf("not pulled: non-fast-forward — %d commit(s) on origin", st.Behind)
				repos[i].PullOutcome = pullSkipped
				continue
			}
			if err != nil {
				repos[i].PullNote = "pull failed: " + userError("pull", err)
				repos[i].PullOutcome = pullFailed
				repos[i].Errs = append(repos[i].Errs, userError("pull", err))
				continue
			}
			if st.Behind == 1 {
				repos[i].PullNote = "pulled 1 commit (fast-forward)"
			} else {
				repos[i].PullNote = fmt.Sprintf("pulled %d commits (fast-forward)", st.Behind)
			}
			repos[i].PullOutcome = pullDone
			refreshRepoStatus(i)
			continue
		}

		if !mergeAndPush {
			repos[i].PullNote = fmt.Sprintf("not pulled: %d local and %d remote commit(s) — use --pull --merge", st.Ahead, st.Behind)
			repos[i].PullOutcome = pullSkipped
			continue
		}

		writeStatus("Merging", st.Path)
		conflict, err := mergeHasConflicts(st.Path, st.Branch)
		if err != nil {
			repos[i].PullNote = "merge failed: " + userError("merge", err)
			repos[i].PullOutcome = pullFailed
			repos[i].Errs = append(repos[i].Errs, userError("merge", err))
			continue
		}
		if conflict {
			repos[i].PullNote = fmt.Sprintf("not merged: local and remote changes conflict — %d ahead, %d behind", st.Ahead, st.Behind)
			repos[i].PullOutcome = pullSkipped
			continue
		}

		if err := performCleanMerge(st.Path, st.Branch); err != nil {
			repos[i].PullNote = "merge failed: " + userError("merge", err)
			repos[i].PullOutcome = pullFailed
			repos[i].Errs = append(repos[i].Errs, userError("merge", err))
			continue
		}
		repos[i].PullNote = fmt.Sprintf("merged origin with local (%d remote, %d local commits)", st.Behind, st.Ahead)
		repos[i].PullOutcome = pullDone
		refreshRepoStatus(i)

		writeStatus("Pushing", st.Path)
		_, _, auth, err = openRepoOrigin(st.Path)
		if err != nil {
			repos[i].PushNote = "push failed: " + userError("push", err)
			repos[i].PushOutcome = pushFailed
			repos[i].Errs = append(repos[i].Errs, userError("push", err))
			continue
		}
		if err := runPush(st.Path, repos[i].Branch, auth); err != nil {
			repos[i].PushNote = "push failed: " + userError("push", err)
			repos[i].PushOutcome = pushFailed
			repos[i].Errs = append(repos[i].Errs, userError("push", err))
			continue
		}
		repos[i].PushNote = "pushed merged commits to origin"
		repos[i].PushOutcome = pushDone
		refreshRepoStatus(i)
	}
}

func applyPushAll() {
	for i := range repos {
		st := repos[i]
		if st.Ahead == 0 {
			continue
		}
		if reason := st.syncBlockReason(); reason != "" {
			repos[i].PushNote = fmt.Sprintf("not pushed: %s", reason)
			repos[i].PushOutcome = pushSkipped
			continue
		}
		if st.Behind > 0 {
			repos[i].PushNote = fmt.Sprintf("not pushed: %d commit(s) on origin — pull first", st.Behind)
			repos[i].PushOutcome = pushSkipped
			continue
		}

		_, _, auth, err := openRepoOrigin(st.Path)
		if err != nil {
			repos[i].PushNote = "push failed: " + userError("push", err)
			repos[i].PushOutcome = pushFailed
			repos[i].Errs = append(repos[i].Errs, userError("push", err))
			continue
		}

		writeStatus("Pushing", st.Path)
		if err := runPush(st.Path, st.Branch, auth); err != nil {
			repos[i].PushNote = "push failed: " + userError("push", err)
			repos[i].PushOutcome = pushFailed
			repos[i].Errs = append(repos[i].Errs, userError("push", err))
			continue
		}
		if st.Ahead == 1 {
			repos[i].PushNote = "pushed 1 commit to origin"
		} else {
			repos[i].PushNote = fmt.Sprintf("pushed %d commits to origin", st.Ahead)
		}
		repos[i].PushOutcome = pushDone
		refreshRepoStatus(i)
	}
}

func changePath(ch *object.Change) string {
	var empty object.ChangeEntry
	if ch.From != empty {
		return ch.From.Name
	}
	return ch.To.Name
}

func diffPaths(from, to *object.Tree) (map[string]plumbing.Hash, error) {
	out := map[string]plumbing.Hash{}
	if from == nil || to == nil {
		return out, nil
	}
	changes, err := object.DiffTreeWithOptions(context.Background(), from, to, &object.DiffTreeOptions{DetectRenames: false})
	if err != nil {
		return nil, err
	}
	var empty object.ChangeEntry
	for _, ch := range changes {
		p := changePath(ch)
		if ch.To != empty {
			out[p] = ch.To.TreeEntry.Hash
		} else {
			out[p] = plumbing.ZeroHash
		}
	}
	return out, nil
}

func mergeHasConflicts(repoPath, branch string) (bool, error) {
	r, err := git.PlainOpen(repoPath)
	if err != nil {
		return false, err
	}
	head, err := r.Head()
	if err != nil {
		return false, err
	}
	remoteRef, err := r.Reference(plumbing.NewRemoteReferenceName("origin", branch), true)
	if err != nil {
		return false, err
	}
	headCommit, err := r.CommitObject(head.Hash())
	if err != nil {
		return false, err
	}
	remoteCommit, err := r.CommitObject(remoteRef.Hash())
	if err != nil {
		return false, err
	}
	bases, err := headCommit.MergeBase(remoteCommit)
	if err != nil || len(bases) == 0 {
		return true, err
	}
	baseTree, err := bases[0].Tree()
	if err != nil {
		return false, err
	}
	headTree, err := headCommit.Tree()
	if err != nil {
		return false, err
	}
	remoteTree, err := remoteCommit.Tree()
	if err != nil {
		return false, err
	}
	local, err := diffPaths(baseTree, headTree)
	if err != nil {
		return false, err
	}
	remote, err := diffPaths(baseTree, remoteTree)
	if err != nil {
		return false, err
	}
	for p, lh := range local {
		rh, ok := remote[p]
		if ok && lh != rh {
			return true, nil
		}
	}
	return false, nil
}

func performCleanMerge(repoPath, branch string) error {
	r, err := git.PlainOpen(repoPath)
	if err != nil {
		return err
	}
	head, err := r.Head()
	if err != nil {
		return err
	}
	remoteRef, err := r.Reference(plumbing.NewRemoteReferenceName("origin", branch), true)
	if err != nil {
		return err
	}
	headCommit, err := r.CommitObject(head.Hash())
	if err != nil {
		return err
	}
	remoteCommit, err := r.CommitObject(remoteRef.Hash())
	if err != nil {
		return err
	}
	bases, err := headCommit.MergeBase(remoteCommit)
	if err != nil || len(bases) == 0 {
		return fmt.Errorf("no merge base")
	}
	baseTree, _ := bases[0].Tree()
	headTree, _ := headCommit.Tree()
	remoteTree, _ := remoteCommit.Tree()

	local, err := diffPaths(baseTree, headTree)
	if err != nil {
		return err
	}
	remote, err := diffPaths(baseTree, remoteTree)
	if err != nil {
		return err
	}

	flat := map[string]object.TreeEntry{}
	if err := flattenTree(headTree, "", flat); err != nil {
		return err
	}
	for p, rh := range remote {
		if _, touched := local[p]; touched {
			continue
		}
		if rh == plumbing.ZeroHash {
			delete(flat, p)
			continue
		}
		ent, err := treeEntryByPath(remoteTree, p)
		if err != nil {
			return err
		}
		flat[p] = *ent
	}

	treeHash, err := hashFromFlatTree(r, flat)
	if err != nil {
		return err
	}

	commit := &object.Commit{
		TreeHash:     treeHash,
		ParentHashes: []plumbing.Hash{head.Hash(), remoteRef.Hash()},
		Author:       headCommit.Author,
		Committer:    headCommit.Committer,
		Message:      fmt.Sprintf("Merge remote-tracking branch 'origin/%s'", branch),
	}
	obj := r.Storer.NewEncodedObject()
	if err := commit.Encode(obj); err != nil {
		return err
	}
	commitHash, err := r.Storer.SetEncodedObject(obj)
	if err != nil {
		return err
	}
	if err := r.Storer.SetReference(plumbing.NewHashReference(head.Name(), commitHash)); err != nil {
		return err
	}
	w, err := r.Worktree()
	if err != nil {
		return err
	}
	return w.Reset(&git.ResetOptions{Mode: git.MergeReset, Commit: commitHash})
}

func flattenTree(t *object.Tree, prefix string, out map[string]object.TreeEntry) error {
	for _, e := range t.Entries {
		name := e.Name
		if prefix != "" {
			name = path.Join(prefix, e.Name)
		}
		if e.Mode == filemode.Dir {
			sub, err := t.Tree(e.Name)
			if err != nil {
				return err
			}
			if err := flattenTree(sub, name, out); err != nil {
				return err
			}
			continue
		}
		out[name] = e
	}
	return nil
}

func treeEntryByPath(root *object.Tree, filePath string) (*object.TreeEntry, error) {
	parts := strings.Split(filePath, "/")
	t := root
	for i, part := range parts {
		e, err := t.FindEntry(part)
		if err != nil {
			return nil, err
		}
		if i == len(parts)-1 {
			return e, nil
		}
		sub, err := t.Tree(part)
		if err != nil {
			return nil, err
		}
		t = sub
	}
	return nil, fmt.Errorf("invalid path %q", filePath)
}

func hashFromFlatTree(r *git.Repository, flat map[string]object.TreeEntry) (plumbing.Hash, error) {
	byDir := map[string]map[string]object.TreeEntry{}
	for p, ent := range flat {
		dir := path.Dir(p)
		if dir == "." {
			dir = ""
		}
		base := path.Base(p)
		if byDir[dir] == nil {
			byDir[dir] = map[string]object.TreeEntry{}
		}
		byDir[dir][base] = ent
	}

	var build func(dir string) (plumbing.Hash, error)
	build = func(dir string) (plumbing.Hash, error) {
		files := byDir[dir]
		var names []string
		for n := range files {
			names = append(names, n)
		}
		sort.Strings(names)
		var entries []object.TreeEntry
		for _, name := range names {
			ent := files[name]
			if ent.Mode == filemode.Dir {
				subDir := name
				if dir != "" {
					subDir = path.Join(dir, name)
				}
				h, err := build(subDir)
				if err != nil {
					return plumbing.ZeroHash, err
				}
				ent.Hash = h
			}
			entries = append(entries, ent)
		}
		t := &object.Tree{Entries: entries}
		obj := r.Storer.NewEncodedObject()
		if err := t.Encode(obj); err != nil {
			return plumbing.ZeroHash, err
		}
		return r.Storer.SetEncodedObject(obj)
	}
	return build("")
}
