package herdr

import (
	"testing"
	"time"

	"github.com/lewtec/lewkit/x/cmd"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRepoBranchParse(t *testing.T) {
	type args struct {
		specs []RepoBranch
	}
	got := cmd.ParseOK[args](t, ".dotfiles:feat/teste", "lewkit:main")
	require.Len(t, got.specs, 2)
	assert.Equal(t, ".dotfiles", got.specs[0].Repo)
	assert.Equal(t, "feat/teste", got.specs[0].Branch)
	assert.Equal(t, "lewkit", got.specs[1].Repo)
	assert.Equal(t, "main", got.specs[1].Branch)

	today := time.Now().Format("20060102")
	dated := cmd.ParseOK[args](t, ".:%d-teste", "%d:keep", "repo:%d/%d")
	require.Len(t, dated.specs, 3)
	assert.Equal(t, ".", dated.specs[0].Repo)
	assert.Equal(t, today+"-teste", dated.specs[0].Branch)
	assert.Equal(t, "%d", dated.specs[1].Repo)
	assert.Equal(t, "keep", dated.specs[1].Branch)
	assert.Equal(t, "repo", dated.specs[2].Repo)
	assert.Equal(t, today+"/"+today, dated.specs[2].Branch)

	err := cmd.ParseErr[args](t, "nocolon")
	assert.ErrorIs(t, err, cmd.ErrInvalidArgument)
	err = cmd.ParseErr[args](t, ":onlybranch")
	assert.ErrorIs(t, err, cmd.ErrInvalidArgument)
	err = cmd.ParseErr[args](t, "onlyrepo:")
	assert.ErrorIs(t, err, cmd.ErrInvalidArgument)
}

func TestOrderPinThenGroups(t *testing.T) {
	p := &plan{pin: "/pin"}
	p.spaces = []Space{
		{ID: "dir", Label: "tmp", Checkout: "/tmp"},
		{ID: "wt", Label: "feat", Checkout: "/wt", RepoRoot: "/repo", Linked: true, Source: "git"},
		{ID: "main", Label: "repo", Checkout: "/repo", RepoRoot: "/repo", Source: "herdr"},
		{ID: "pinwt", Label: "branch", Checkout: "/pin-wt", RepoRoot: "/pin", Linked: true},
		{ID: "pin", Label: "dotfiles", Checkout: "/pin", RepoRoot: "/pin"},
	}
	got := p.order()
	ids := make([]string, len(got))
	for i, space := range got {
		ids[i] = space.ID
	}
	assert.Equal(t, []string{"pin", "pinwt", "main", "wt", "dir"}, ids)
	assert.Equal(t, "this checkout first; on main/master", p.criterion(got[0]))
	assert.Equal(t, "worktree", Kind(got[1]))
	assert.Equal(t, "repo", Kind(got[2]))
	assert.Equal(t, "dir", Kind(got[4]))
}

func TestLayoutSlugAndMatch(t *testing.T) {
	slug := layoutSlug("/home/lucasew/.grok/worktrees/lewtec-lewkit/branch", []string{"/home/lucasew/.grok/worktrees"})
	assert.Equal(t, "lewtec-lewkit", slug)
	root := matchSlug("lewtec-lewkit", []nameRoot{{"lewkit", "/repo"}, {"other", "/no"}})
	assert.Equal(t, "/repo", root)
}
