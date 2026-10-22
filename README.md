# gitcng

Scan a directory tree for git repositories and print a summary of local changes, unpushed commits, commits to pull, and errors.

## Usage

```bash
gitcng [path] [--depth N] [--all]
```

While scanning, the current path is rewritten on a single stderr line. The final report goes to stdout and lists only repositories with local file changes, commits to pull, commits to push, or errors. `--all` includes repositories with nothing pending.

Paths are padded to the widest path that still fits the terminal, and trimmed when they would wrap.

## Example

```
../gitcng   | Local: 9 | Remote: 0 | Uncommitted: yes | Commit: 2026-10-22 18:30 | Push: 2026-10-20 11:00
../updatego | Local: 0 | Remote: 9 | Uncommitted: no  | Commit: 2026-10-15 09:12 | Push: 2026-10-01 14:45
```

`Local` is dirty files. `Remote` is commits on `origin` that are not in HEAD. `Commit` is the local HEAD commit time. `Push` is the commit time of `origin` for the current branch. `Ahead` is appended when the branch has commits that are not on `origin`.

Exit codes: `0` after a completed scan, `1` if a repository hit an error, `2` if the root path cannot be scanned.
