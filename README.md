# gitcng

Scan a directory tree for git repositories, **fetch `origin`** (SSH via `ssh-agent`, with timeouts), and print a structured status report. Implemented in Go with [go-git](https://github.com/go-git/go-git) — no external `git` binary required.

Repositories are discovered under `[path]` (default `.`), checked **in parallel**, then optional **pull / push / merge** runs sequentially on repos that need it.

## Usage

```bash
gitcng [path] [flags]
```

`path` defaults to `.`. Flags can go before or after the path.

```bash
gitcng ../web
gitcng --all --jobs 8 ../web
gitcng --release-diff --all .
```

While scanning, stderr shows a single rewritten progress line (unless `-q` / `--quiet`), e.g. `[3/13] Fetching pkg-auth`.

## Default report

By default, only repos that **need attention** are listed:

- Uncommitted changes, ahead/behind origin, merge conflicts, fetch/auth errors
- **Commits after the last release tag** on `origin/<branch>` (`SinceRelease > 0`)

Use **`--all`** to include fully clean repos as well.

### Layout (multi-line)

Each repo is a block separated by a blank line. The **name on line 0** is the **origin project name** (e.g. `pkg-auth` from `…/pkg-auth.git`), not necessarily the local directory. **`--list` / `--count`** still use filesystem paths for scripting.

| Line | Content |
|------|---------|
| **0** | Name, `Local` (dirty file count), `Remote` (commits behind origin), `Uncommitted`, optional first error |
| **1** | Release **tag** under the name (one-space inset), then `Conflicts`, `Diverged`, `Ahead` |
| **2** | `Commit`, `Push`, `Last Release` (date of the tag), aligned under **L** in `Local` |
| **3** | `Since last release:` commit count (and file count when &gt; 0 commits) |
| **+** | With **`--release-diff`**: one line per commit, then changed file paths |
| **Last** | Pull note, then push note (after **`--pull`** / **`--push`**) |

With **`--colour auto`** (default), only **values** and **`yes`** / errors are highlighted; labels stay plain. British spelling **`--colour`** is supported; `NO_COLOR` / `NO_COLOUR` and `FORCE_COLOR` / `FORCE_COLOUR` apply as usual.

**`--compact`** collapses the block into one line (same fields, no extra spacing).

### Release tags

For each repo, gitcng compares **`origin/<current-branch>`** to a **release tag**:

1. Highest **semver** tag (`v1.2.3` or `1.2.3`) if any exist  
2. Otherwise the **newest tag by date** (annotated tagger time, else tagged commit time)

Line 1 shows the tag name; line 2 shows **Last Release** date; line 3 counts commits and changed files since that tag. Tags must be an **ancestor** of the remote branch tip; otherwise a release error is recorded.

## Sync: pull, push, merge

| Flag | Behaviour |
|------|-----------|
| **`--pull`** | Fast-forward **pull** from `origin` when the working tree is clean and only remote commits are pending (`Remote > 0`, `Ahead == 0`) |
| **`--pull --merge`** | When both local and remote commits exist: if trees do not conflict, create a merge commit and **push** to `origin` (requires **`--pull`**) |
| **`--push`** | **Push** local commits when clean, not behind, and nothing left to pull |

Pull/push outcomes appear as extra lines at the bottom of each repo block. **`--pull` / `--push`** cannot be combined with **`--count`** or **`--list`**.

Sync uses the same SSH settings as fetch (agent, `known_hosts`, timeouts).

## Parallel scan and network

| Flag | Default | Purpose |
|------|---------|---------|
| **`--jobs N`** | `0` (auto) | Max repos checked at once (`0` → up to **16**, capped by CPU and repo count) |
| **`--fetch-timeout N`** | **10** s | Per-repo fetch / SSH dial budget (`0` → 10 s) |

Discovery is sequential; **open, status, fetch, compare, and release math** run in parallel workers.

SSH fetches (and pull/push) share a pool of **4 concurrent** connections, with **retries** on flaky `ssh-agent` handshakes — needed when many Gitea/GitHub repos are fetched at once.

## Script helpers

### `--count METRIC`

Print how many repos match, then a table of each match and its value (`Remote` = behind count, `Local` = dirty files, etc.).

With **`-q` / `--quiet`**: no header. One match → value only; several → `path` + value per line; none → `0`.

```bash
gitcng --count remote ../web
gitcng -q --count remote ../web
if [ "$(gitcng -q --count remote ..)" -gt 0 ]; then echo "need pull"; fi
```

### `--only METRIC`

Same human report as default, filtered to one metric.

### `--list`

Print matching **paths** (one per line). Respects **`--only`**; respects default attention filter unless **`--all`**.

### Metrics

| METRIC | Matches |
|--------|---------|
| `uncommitted`, `local` | Dirty working tree / index |
| `remote` | Commits on origin not in HEAD |
| `ahead` | Local commits not on origin |
| `conflicts` | Unmerged paths |
| `diverged` | Both ahead and behind |
| `errors` | Fetch, auth, missing `origin` ref, release errors, etc. |
| `attention` | Any of the above (including post-release commits) |
| `repos` | Every git repo found |

**`--count`** and **`--only`** cannot be used together.

## All flags

| Flag | Purpose |
|------|---------|
| `--depth N` | Max directory depth from scan root (default **4**) |
| `--all` | Include repos with nothing pending |
| `--compact` | Single-line rows |
| `--colour auto\|always\|never` | ANSI highlighting |
| `--fetch-timeout N` | Fetch / SSH timeout in seconds (default **10**) |
| `--jobs N` | Parallel repo checks (`0` = auto, max **16**) |
| `--release-diff` | List commits and files since latest release tag |
| `--pull` | Fast-forward pull when safe |
| `--merge` | With `--pull`: merge non-conflicting diverged branches, then push |
| `--push` | Push unpushed commits when safe |
| `-q`, `--quiet` | No stderr progress |

## Exit codes

| Code | Meaning |
|------|---------|
| 0 | Scan finished (dirty repos are OK) |
| 1 | At least one repo recorded an error |
| 2 | Bad flags or scan root could not be read |

## Build

```bash
go build -o gitcng .
./gitcng ../web    # run the binary you built, not an old copy on PATH
```

## Shell tab completion

Bash (once per shell):

```bash
source <(./gitcng completion bash)
```

Install:

```bash
./gitcng completion bash > ~/.local/share/bash-completion/completions/gitcng
```

Zsh: `gitcng completion zsh > "${fpath[1]}/_gitcng"` then `compinit`.

Tab completes metrics for **`--count`** / **`--only`**, **`--colour`**, and directory paths.

## Example (default layout)

With **`--colour auto`** (default on a TTY), gitcng applies **orange** to highlighted values and **red** to conflict counts and errors. Labels, zeros, **`no`**, and dates are plain.

The HTML sample below also uses **blue-grey** for repository and tag names and **muted grey** for timestamps so it matches a typical dark terminal screenshot (those name/date colours come from your theme, not extra ANSI codes from gitcng).

| Colour in sample | Meaning |
|------------------|---------|
| Orange | Non-zero counts, **`yes`**, post-release commit/file counts (from gitcng) |
| Red | Merge **Conflicts** counts, fetch/auth errors (from gitcng) |
| Blue-grey | Repository name and release tag (terminal accent in the sample) |
| Muted grey | `Commit` / `Push` / `Last Release` timestamps (plain text in the sample) |
| Default | Labels, **`no`**, zeros |

<!-- markdownlint-disable MD033 -->
<pre style="background:#0d1117;color:#c9d1d9;padding:12px;border-radius:6px;font-family:ui-monospace,monospace;font-size:13px;line-height:1.45;overflow-x:auto">
<span style="color:#79c0ff">pkg-productauthgate</span>   Local: 0 | Remote: 0 | Uncommitted:  no
                          Conflicts: 0 | Diverged:  no | Ahead: <span style="color:#ffa657">1</span>
                          Commit: <span style="color:#8b949e">2026-11-03 18:49</span> | Push: <span style="color:#8b949e">2026-11-02 10:50</span>

<span style="color:#79c0ff">planning</span>                Local: <span style="color:#ffa657">14</span> | Remote: 0 | Uncommitted: <span style="color:#ffa657">yes</span>
                          Conflicts: 0 | Diverged:  no | Ahead: 0
                          Commit: <span style="color:#8b949e">2026-11-02 17:15</span> | Push: <span style="color:#8b949e">2026-11-02 17:15</span>

<span style="color:#79c0ff">pso</span>                     Local: 0 | Remote: 0 | Uncommitted:  no
                          Conflicts: 0 | Diverged:  no | Ahead: <span style="color:#ffa657">3</span>
                          Commit: <span style="color:#8b949e">2026-11-03 18:49</span> | Push: <span style="color:#8b949e">2026-11-02 17:16</span>

<span style="color:#79c0ff">sqlrunner</span>               Local: 0 | Remote: 0 | Uncommitted:  no
 <span style="color:#79c0ff">v0.1.43</span>                Conflicts: 0 | Diverged:  no | Ahead: 0
                          Commit: <span style="color:#8b949e">2026-10-21 16:37</span> | Push: <span style="color:#8b949e">2026-10-21 16:37</span> | Last Release: <span style="color:#8b949e">2026-10-21 16:37</span>
                          Since last release: <span style="color:#ffa657">55</span> commits, <span style="color:#ffa657">71</span> files

<span style="color:#79c0ff">vfikvs</span>                  Local: <span style="color:#ffa657">4</span> | Remote: 0 | Uncommitted: <span style="color:#ffa657">yes</span>
 <span style="color:#79c0ff">v0.5.103</span>               Conflicts: 0 | Diverged:  no | Ahead: 0
                          Commit: <span style="color:#8b949e">2026-10-27 16:06</span> | Push: <span style="color:#8b949e">2026-10-27 16:06</span> | Last Release: <span style="color:#8b949e">2026-10-27 16:06</span>
                          Since last release: <span style="color:#ffa657">2</span> commits, <span style="color:#ffa657">6</span> files
</pre>
<!-- markdownlint-enable MD033 -->

Plain-text copy (no colour):

```
pkg-productauthgate       Local: 0 | Remote: 0 | Uncommitted:  no
                          Conflicts: 0 | Diverged:  no | Ahead: 1
                          Commit: 2026-11-03 18:49 | Push: 2026-11-02 10:50

planning                  Local: 14 | Remote: 0 | Uncommitted: yes
                          Conflicts: 0 | Diverged:  no | Ahead: 0
                          Commit: 2026-11-02 17:15 | Push: 2026-11-02 17:15

pso                       Local: 0 | Remote: 0 | Uncommitted:  no
                          Conflicts: 0 | Diverged:  no | Ahead: 3
                          Commit: 2026-11-03 18:49 | Push: 2026-11-02 17:16

sqlrunner                 Local: 0 | Remote: 0 | Uncommitted:  no
 v0.1.43                  Conflicts: 0 | Diverged:  no | Ahead: 0
                          Commit: 2026-10-21 16:37 | Push: 2026-10-21 16:37 | Last Release: 2026-10-21 16:37
                          Since last release: 55 commits, 71 files

vfikvs                    Local: 4 | Remote: 0 | Uncommitted: yes
 v0.5.103                 Conflicts: 0 | Diverged:  no | Ahead: 0
                          Commit: 2026-10-27 16:06 | Push: 2026-10-27 16:06 | Last Release: 2026-10-27 16:06
                          Since last release: 2 commits, 6 files
```

