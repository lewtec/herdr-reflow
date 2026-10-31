package herdr

import (
	"bufio"
	"encoding/json"
	"errors"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/lewtec/lewkit/x/git"
	"github.com/stretchr/testify/require"
)

func TestReorderMovesPaneIntoLastWorktree(t *testing.T) {
	t.Parallel()
	repo, wts := testRepo(t, "other", "topic")
	logPath, sock := (fakeSession{repo: repo, wts: wts}).install(t)
	_, err := Reorder(t.Context(), Options{
		Pin:    repo,
		Home:   t.TempDir(),
		Pane:   "wCaller",
		Client: &Client{Path: sock},
		Git:    &git.Git{},
		Specs: []RepoBranch{
			{Repo: repo, Branch: "topic"},
			{Repo: repo, Branch: "other"},
		},
	})
	require.NoError(t, err)
	moves := paneMoves(t, logPath)
	require.Equal(t, []moveRecord{{
		PaneID: "wCaller",
		Focus:  true,
		Destination: moveDest{
			Type:        "new_tab",
			WorkspaceID: "wOther",
			Label:       "other",
		},
	}}, moves)
}

func TestReorderSkipsPaneMoveWithoutPane(t *testing.T) {
	t.Parallel()
	repo, wts := testRepo(t, "topic")
	logPath, sock := (fakeSession{repo: repo, wts: wts}).install(t)
	_, err := Reorder(t.Context(), Options{
		Pin:    repo,
		Home:   t.TempDir(),
		Client: &Client{Path: sock},
		Git:    &git.Git{},
		Specs:  []RepoBranch{{Repo: repo, Branch: "topic"}},
	})
	require.NoError(t, err)
	require.Empty(t, paneMoves(t, logPath))
}

func TestReorderSkipsPaneMoveOnMainBranch(t *testing.T) {
	t.Parallel()
	repo, wts := testRepo(t, "topic")
	logPath, sock := (fakeSession{repo: repo, wts: wts}).install(t)
	_, err := Reorder(t.Context(), Options{
		Pin:    repo,
		Home:   t.TempDir(),
		Pane:   "wCaller",
		Client: &Client{Path: sock},
		Git:    &git.Git{},
		Specs:  []RepoBranch{{Repo: repo, Branch: "main"}},
	})
	require.NoError(t, err)
	require.Empty(t, paneMoves(t, logPath))
}

func TestReorderPaneMoveError(t *testing.T) {
	t.Parallel()
	repo, wts := testRepo(t, "topic")
	logPath, sock := (fakeSession{repo: repo, wts: wts, moveErr: "pane busy"}).install(t)
	_, err := Reorder(t.Context(), Options{
		Pin:    repo,
		Home:   t.TempDir(),
		Pane:   "wCaller",
		Client: &Client{Path: sock},
		Git:    &git.Git{},
		Specs:  []RepoBranch{{Repo: repo, Branch: "topic"}},
	})
	require.ErrorContains(t, err, "pane busy")
	require.Equal(t, []moveRecord{{
		PaneID: "wCaller",
		Focus:  true,
		Destination: moveDest{
			Type:        "new_tab",
			WorkspaceID: "wTopic",
			Label:       "topic",
		},
	}}, paneMoves(t, logPath))
}

type moveDest struct {
	Type        string `json:"type"`
	WorkspaceID string `json:"workspace_id"`
	Label       string `json:"label"`
}

type moveRecord struct {
	PaneID      string   `json:"pane_id"`
	Focus       bool     `json:"focus"`
	Destination moveDest `json:"destination"`
}

func paneMoves(t *testing.T, logPath string) []moveRecord {
	t.Helper()
	raw, err := os.ReadFile(logPath)
	require.NoError(t, err)
	var moves []moveRecord
	for line := range strings.SplitSeq(strings.TrimSpace(string(raw)), "\n") {
		if line == "" {
			continue
		}
		var rec moveRecord
		require.NoError(t, json.Unmarshal([]byte(line), &rec))
		moves = append(moves, rec)
	}
	return moves
}

func testRepo(t *testing.T, branches ...string) (string, map[string]string) {
	t.Helper()
	root := t.TempDir()
	repo := filepath.Join(root, "repo")
	require.NoError(t, os.Mkdir(repo, 0o755))
	gitCmd := func(dir string, args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=test",
			"GIT_AUTHOR_EMAIL=t@example.com",
			"GIT_COMMITTER_NAME=test",
			"GIT_COMMITTER_EMAIL=t@example.com",
			"GIT_CONFIG_NOSYSTEM=1",
		)
		out, err := cmd.CombinedOutput()
		require.NoErrorf(t, err, "git %s\n%s", strings.Join(args, " "), out)
	}
	gitCmd(repo, "init", "-b", "main")
	gitCmd(repo, "config", "user.email", "t@example.com")
	gitCmd(repo, "config", "user.name", "test")
	gitCmd(repo, "config", "commit.gpgsign", "false")
	require.NoError(t, os.WriteFile(filepath.Join(repo, "README"), []byte("x\n"), 0o644))
	gitCmd(repo, "add", "README")
	gitCmd(repo, "commit", "-m", "init")
	wts := map[string]string{}
	for _, branch := range branches {
		wt := filepath.Join(root, branch)
		gitCmd(repo, "worktree", "add", "-b", branch, wt)
		wts[branch] = git.Resolve(wt)
	}
	return git.Resolve(repo), wts
}

type fakeSession struct {
	repo    string
	wts     map[string]string
	moveErr string
}

