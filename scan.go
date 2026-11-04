package main

import (
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"sync"

	git "github.com/go-git/go-git/v5"
)

const maxParallelJobs = 16

// collectGitRepoPaths walks the tree and returns paths that are git repositories.
func collectGitRepoPaths(path string, depth int) ([]string, error) {
	if depth > maxDepth {
		return nil, nil
	}

	_, err := git.PlainOpen(path)
	if err == nil {
		return []string{path}, nil
	}

	files, err := os.ReadDir(path)
	if err != nil {
		if depth == 0 {
			return nil, err
		}
		return nil, nil
	}

	var paths []string
	for _, f := range files {
		if f.Name() == ".git" {
			continue
		}
		if f.Type().IsDir() && f.Type()&os.ModeSymlink != os.ModeSymlink {
			sub, err := collectGitRepoPaths(filepath.Join(path, f.Name()), depth+1)
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
			results[i] = checkRepository(path, r)
		}(i, path)
	}

	wg.Wait()
	clearScanProgress()
	return results
}
