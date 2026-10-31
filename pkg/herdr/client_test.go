package herdr

import (
	"bufio"
	"encoding/json"
	"net"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSocketPathOrder(t *testing.T) {
	t.Parallel()
	require.Equal(t, "/override", resolveSocket("/override", "/env", "/cfg"))
	require.Equal(t, "/env", resolveSocket("", "/env", "/cfg"))
	require.Equal(t, filepath.Join("/cfg", "herdr", "herdr.sock"), resolveSocket("", "", "/cfg"))
}

func TestSocketMissing(t *testing.T) {
	t.Parallel()
	_, err := (&Client{Path: filepath.Join(t.TempDir(), "missing.sock")}).Socket(t.Context())
	require.ErrorIs(t, err, errServerDown)
}

func TestWorkspacesOverSocket(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	sock := filepath.Join(dir, "herdr.sock")
	ln, err := net.Listen("unix", sock)
	require.NoError(t, err)
	t.Cleanup(func() { ln.Close() })
	got := make(chan []byte, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		line, err := bufio.NewReader(conn).ReadBytes('\n')
		if err != nil {
			return
		}
		got <- line
		if err := writeLine(conn, `{"id":"workspace.list","result":{"workspaces":[{"workspace_id":"w1","label":"repo"}]}}`); err != nil {
			return
		}
	}()

	spaces, err := (&Client{Path: sock}).Workspaces(t.Context())
	require.NoError(t, err)
	require.Equal(t, []Workspace{{ID: "w1", Label: "repo"}}, spaces)

	var req struct {
		Method string         `json:"method"`
		Params map[string]any `json:"params"`
	}
	require.NoError(t, json.Unmarshal(<-got, &req))
	require.Equal(t, "workspace.list", req.Method)
	require.Empty(t, req.Params)
}

func TestMutationsUseSocketParams(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	sock := filepath.Join(dir, "herdr.sock")
	ln, err := net.Listen("unix", sock)
	require.NoError(t, err)
	t.Cleanup(func() { ln.Close() })
	got := make(chan []byte, 4)
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			line, err := bufio.NewReader(conn).ReadBytes('\n')
			if err != nil {
				conn.Close()
				return
			}
			got <- append([]byte(nil), line...)
			if err := writeLine(conn, `{"id":"x","result":{"workspace":{"workspace_id":"w1"},"already_open":false}}`); err != nil {
				conn.Close()
				return
			}
			conn.Close()
		}
	}()
	c := &Client{Path: sock}
	_, err = c.CreateWorkspace(t.Context(), "/repo", "repo")
	require.NoError(t, err)
	opened, err := c.OpenWorktree(t.Context(), OpenWorktree{Path: "/wt", Label: "topic", Workspace: "wMain"})
	require.NoError(t, err)
	require.Equal(t, "w1", opened.Workspace.ID)
	require.NoError(t, c.MovePaneNewTab(t.Context(), "p1", "w1", "topic"))

	want := []map[string]any{
		{"cwd": "/repo", "label": "repo", "focus": false},
		{"path": "/wt", "label": "topic", "focus": false, "workspace_id": "wMain"},
		{
			"pane_id": "p1",
			"focus":   true,
			"destination": map[string]any{
				"type":         "new_tab",
				"workspace_id": "w1",
				"label":        "topic",
			},
		},
	}
	methods := []string{"workspace.create", "worktree.open", "pane.move"}
	for i := range want {
		var req struct {
			Method string         `json:"method"`
			Params map[string]any `json:"params"`
		}
		require.NoError(t, json.Unmarshal(<-got, &req))
		require.Equal(t, methods[i], req.Method)
		require.Equal(t, want[i], req.Params)
	}
}

func writeLine(conn net.Conn, payload string) error {
	_, err := conn.Write(append([]byte(payload), '\n'))
	return err
}

func TestLiveLists(t *testing.T) {
	c := &Client{}
	if _, err := c.Socket(t.Context()); err != nil {
		t.Skip(err)
	}
	workspaces, err := c.Workspaces(t.Context())
	require.NoError(t, err)
	require.NotEmpty(t, workspaces)
	_, err = c.Tabs(t.Context())
	require.NoError(t, err)
	_, err = c.Panes(t.Context())
	require.NoError(t, err)
	sock, err := c.Socket(t.Context())
	require.NoError(t, err)
	require.NotEmpty(t, sock)
}
