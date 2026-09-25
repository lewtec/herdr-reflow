package main

import (
	"strings"
	"testing"

	"github.com/lewtec/lewkit/x/cmd"
)

func TestReorderArg(t *testing.T) {
	app, err := cmd.Parse[cmd.App[root]](".dotfiles:feat/teste")
	if err != nil {
		t.Fatal(err)
	}
	if len(app.Args.specs) != 1 {
		t.Fatalf("specs: got %d, want 1", len(app.Args.specs))
	}
	got := app.Args.specs[0]
	if got.Repo != ".dotfiles" || got.Branch != "feat/teste" {
		t.Fatalf("spec: repo %q branch %q", got.Repo, got.Branch)
	}
}

func TestReorderArgRejectsBareToken(t *testing.T) {
	_, err := cmd.Parse[cmd.App[root]]("feat/teste")
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestUsage(t *testing.T) {
	text, err := cmd.Usage[cmd.App[root]]("herdr-reflow")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, "REPO:BRANCH") {
		t.Fatalf("usage missing REPO:BRANCH:\n%s", text)
	}
	if !strings.Contains(text, "[arg...]") {
		t.Fatalf("usage missing rest positional:\n%s", text)
	}
}
