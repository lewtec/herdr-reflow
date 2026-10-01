package herdr

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/lewtec/lewkit/x/git"
	"github.com/stretchr/testify/require"
)

func TestReorderMovesPaneIntoLastWorktree(t *testing.T) {
	t.Parallel()
	repo, wts := testRepo(t, "other", "topic")
	logPath, bin := (fakeSession{repo: repo, wts: wts}).install(t)
	_, err := Reorder(t.Context(), Options{
		Pin:    repo,
		Home:   t.TempDir(),
		Pane:   "wCaller",
		Client: &Client{Bin: bin},
		Git:    &git.Git{},
		Specs: []RepoBranch{
			{Repo: repo, Branch: "topic"},
			{Repo: repo, Branch: "other"},
		},
	})
	require.NoError(t, err)
	moves := paneMoves(t, logPath)
	require.Equal(t, []string{
		"pane move wCaller --new-tab --workspace wOther --focus --label other",
	}, moves)
}

func TestReorderSkipsPaneMoveWithoutPane(t *testing.T) {
	t.Parallel()
	repo, wts := testRepo(t, "topic")
	logPath, bin := (fakeSession{repo: repo, wts: wts}).install(t)
	_, err := Reorder(t.Context(), Options{
		Pin:    repo,
		Home:   t.TempDir(),
		Client: &Client{Bin: bin},
		Git:    &git.Git{},
		Specs:  []RepoBranch{{Repo: repo, Branch: "topic"}},
	})
	require.NoError(t, err)
	require.Empty(t, paneMoves(t, logPath))
}

func TestReorderSkipsPaneMoveOnMainBranch(t *testing.T) {
	t.Parallel()
	repo, wts := testRepo(t, "topic")
	logPath, bin := (fakeSession{repo: repo, wts: wts}).install(t)
	_, err := Reorder(t.Context(), Options{
		Pin:    repo,
		Home:   t.TempDir(),
		Pane:   "wCaller",
		Client: &Client{Bin: bin},
		Git:    &git.Git{},
		Specs:  []RepoBranch{{Repo: repo, Branch: "main"}},
	})
	require.NoError(t, err)
	require.Empty(t, paneMoves(t, logPath))
}

func TestReorderPaneMoveError(t *testing.T) {
	t.Parallel()
	repo, wts := testRepo(t, "topic")
	logPath, bin := (fakeSession{repo: repo, wts: wts, moveErr: "pane busy"}).install(t)
	_, err := Reorder(t.Context(), Options{
		Pin:    repo,
		Home:   t.TempDir(),
		Pane:   "wCaller",
		Client: &Client{Bin: bin},
		Git:    &git.Git{},
		Specs:  []RepoBranch{{Repo: repo, Branch: "topic"}},
	})
	require.ErrorContains(t, err, "pane busy")
	require.Equal(t, []string{
		"pane move wCaller --new-tab --workspace wTopic --focus --label topic",
	}, paneMoves(t, logPath))
}

func paneMoves(t *testing.T, logPath string) []string {
	t.Helper()
	raw, err := os.ReadFile(logPath)
	require.NoError(t, err)
	var moves []string
	for line := range strings.SplitSeq(string(raw), "\n") {
		if strings.HasPrefix(line, "pane move ") {
			moves = append(moves, line)
		}
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
	writeJSON(t, filepath.Join(dir, "workspaces"), map[string]any{"result": map[string]any{"workspaces": spaces}})
	writeJSON(t, filepath.Join(dir, "tabs"), map[string]any{"result": map[string]any{"tabs": tabs}})
	writeJSON(t, filepath.Join(dir, "panes"), map[string]any{"result": map[string]any{"panes": panes}})
	openFile := filepath.Join(dir, "opens.tsv")
	var table strings.Builder
	for path, payload := range opens {
		table.WriteString(path)
		table.WriteByte('\t')
		table.WriteString(payload)
		table.WriteByte('\n')
	}
	require.NoError(t, os.WriteFile(openFile, []byte(table.String()), 0o644))
	script := filepath.Join(dir, "herdr")
	body := `#!/bin/sh
printf '%s\n' "$*" >> "$LOG"
case "$1 $2" in
  "workspace list") cat "$WS" ;;
  "tab list") cat "$TABS" ;;
  "pane list") cat "$PANES" ;;
  "worktree open")
    path=""
    prev=""
    for arg in "$@"; do
      if [ "$prev" = "--path" ]; then
        path=$arg
      fi
      prev=$arg
    done
    found=$(awk -F '\t' -v path="$path" '$1 == path { print $2; exit }' "$OPENS")
    if [ -z "$found" ]; then
      echo "unexpected path: $path" >&2
      exit 1
    fi
    printf '%s\n' "$found"
    ;;
  "pane move")
    if [ -n "$MOVE_ERR" ]; then
      echo "$MOVE_ERR" >&2
      exit 1
    fi
    printf '%s\n' '{"result":{}}'
    ;;
  *)
    echo "unexpected: $*" >&2
    exit 1
    ;;
esac
`
	require.NoError(t, os.WriteFile(script, []byte(body), 0o755))
	wrapper := filepath.Join(dir, "herdrw")
	wrap := "#!/bin/sh\n" +
		"export LOG=" + shQuote(logPath) + "\n" +
		"export WS=" + shQuote(filepath.Join(dir, "workspaces")) + "\n" +
		"export TABS=" + shQuote(filepath.Join(dir, "tabs")) + "\n" +
		"export PANES=" + shQuote(filepath.Join(dir, "panes")) + "\n" +
		"export OPENS=" + shQuote(openFile) + "\n" +
		"export MOVE_ERR=" + shQuote(s.moveErr) + "\n" +
		"exec " + shQuote(script) + " \"$@\"\n"
	require.NoError(t, os.WriteFile(wrapper, []byte(wrap), 0o755))
	return logPath, wrapper
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

func writeJSON(t *testing.T, path string, doc any) {
	t.Helper()
	raw, err := json.Marshal(doc)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, append(raw, '\n'), 0o644))
}

func mustJSON(t *testing.T, doc any) string {
	t.Helper()
	raw, err := json.Marshal(doc)
	require.NoError(t, err)
	return string(raw)
}

func shQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}
