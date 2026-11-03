package main

import (
	"fmt"
	"io"
	"strings"
)

// metric names for --count and --only (case-insensitive).
const (
	metricUncommitted = "uncommitted"
	metricLocal       = "local"
	metricRemote      = "remote"
	metricAhead       = "ahead"
	metricConflicts   = "conflicts"
	metricDiverged    = "diverged"
	metricErrors      = "errors"
	metricAttention   = "attention"
	metricRepos       = "repos"
)

// Listed in --count / --only flag help and unknown-metric errors.
const metricNamesListed = "uncommitted, local, remote, ahead, conflicts, diverged, errors, attention, repos"

func normalizeMetric(name string) (string, error) {
	if name == "" {
		return "", nil
	}
	m := strings.ToLower(strings.TrimSpace(name))
	switch m {
	case metricUncommitted, metricLocal, metricRemote, metricAhead,
		metricConflicts, metricDiverged, metricErrors, metricAttention, metricRepos:
		return m, nil
	default:
		return "", fmt.Errorf("unknown metric %q (use: %s)", name, metricNamesListed)
	}
}

func repoMatchesMetric(r repoStatus, metric string) bool {
	switch metric {
	case metricUncommitted, metricLocal:
		return r.DirtyFiles > 0
	case metricRemote:
		return r.Behind > 0
	case metricAhead:
		return r.Ahead > 0
	case metricConflicts:
		return r.Conflicts > 0
	case metricDiverged:
		return r.diverged()
	case metricErrors:
		return len(r.Errs) > 0
	case metricAttention:
		return r.hasChanges()
	case metricRepos:
		return true
	default:
		return false
	}
}

func countMatching(repos []repoStatus, metric string) int {
	n := 0
	for _, r := range repos {
		if repoMatchesMetric(r, metric) {
			n++
		}
	}
	return n
}

func filterRepos(repos []repoStatus, metric string) []repoStatus {
	if metric == "" {
		return repos
	}
	var out []repoStatus
	for _, r := range repos {
		if repoMatchesMetric(r, metric) {
			out = append(out, r)
		}
	}
	return out
}

func reposForDisplay(repos []repoStatus, showAll bool, onlyMetric string, includePullNotes bool) []repoStatus {
	if onlyMetric != "" {
		return filterRepos(repos, onlyMetric)
	}
	if showAll {
		return repos
	}
	var out []repoStatus
	for _, r := range repos {
		if r.hasChanges() || (includePullNotes && (r.PullNote != "" || r.PushNote != "")) {
			out = append(out, r)
		}
	}
	return out
}

func printCountReport(w io.Writer, repos []repoStatus, metric string, quiet bool) {
	matches := matchingRepos(repos, metric)
	n := len(matches)

	if quiet {
		printCountReportQuiet(w, matches, metric, n)
		return
	}

	fmt.Fprintln(w, metricCountSummary(metric, n))
	if n == 0 {
		return
	}

	maxPath := maxPathLen(matches)
	valueLabel := metricValueLabel(metric)
	if valueLabel != "" {
		fmt.Fprintf(w, "  %-*s  %s\n", maxPath, "Repository", valueLabel)
	}

	for _, r := range matches {
		detail := metricCountDetail(r, metric)
		if detail == "" && metric == metricRepos {
			fmt.Fprintf(w, "  %s\n", r.Path)
			continue
		}
		fmt.Fprintf(w, "  %-*s  %s\n", maxPath, r.Path, detail)
	}
}

func matchingRepos(repos []repoStatus, metric string) []repoStatus {
	var matches []repoStatus
	for _, r := range repos {
		if repoMatchesMetric(r, metric) {
			matches = append(matches, r)
		}
	}
	return matches
}

func maxPathLen(matches []repoStatus) int {
	maxPath := 0
	for _, r := range matches {
		if len(r.Path) > maxPath {
			maxPath = len(r.Path)
		}
	}
	return maxPath
}

func printCountReportQuiet(w io.Writer, matches []repoStatus, metric string, n int) {
	if n == 0 {
		fmt.Fprintln(w, 0)
		return
	}
	if n == 1 {
		if metric == metricRepos {
			fmt.Fprintln(w, 1)
			return
		}
		fmt.Fprintln(w, metricCountDetail(matches[0], metric))
		return
	}

	maxPath := maxPathLen(matches)
	for _, r := range matches {
		detail := metricCountDetail(r, metric)
		if metric == metricRepos {
			fmt.Fprintln(w, r.Path)
			continue
		}
		fmt.Fprintf(w, "%-*s  %s\n", maxPath, r.Path, detail)
	}
}

func metricCountSummary(metric string, n int) string {
	repos := "repositories"
	if n == 1 {
		repos = "repository"
	}
	switch metric {
	case metricUncommitted, metricLocal:
		return fmt.Sprintf("%d %s with uncommitted changes (dirty files):", n, repos)
	case metricRemote:
		return fmt.Sprintf("%d %s behind origin (commits to pull):", n, repos)
	case metricAhead:
		return fmt.Sprintf("%d %s with unpushed commits:", n, repos)
	case metricConflicts:
		return fmt.Sprintf("%d %s with merge conflicts:", n, repos)
	case metricDiverged:
		return fmt.Sprintf("%d %s diverged from origin (ahead and behind):", n, repos)
	case metricErrors:
		return fmt.Sprintf("%d %s with errors:", n, repos)
	case metricAttention:
		return fmt.Sprintf("%d %s needing attention:", n, repos)
	case metricRepos:
		return fmt.Sprintf("%d git %s found:", n, repos)
	default:
		return fmt.Sprintf("%d %s matching %q:", n, repos, metric)
	}
}

func metricValueLabel(metric string) string {
	switch metric {
	case metricUncommitted, metricLocal:
		return "Local"
	case metricRemote:
		return "Remote"
	case metricAhead:
		return "Ahead"
	case metricConflicts:
		return "Conflicts"
	case metricDiverged:
		return "Ahead / Behind"
	case metricErrors:
		return "Error"
	case metricAttention:
		return "Summary"
	case metricRepos:
		return ""
	default:
		return "Count"
	}
}

func metricCountDetail(r repoStatus, metric string) string {
	switch metric {
	case metricUncommitted, metricLocal:
		return fmt.Sprintf("%d", r.DirtyFiles)
	case metricRemote:
		return fmt.Sprintf("%d", r.Behind)
	case metricAhead:
		return fmt.Sprintf("%d", r.Ahead)
	case metricConflicts:
		return fmt.Sprintf("%d", r.Conflicts)
	case metricDiverged:
		return fmt.Sprintf("%d / %d", r.Ahead, r.Behind)
	case metricErrors:
		if len(r.Errs) > 0 {
			return r.Errs[0]
		}
		return ""
	case metricAttention:
		var parts []string
		if r.DirtyFiles > 0 {
			parts = append(parts, fmt.Sprintf("local %d", r.DirtyFiles))
		}
		if r.Behind > 0 {
			parts = append(parts, fmt.Sprintf("remote %d", r.Behind))
		}
		if r.Ahead > 0 {
			parts = append(parts, fmt.Sprintf("ahead %d", r.Ahead))
		}
		if r.Conflicts > 0 {
			parts = append(parts, fmt.Sprintf("conflicts %d", r.Conflicts))
		}
		if len(r.Errs) > 0 {
			parts = append(parts, r.Errs[0])
		}
		return strings.Join(parts, ", ")
	case metricRepos:
		return ""
	default:
		return ""
	}
}
