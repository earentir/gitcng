package main

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"sync"

	git "github.com/go-git/go-git/v5"
)

const (
	maxParallelJobs    = 16
	defaultScanDepth   = 4
)

func effectiveScanDepth() int {
	if maxDepth > 0 {
		return maxDepth
	}
	return defaultScanDepth
}

func canonicalRepoPath(path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		return filepath.Clean(path)
	}
	return filepath.Clean(abs)
}

// formatListPath prints a repo path relative to the scan root (e.g. "dso", not "/home/.../web/dso").
func formatListPath(scanRoot, repoPath string) string {
	scanRoot = canonicalRepoPath(scanRoot)
	repoPath = canonicalRepoPath(repoPath)
	rel, err := filepath.Rel(scanRoot, repoPath)
	if err != nil {
		return filepath.Base(repoPath)
	}
	if rel == "." {
		return "."
	}
	return rel
}

// collectGitRepoPaths walks the tree and returns paths that are git repositories.
// The root may itself be a repo; nested repos under it are still discovered (unless --here).
func collectGitRepoPaths(path string, depth int, hereOnly bool) ([]string, error) {
	if hereOnly {
		if depth > effectiveScanDepth() {
			return nil, nil
		}
		_, err := git.PlainOpen(path)
		if err == nil {
			return []string{canonicalRepoPath(path)}, nil
		}
		return nil, nil
	}

	if depth > effectiveScanDepth() {
		return nil, nil
	}

	var paths []string
	if _, err := git.PlainOpen(path); err == nil {
		paths = append(paths, canonicalRepoPath(path))
	}

	files, err := os.ReadDir(path)
	if err != nil {
		if len(paths) > 0 {
			return paths, nil
		}
		if depth == 0 {
			return nil, err
		}
		return nil, nil
	}

	for _, f := range files {
		if f.Name() == ".git" {
			continue
		}
		if f.Type().IsDir() && f.Type()&os.ModeSymlink != os.ModeSymlink {
			sub, err := collectGitRepoPaths(filepath.Join(path, f.Name()), depth+1, false)
			if err != nil {
				return nil, err
			}
			paths = append(paths, sub...)
		}
	}
	return paths, nil
}

func parallelWorkerCount(repoCount int) int {
	if repoCount < 1 {
		return 1
	}
	if parallelJobs > 0 {
		if parallelJobs > repoCount {
			return repoCount
		}
		return parallelJobs
	}
	n := runtime.GOMAXPROCS(0)
	if repoCount < n {
		n = repoCount
	}
	if n > maxParallelJobs {
		n = maxParallelJobs
	}
	if n < 1 {
		n = 1
	}
	return n
}

func checkRepositoriesParallel(paths []string) []repoStatus {
	if len(paths) == 0 {
		return nil
	}

	sort.Strings(paths)
	setScanProgress(len(paths))

	jobs := parallelWorkerCount(len(paths))
	sem := make(chan struct{}, jobs)
	results := make([]repoStatus, len(paths))
	var wg sync.WaitGroup

	for i, path := range paths {
		wg.Add(1)
		go func(i int, path string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			markRepoCheckStarted()
			writeStatus("Checking", path)

			r, err := git.PlainOpen(path)
			if err != nil {
				results[i] = repoStatus{
					Path: path,
					Errs: []string{userError("open", err)},
				}
				return
			}
			results[i] = checkRepository(path, r, false)
		}(i, path)
	}

	wg.Wait()
	clearScanProgress()
	return results
}

func runListRepos(root string) int {
	paths, err := collectGitRepoPaths(root, 0, hereOnly)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 2
	}
	scanRoot := canonicalRepoPath(root)
	sort.Strings(paths)
	for _, p := range paths {
		fmt.Println(formatListPath(scanRoot, p))
	}
	return 0
}

// forEachRepoParallel runs fn for each repo index with the same worker pool as scan (--jobs).
func forEachRepoParallel(indices []int, fn func(i int)) {
	if len(indices) == 0 {
		return
	}
	jobs := parallelWorkerCount(len(indices))
	sem := make(chan struct{}, jobs)
	var wg sync.WaitGroup
	for _, i := range indices {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			fn(i)
		}(i)
	}
	wg.Wait()
}
