# herdr-reflow

`herdr-reflow` orders the workspaces in the running Herdr session. It is the `lewkit herdr reorder` command as its own program.

```
herdr-reflow [REPO:BRANCH...]
```

Each `REPO:BRANCH` ensures a main workspace and a linked worktree at `~/.grok/worktrees/<slug>/<branch>`. A slash in the branch is a hyphen in the directory name. `%d` in the branch is the local date as `yyyymmdd`, so `.:%d-teste` is `.:20260925-teste` on that day. The repo side stays literal. The dotfiles root sorts first. The program prints the final order as a tree. Each worktree sits under its main checkout.

Global flags are `-h`, `-v`, `--version`, and `--pprof`.

Build from a clone:

```
go build -o herdr-reflow ./cmd/herdr-reflow
```

`mise release patch` tags the next patch version and runs GoReleaser. On GitHub, run the Autorelease workflow and choose patch, minor, or major.
