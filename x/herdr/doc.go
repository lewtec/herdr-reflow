// Package herdr talks to a running Herdr session and lays out its workspaces.
//
// [Client] is the socket CLI: workspaces, tabs, panes, worktree open, and
// workspace.move_block. Checkouts come from x/git.
// [RepoBranch] is the REPO:BRANCH argument. [Reorder] nests linked worktrees
// under an open main, parks a feature branch off the main checkout, renames
// worktree workspaces to their branch, and orders the dotfiles root first.
// Steps are logged with slog. A taskgroup status carries the phase name.
package herdr
