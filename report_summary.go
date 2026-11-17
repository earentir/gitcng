package main

import (
	"fmt"
	"io"
	"strings"
)

func printSyncSummary(w io.Writer, scanRoot string, repos, rows []repoStatus, didPull, didPush, showAll bool) {
	if !didPull && !didPush {
		return
	}
	n := len(repos)
	fmt.Fprintf(w, "Scanned %d repositories (root %s).\n", n, formatListPath(scanRoot, scanRoot))

	if didPull {
		printPullSummaryLines(w, scanRoot, repos)
	}
	if didPush {
		printPushSummaryLines(w, scanRoot, repos)
	}

	if !showAll {
		hidden := n - len(rows)
		if hidden > 0 {
			fmt.Fprintf(w, "%d repos hidden (use --all to list every repo).\n", hidden)
		}
	}
	if didPull || didPush {
		fmt.Fprintf(w, "Details below: repos where %s.\n\n", syncDetailsBlurb(didPull, didPush))
		return
	}
	fmt.Fprintf(w, "Details below: repos that need attention (dirty, behind, ahead, conflicts, or errors).\n\n")
}

func syncDetailsBlurb(didPull, didPush bool) string {
	if didPull && didPush {
		return "pull or push ran, was skipped, or failed"
	}
	if didPull {
		return "pull ran, was skipped, or failed"
	}
	return "push ran, was skipped, or failed"
}

func printPullSummaryLines(w io.Writer, scanRoot string, repos []repoStatus) {
	var pulled, skipped, failed []string
	for _, r := range repos {
		name := repoLabelForSummary(scanRoot, r)
		switch r.PullOutcome {
		case pullDone:
			pulled = append(pulled, summaryPullDone(name, r.PullNote))
		case pullSkipped:
			skipped = append(skipped, summaryPullSkipped(name, r.PullNote))
		case pullFailed:
			failed = append(failed, summaryPullFailed(name, r.PullNote))
		}
	}
	if len(pulled) > 0 {
		fmt.Fprintf(w, "Pull OK: %s\n", strings.Join(pulled, "; "))
	}
	if len(skipped) > 0 {
		fmt.Fprintf(w, "Pull skipped: %s\n", strings.Join(skipped, "; "))
	}
	if len(failed) > 0 {
		fmt.Fprintf(w, "Pull failed: %s\n", strings.Join(failed, "; "))
	}
}

func printPushSummaryLines(w io.Writer, scanRoot string, repos []repoStatus) {
	var pushed, skipped, failed []string
	for _, r := range repos {
		name := repoLabelForSummary(scanRoot, r)
		switch r.PushOutcome {
		case pushDone:
			pushed = append(pushed, summaryNote(name, r.PushNote))
		case pushSkipped:
			skipped = append(skipped, summaryNote(name, r.PushNote))
		case pushFailed:
			failed = append(failed, summaryNote(name, r.PushNote))
		}
	}
	if len(pushed) > 0 {
		fmt.Fprintf(w, "Push OK: %s\n", strings.Join(pushed, "; "))
	}
	if len(skipped) > 0 {
		fmt.Fprintf(w, "Push skipped: %s\n", strings.Join(skipped, "; "))
	}
	if len(failed) > 0 {
		fmt.Fprintf(w, "Push failed: %s\n", strings.Join(failed, "; "))
	}
}

func repoLabelForSummary(scanRoot string, r repoStatus) string {
	if r.RepoName != "" {
		return r.RepoName
	}
	return formatListPath(scanRoot, r.Path)
}

func summaryPullDone(name, note string) string {
	if note == "" {
		return name
	}
	return fmt.Sprintf("%s (%s)", name, strings.TrimPrefix(note, "pulled "))
}

func summaryPullSkipped(name, note string) string {
	reason := strings.TrimSpace(strings.TrimPrefix(note, "not pulled: "))
	if reason == "" {
		return name
	}
	return fmt.Sprintf("%s — %s", name, reason)
}

func summaryPullFailed(name, note string) string {
	reason := strings.TrimPrefix(note, "pull failed: ")
	if reason == note {
		reason = note
	}
	return fmt.Sprintf("%s — %s", name, reason)
}

func summaryNote(name, note string) string {
	if note == "" {
		return name
	}
	return fmt.Sprintf("%s (%s)", name, note)
}