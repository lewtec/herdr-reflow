package herdr

import (
	"bufio"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
)

var (
	errEmptyResult      = errors.New("empty result")
	errMissingWorkspace = errors.New("missing workspace id")
	errNotSocket        = errors.New("not a socket")
	errServerDown       = errors.New("herdr server is not running")
)

type serverError struct {
	Method  string
	Code    string
	Message string
}

func (e serverError) Error() string {
	return "herdr " + e.Method + ": " + e.Code + ": " + e.Message
}

// Client calls the Herdr server over its unix socket.
// Path overrides HERDR_SOCKET_PATH and the default config socket.
type Client struct {
	Path string
}

// Workspace is one row from workspace list.
type Workspace struct {
	ID       string   `json:"workspace_id"`
	Label    string   `json:"label"`
	Number   int      `json:"number"`
	Worktree *Binding `json:"worktree"`
}

// Binding is the worktree object Herdr stores on a workspace.
type Binding struct {
	Checkout string `json:"checkout_path"`
	Root     string `json:"repo_root"`
	Name     string `json:"repo_name"`
	Linked   bool   `json:"is_linked_worktree"`
}

// Tab is one row from tab list.
type Tab struct {
	ID          string `json:"tab_id"`
	WorkspaceID string `json:"workspace_id"`
}

// Pane is one row from pane list.
type Pane struct {
	ID          string `json:"pane_id"`
	TabID       string `json:"tab_id"`
	WorkspaceID string `json:"workspace_id"`
	Cwd         string `json:"cwd"`
}

// OpenWorktree asks worktree open to show a checkout.
// Workspace and Cwd are exclusive: Cwd is the first tab of a new space,
// Workspace nests under an existing main.
type OpenWorktree struct {
	Path      string
	Label     string
	Workspace string
	Cwd       string
}

// Opened is the workspace worktree open returned.
type Opened struct {
	Workspace   Workspace `json:"workspace"`
	AlreadyOpen bool      `json:"already_open"`
}

func resolveSocket(override, env, configDir string) string {
	return cmp.Or(override, env, filepath.Join(configDir, "herdr", "herdr.sock"))
}

func (c *Client) socketPath() (string, error) {
	override := ""
	if c != nil {
		override = c.Path
	}
	env := os.Getenv("HERDR_SOCKET_PATH")
	if override != "" || env != "" {
		return resolveSocket(override, env, ""), nil
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return resolveSocket("", "", dir), nil
}

// Socket is the server socket path. A missing path means the server is down.
func (c *Client) Socket(ctx context.Context) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	path, err := c.socketPath()
	if err != nil {
		return "", err
	}
	info, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", errServerDown
		}
		return "", fmt.Errorf("herdr socket: %w", err)
	}
	if info.Mode()&os.ModeSocket == 0 {
		return "", fmt.Errorf("herdr socket: %s: %w", path, errNotSocket)
	}
	return path, nil
}

func (c *Client) call(ctx context.Context, method string, params any, dest any) error {
	if params == nil {
		params = map[string]any{}
	}
	sock, err := c.Socket(ctx)
	if err != nil {
		return err
	}
	payload, err := json.Marshal(map[string]any{
		"id":     method,
		"method": method,
		"params": params,
	})
	if err != nil {
		return err
	}
	var dialer net.Dialer
	conn, err := dialer.DialContext(ctx, "unix", sock)
	if err != nil {
		return fmt.Errorf("herdr socket: %w", err)
	}
	defer conn.Close()
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	defer stop()
	if _, err := conn.Write(append(payload, '\n')); err != nil {
		return fmt.Errorf("herdr %s: %w", method, err)
	}
	line, err := bufio.NewReader(conn).ReadBytes('\n')
	if err != nil {
		return fmt.Errorf("herdr %s: %w", method, err)
	}
	var resp struct {
		Result json.RawMessage `json:"result"`
		Error  *struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(line, &resp); err != nil {
		return fmt.Errorf("herdr %s: %w", method, err)
	}
	if resp.Error != nil {
		return serverError{Method: method, Code: resp.Error.Code, Message: resp.Error.Message}
	}
	if dest == nil {
		return nil
	}
	if len(resp.Result) == 0 || string(resp.Result) == "null" {
		return fmt.Errorf("herdr %s: %w", method, errEmptyResult)
	}
	if err := json.Unmarshal(resp.Result, dest); err != nil {
		return fmt.Errorf("herdr %s: %w", method, err)
	}
	return nil
}

// Workspaces lists the session.
func (c *Client) Workspaces(ctx context.Context) ([]Workspace, error) {
	var body struct {
		Workspaces []Workspace `json:"workspaces"`
	}
	if err := c.call(ctx, "workspace.list", map[string]any{}, &body); err != nil {
		return nil, err
	}
	return body.Workspaces, nil
}

// Tabs lists tabs.
func (c *Client) Tabs(ctx context.Context) ([]Tab, error) {
	var body struct {
		Tabs []Tab `json:"tabs"`
	}
	if err := c.call(ctx, "tab.list", map[string]any{}, &body); err != nil {
		return nil, err
	}
	return body.Tabs, nil
}

// Panes lists panes.
func (c *Client) Panes(ctx context.Context) ([]Pane, error) {
	var body struct {
		Panes []Pane `json:"panes"`
	}
	if err := c.call(ctx, "pane.list", map[string]any{}, &body); err != nil {
		return nil, err
	}
	return body.Panes, nil
}

// CreateWorkspace opens a workspace whose first tab is cwd.
func (c *Client) CreateWorkspace(ctx context.Context, cwd, label string) (Workspace, error) {
	var body struct {
		Workspace Workspace `json:"workspace"`
	}
	if err := c.call(ctx, "workspace.create", map[string]any{
		"cwd":   cwd,
		"label": label,
		"focus": false,
	}, &body); err != nil {
		return Workspace{}, err
	}
	if body.Workspace.ID == "" {
		return Workspace{}, fmt.Errorf("herdr workspace.create: %w", errMissingWorkspace)
	}
	return body.Workspace, nil
}

// RenameWorkspace sets the workspace label.
func (c *Client) RenameWorkspace(ctx context.Context, id, label string) error {
	return c.call(ctx, "workspace.rename", map[string]any{
		"workspace_id": id,
		"label":        label,
	}, nil)
}

// MovePaneNewTab moves paneID into workspaceID as a new tab and focuses it.
func (c *Client) MovePaneNewTab(ctx context.Context, paneID, workspaceID, label string) error {
	dest := map[string]any{
		"type":         "new_tab",
		"workspace_id": workspaceID,
	}
	if label != "" {
		dest["label"] = label
	}
	return c.call(ctx, "pane.move", map[string]any{
		"pane_id":     paneID,
		"focus":       true,
		"destination": dest,
	}, nil)
}

// OpenWorktree runs worktree.open.
func (c *Client) OpenWorktree(ctx context.Context, req OpenWorktree) (Opened, error) {
	params := map[string]any{
		"path":  req.Path,
		"label": req.Label,
		"focus": false,
	}
	if req.Workspace != "" {
		params["workspace_id"] = req.Workspace
	} else {
		params["cwd"] = req.Cwd
	}
	var opened Opened
	if err := c.call(ctx, "worktree.open", params, &opened); err != nil {
		return Opened{}, err
	}
	return opened, nil
}

// MoveBlock places workspace IDs immediately before beforeID.
func (c *Client) MoveBlock(ctx context.Context, ids []string, beforeID string) error {
	return c.call(ctx, "workspace.move_block", map[string]any{
		"workspace_ids":       ids,
		"before_workspace_id": beforeID,
	}, nil)
}
