package main

import (
	"encoding/json"
	"io"
)

type repoJSON struct {
	Path              string   `json:"path"`
	RepoName          string   `json:"repo_name"`
	Branch            string   `json:"branch,omitempty"`
	Local             int      `json:"local"`
	Remote            int      `json:"remote"`
	Ahead             int      `json:"ahead"`
	Uncommitted       bool     `json:"uncommitted"`
	Conflicts         int      `json:"conflicts"`
	Diverged          bool     `json:"diverged"`
	Errors            []string `json:"errors,omitempty"`
	LastCommit        string   `json:"last_commit,omitempty"`
	LastPush          string   `json:"last_push,omitempty"`
	LastReleaseTag    string   `json:"last_release_tag,omitempty"`
	LastRelease       string   `json:"last_release,omitempty"`
	SinceRelease      *int     `json:"since_release,omitempty"`
	SinceReleaseFiles int      `json:"since_release_files,omitempty"`
	PullNote          string   `json:"pull_note,omitempty"`
	PushNote          string   `json:"push_note,omitempty"`
}

func repoToJSON(r repoStatus) repoJSON {
	out := repoJSON{
		Path:        r.Path,
		RepoName:    repoDisplayName(r),
		Branch:      r.Branch,
		Local:       r.DirtyFiles,
		Remote:      r.Behind,
		Ahead:       r.Ahead,
		Uncommitted: r.uncommitted(),
		Conflicts:   r.Conflicts,
		Diverged:    r.diverged(),
	}
	if len(r.Errs) > 0 {
		out.Errors = append([]string(nil), r.Errs...)
	}
	if !r.LastCommit.IsZero() {
		out.LastCommit = formatReportTime(r.LastCommit)
	}
	if r.HasPush && !r.LastPush.IsZero() {
		out.LastPush = formatReportTime(r.LastPush)
	}
	if r.LastReleaseTag != "" {
		out.LastReleaseTag = r.LastReleaseTag
	}
	if r.HasLastRelease && !r.LastRelease.IsZero() {
		out.LastRelease = formatReportTime(r.LastRelease)
	}
	if r.SinceRelease >= 0 {
		n := r.SinceRelease
		out.SinceRelease = &n
		out.SinceReleaseFiles = r.SinceReleaseFiles
	}
	out.PullNote = r.PullNote
	out.PushNote = r.PushNote
	return out
}

func printJSONReport(w io.Writer, rows []repoStatus) {
	enc := json.NewEncoder(w)
	for _, r := range rows {
		_ = enc.Encode(repoToJSON(r))
	}
}
