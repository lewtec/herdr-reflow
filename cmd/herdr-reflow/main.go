package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"

	"github.com/lewtec/herdr-reflow/x/herdr"
	"github.com/lewtec/lewkit/x/cmd"
	"github.com/lewtec/lewkit/x/logging"
	"github.com/lewtec/lewkit/x/taskgroup"
	"github.com/lewtec/lewkit/x/taskgroup/progress"
	"github.com/lewtec/lewkit/x/thread"
)

func main() {
	slog.SetDefault(slog.New(logging.NewHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo})))
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	if err := thread.Run(ctx, run); err != nil {
		slog.Error(err.Error())
		os.Exit(1)
	}
}

type root struct {
	specs []herdr.RepoBranch `help:"REPO:BRANCH, for example .dotfiles:feat/teste"`
}

func (root) Description() string {
	return "nest worktrees, park feature branches, and order workspaces"
}

func (c *root) Run(ctx context.Context) error {
	session, ctx := taskgroup.New(ctx, taskgroup.DefaultLimits())
	var report herdr.Report
	err := progress.Run(session, ctx, func(ctx context.Context) error {
		taskgroup.Go(ctx, "reorder", taskgroup.Control, func(ctx context.Context, status *taskgroup.Status) error {
			var runErr error
			report, runErr = herdr.Reorder(ctx, herdr.Options{
				Specs:  c.specs,
				Status: status,
			})
			return runErr
		})
		return nil
	})
	if err != nil {
		return err
	}
	_, err = os.Stdout.WriteString(progress.Format(report.Nodes(), 0))
	return err
}

func run(ctx context.Context) error {
	app, err := cmd.Parse[cmd.App[root]](os.Args[1:]...)
	if err != nil {
		return err
	}
	return app.Run(ctx)
}