func (s fakeSession) install(t *testing.T) (string, string) {
	t.Helper()
	dir := t.TempDir()
	logPath := filepath.Join(dir, "log")
	type row struct {
		branch string
		path   string
		id     string
		linked bool
	}
	rows := []row{{branch: "main", path: s.repo, id: "wMain"}}
	for branch, path := range s.wts {
		rows = append(rows, row{branch: branch, path: path, id: workspaceID(branch), linked: true})
	}
	slices.SortFunc(rows[1:], func(a, b row) int {
		return strings.Compare(a.path, b.path)
	})
	var spaces []Workspace
	var tabs []Tab
	var panes []Pane
	opens := map[string]string{}
	for i, row := range rows {
		label := row.branch
		if !row.linked {
			label = "repo"
		}
		spaces = append(spaces, Workspace{
			ID: row.id, Label: label, Number: i + 1,
			Worktree: &Binding{
				Checkout: row.path, Root: s.repo, Name: "repo", Linked: row.linked,
			},
		})
		tabID := row.id + ":t1"
		tabs = append(tabs, Tab{ID: tabID, WorkspaceID: row.id})
		panes = append(panes, Pane{ID: row.id + ":p1", TabID: tabID, WorkspaceID: row.id, Cwd: row.path})
		if row.linked {
			opens[row.path] = mustJSON(t, map[string]any{
				"result": Opened{Workspace: Workspace{ID: row.id, Label: row.branch}, AlreadyOpen: true},
			})
		}
	}
	require.NoError(t, os.WriteFile(logPath, nil, 0o644))
	wsResp := mustJSON(t, map[string]any{"result": map[string]any{"workspaces": spaces}})
	tabsResp := mustJSON(t, map[string]any{"result": map[string]any{"tabs": tabs}})
	panesResp := mustJSON(t, map[string]any{"result": map[string]any{"panes": panes}})
	sockPath := filepath.Join(dir, "herdr.sock")
	ln, err := net.Listen("unix", sockPath)
	require.NoError(t, err)
	t.Cleanup(func() { ln.Close() })
	srv := &fakeServer{
		ln: ln, logPath: logPath, moveErr: s.moveErr,
		ws: wsResp, tabs: tabsResp, panes: panesResp, opens: opens,
	}
	go srv.serve()
	return logPath, sockPath
}

type fakeServer struct {
	ln      net.Listener
	mu      sync.Mutex
	logPath string
	moveErr string
	ws      string
	tabs    string
	panes   string
	opens   map[string]string
}

func (s *fakeServer) serve() {
	for {
		conn, err := s.ln.Accept()
		if err != nil {
			return
		}
		go s.handle(conn)
	}
}

func (s *fakeServer) handle(conn net.Conn) {
	defer conn.Close()
	line, err := bufio.NewReader(conn).ReadBytes('\n')
	if err != nil {
		return
	}
	var req struct {
		Method string          `json:"method"`
		Params json.RawMessage `json:"params"`
	}
	if err := json.Unmarshal(line, &req); err != nil {
		writeRPC(conn, rpcError(req.Method, "bad_request", err.Error()))
		return
	}
	switch req.Method {
	case "workspace.list":
		writeRPC(conn, s.ws)
	case "tab.list":
		writeRPC(conn, s.tabs)
	case "pane.list":
		writeRPC(conn, s.panes)
	case "worktree.open":
		var params struct {
			Path string `json:"path"`
		}
		if err := json.Unmarshal(req.Params, &params); err != nil {
			writeRPC(conn, rpcError(req.Method, "bad_request", err.Error()))
			return
		}
		found, ok := s.opens[params.Path]
		if !ok {
			writeRPC(conn, rpcError(req.Method, "unexpected", "unexpected path: "+params.Path))
			return
		}
		writeRPC(conn, found)
	case "pane.move":
		s.mu.Lock()
		err := appendLog(s.logPath, req.Params)
		s.mu.Unlock()
		if err != nil {
			writeRPC(conn, rpcError(req.Method, "log", err.Error()))
			return
		}
		if s.moveErr != "" {
			writeRPC(conn, rpcError(req.Method, "move", s.moveErr))
			return
		}
		writeRPC(conn, `{"id":"pane.move","result":{}}`)
	case "workspace.move_block", "workspace.rename", "workspace.create":
		writeRPC(conn, `{"id":"`+req.Method+`","result":{}}`)
	default:
		writeRPC(conn, rpcError(req.Method, "unexpected", "unexpected: "+req.Method))
	}
}

func appendLog(path string, line []byte) error {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	_, werr := f.Write(append(line, '\n'))
	cerr := f.Close()
	return errors.Join(werr, cerr)
}

func writeRPC(conn net.Conn, payload string) {
	if err := writeLine(conn, payload); err != nil {
		return
	}
}

func rpcError(method, code, message string) string {
	raw, err := json.Marshal(map[string]any{
		"id": method,
		"error": map[string]string{
			"code":    code,
			"message": message,
		},
	})
	if err != nil {
		return `{"id":"error","error":{"code":"marshal","message":"marshal"}}`
	}
	return string(raw)
}

func workspaceID(branch string) string {
	switch branch {
	case "topic":
		return "wTopic"
	case "other":
		return "wOther"
	default:
		return "w" + branch
	}
}

func mustJSON(t *testing.T, doc any) string {
	t.Helper()
	raw, err := json.Marshal(doc)
	require.NoError(t, err)
	return string(raw)
}
