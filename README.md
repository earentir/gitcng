# gitcng

Scan a directory tree for git repositories, fetch `origin` (with timeouts), and print status.

## Usage

```bash
gitcng [path] [flags]
```

`path` defaults to `.`. Flags can go before or after the path.

```bash
gitcng ..
gitcng --compact --depth 2 ../web
```

While scanning, one progress line is rewritten on stderr (unless `-q` / `--quiet`).

## Report (default)

Only repositories that need attention are shown (dirty files, behind/ahead of origin, merge conflicts, or errors). Use `--all` to include clean repos.

Each repo uses **two lines**, with a blank line between repos:

- **Line 1:** path, `Local` (dirty file count), `Remote` (commits behind origin), `Uncommitted`, `Conflicts`, `Diverged`, optional `Ahead`, optional error text.
- **Line 2:** `Commit` and `Push` times, indented to line up under the **L** in `Local`.

Only the **numeric values** (and `yes` / errors) are colourised when `--colour auto` (default). Labels stay plain.

`--compact` puts everything on one line again (no extra spacing).

## Script helpers

These were added so shell scripts can branch on repo state without parsing the table.

### `--count METRIC`

Prints how many repositories match, then **lists each one** with the relevant number (Remote = commits behind origin, Local = dirty files, etc.).

**`-q` / `--quiet`** (with `--count` only): no summary or column headers. If exactly one repo matches, print only its value (e.g. `16`). If several match, one line per repo: `path` and value. If none match, `0`.

```bash
gitcng --count remote ../web
# 3 repositories behind origin (commits to pull):
#   Repository  Remote
#   pkg/authz       16
#   ...

gitcng -q --count remote ../web
# pkg/auth        1
# pkg/authz      16
# pkg/productauthgate  12

gitcng -q --count remote ../single-repo
# 9
```

### `--only METRIC`

Same human report as usual, but **only** repos matching `METRIC`.

### `--list`

Print matching **paths**, one per line (respects `--only`; default filter unless `--all`).

### Metrics

| METRIC | Matches |
|--------|---------|
| `uncommitted`, `local` | dirty working tree / index |
| `remote` | commits on origin not in HEAD |
| `ahead` | local commits not on origin |
| `conflicts` | unmerged paths (`U` in status) |
| `diverged` | both ahead and behind |
| `errors` | fetch, auth, missing origin ref, etc. |
| `attention` | any of the above |
| `repos` | every git repo found |

`--count` and `--only` cannot be used together.

## Other flags

| Flag | Purpose |
|------|---------|
| `--depth N` | Max directory depth (default 4) |
| `--all` | Include repos with nothing pending |
| `--compact` | Single-line rows |
| `--colour auto\|always\|never` | ANSI highlighting (`NO_COLOR` / `NO_COLOUR`, `FORCE_COLOR` / `FORCE_COLOUR`) |
| `--fetch-timeout N` | Per-fetch / SSH dial cap in seconds (default 5) |
| `-q`, `--quiet` | No stderr progress |

## Exit codes

| Code | Meaning |
|------|---------|
| 0 | Scan finished (dirty repos are OK) |
| 1 | At least one repo recorded an error |
| 2 | Bad flags or root path could not be scanned |

## Build

```bash
go build -o gitcng .
./gitcng ../web    # run the binary you built, not an old copy on PATH
```

## Shell tab completion

Completion is built in but must be loaded once per shell (bash example):

```bash
source <(./gitcng completion bash)
```

To install permanently:

```bash
./gitcng completion bash > ~/.local/share/bash-completion/completions/gitcng
# or append: echo 'source <(gitcng completion bash)' >> ~/.bashrc  (use full path to gitcng)
```

After that, tab completes `--count` / `--only` metrics and directory paths (e.g. `gitcng --count remote ../plan<Tab>`).

Zsh: `gitcng completion zsh > "${fpath[1]}/_gitcng"` then `compinit`.

## Example

```
../logfacility | Local:  2 | Remote:  0 | Uncommitted: yes | Conflicts:  0 | Diverged:  no
               Commit: 2026-10-12 18:43 | Push: 2026-10-12 18:43

../pkg/auth    | Local:  8 | Remote:  1 | Uncommitted: yes | Conflicts:  0 | Diverged:  no | Ahead: 2
               Commit: 2026-10-14 14:18 | Push: 2026-11-02 10:36
```
