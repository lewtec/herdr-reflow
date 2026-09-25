package herdr

import (
	"testing"

	"github.com/lewtec/lewkit/x/taskgroup"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestReportNodesNestWorktrees(t *testing.T) {
	report := Report{
		Pin: "/pin",
		Order: []Space{
			{ID: "pin", Label: "dotfiles", Checkout: "/pin", RepoRoot: "/pin", Branch: "master", Source: "herdr"},
			{ID: "wt", Label: "feat", Checkout: "/pin-wt", RepoRoot: "/pin", Linked: true, Branch: "feat", Source: "git"},
			{ID: "main", Label: "lewkit", Checkout: "/repo", RepoRoot: "/repo", Branch: "main", Source: "herdr"},
			{ID: "feat", Label: "topic", Checkout: "/repo-wt", RepoRoot: "/repo", Linked: true, Branch: "topic", Source: "layout", Identity: "/old"},
			{ID: "dir", Label: "tmp", Checkout: "/tmp"},
		},
	}
	nodes := report.Nodes()
	byLabel := map[string]int{}
	for i, node := range nodes {
		if _, ok := byLabel[node.Name]; !ok {
			byLabel[node.Name] = i
		}
	}
	require.Contains(t, byLabel, "dotfiles")
	require.Contains(t, byLabel, "feat")
	require.Contains(t, byLabel, "lewkit")
	require.Contains(t, byLabel, "topic")
	require.Contains(t, byLabel, "tmp")
	assert.Equal(t, "/pin", nodes[byLabel["dotfiles"]].Message)
	assert.Equal(t, "/pin-wt", nodes[byLabel["feat"]].Message)
	assert.Equal(t, "/tmp", nodes[byLabel["tmp"]].Message)
	assert.Equal(t, "📁", nodes[byLabel["tmp"]].Emoji)
	assert.Equal(t, taskgroup.IO, nodes[byLabel["tmp"]].Pool)
	assert.Empty(t, nodes[byLabel["dotfiles"]].Emoji)
	assert.Equal(t, taskgroup.IO, nodes[byLabel["dotfiles"]].Pool)
	assert.Equal(t, taskgroup.CPU, nodes[byLabel["feat"]].Pool)

	assert.Equal(t, nodes[byLabel["dotfiles"]].ID, nodes[byLabel["feat"]].Parent)
	assert.Equal(t, nodes[byLabel["lewkit"]].ID, nodes[byLabel["topic"]].Parent)
	assert.Zero(t, nodes[byLabel["dotfiles"]].Parent)
	assert.Zero(t, nodes[byLabel["lewkit"]].Parent)
	assert.Zero(t, nodes[byLabel["tmp"]].Parent)
	assert.Less(t, byLabel["dotfiles"], byLabel["feat"])
	assert.Less(t, byLabel["feat"], byLabel["lewkit"])
	assert.Less(t, byLabel["topic"], byLabel["tmp"])

	var underDot []string
	dot := nodes[byLabel["dotfiles"]].ID
	for _, node := range nodes {
		if node.Parent == dot {
			underDot = append(underDot, node.Name)
		}
	}
	assert.Equal(t, []string{"feat"}, underDot)

	var underTopic []string
	topic := nodes[byLabel["topic"]].ID
	for _, node := range nodes {
		if node.Parent == topic {
			underTopic = append(underTopic, node.Name+":"+node.Message)
		}
	}
	assert.Equal(t, []string{"identity:/old"}, underTopic)
}
