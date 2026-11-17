package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestCollectGitRepoPathsNestedUnderRepoRoot(t *testing.T) {
	root := t.TempDir()
	parent := filepath.Join(root, "parent")
	child := filepath.Join(parent, "child")
	for _, p := range []string{parent, child} {
		if err := os.MkdirAll(p, 0o755); err != nil {
			t.Fatal(err)
		}
		runGit(t, p, "init")
	}

	paths, err := collectGitRepoPaths(parent, 0, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) != 2 {
		t.Fatalf("want 2 repos (parent + child), got %d: %v", len(paths), paths)
	}
}

func TestCollectGitRepoPathsHereOnlySkipsChildren(t *testing.T) {
	root := t.TempDir()
	parent := filepath.Join(root, "parent")
	child := filepath.Join(parent, "child")
	for _, p := range []string{parent, child} {
		if err := os.MkdirAll(p, 0o755); err != nil {
			t.Fatal(err)
		}
		runGit(t, p, "init")
	}

	paths, err := collectGitRepoPaths(parent, 0, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) != 1 {
		t.Fatalf("want 1 repo with --here semantics, got %d", len(paths))
	}
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v in %s: %v\n%s", args, dir, err, out)
	}
}