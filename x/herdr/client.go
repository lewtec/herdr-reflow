package herdr

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os/exec"
	"strings"
)

var (
	errEmptyResult      = errors.New("empty result")
	errMissingWorkspace = errors.New("missing workspace id")
	errServerDown       = errors.New("herdr server is not running")
	errMoveBlock        = errors.New("workspace.move_block")
)

// Client calls the herdr CLI. Bin defaults to "herdr".
type Client struct {
	Bin string
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

func (c *Client) bin() string {
	if c == nil || c.Bin == "" {
		return "herdr"
	}
	return c.Bin
}

func (c *Client) run(ctx context.Context, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, c.bin(), args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = strings.TrimSpace(stdout.String())
		}
		if msg == "" {
			return nil, fmt.Errorf("herdr %s: %w", strings.Join(args, " "), err)
		}
		return nil, fmt.Errorf("herdr %s: %s: %w", strings.Join(args, " "), msg, err)
	}
	return stdout.Bytes(), nil
}

func decodeResult[T any](raw []byte) (T, error) {
	var zero T
	var env struct {
		Result json.RawMessage `json:"result"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		return zero, fmt.Errorf("herdr: %w", err)
	}
	if len(env.Result) == 0 {
		return zero, fmt.Errorf("herdr: %w", errEmptyResult)
	}
	var out T
	if err := json.Unmarshal(env.Result, &out); err != nil {
		return zero, fmt.Errorf("herdr: %w", err)
	}
	return out, nil
}

// Workspaces lists the session.
func (c *Client) Workspaces(ctx context.Context) ([]Workspace, error) {
	raw, err := c.run(ctx, "workspace", "list")
	if err != nil {
		return nil, err
	}
	body, err := decodeResult[struct {
		Workspaces []Workspace `json:"workspaces"`
	}](raw)
	if err != nil {
		return nil, err
	}
	return body.Workspaces, nil
}

// Tabs lists tabs.
func (c *Client) Tabs(ctx context.Context) ([]Tab, error) {
	raw, err := c.run(ctx, "tab", "list")
	if err != nil {
		return nil, err
	}
	body, err := decodeResult[struct {
		Tabs []Tab `json:"tabs"`
	}](raw)
	if err != nil {
		return nil, err
	}
	return body.Tabs, nil
}

// Panes lists panes.
func (c *Client) Panes(ctx context.Context) ([]Pane, error) {
	raw, err := c.run(ctx, "pane", "list")
	if err != nil {
		return nil, err
	}
	body, err := decodeResult[struct {
		Panes []Pane `json:"panes"`
	}](raw)
	if err != nil {
		return nil, err
	}
	return body.Panes, nil
}

// CreateWorkspace opens a workspace whose first tab is cwd.
func (c *Client) CreateWorkspace(ctx context.Context, cwd, label string) (Workspace, error) {
	raw, err := c.run(ctx, "workspace", "create", "--cwd", cwd, "--label", label, "--no-focus")
	if err != nil {
		return Workspace{}, err
	}
	body, err := decodeResult[struct {
		Workspace Workspace `json:"workspace"`
	}](raw)
	if err != nil {
		return Workspace{}, err
	}
	if body.Workspace.ID == "" {
		return Workspace{}, fmt.Errorf("herdr workspace create: %w", errMissingWorkspace)
	}
	return body.Workspace, nil
}

// RenameWorkspace sets the workspace label.
func (c *Client) RenameWorkspace(ctx context.Context, id, label string) error {
	_, err := c.run(ctx, "workspace", "rename", id, label)
	return err
}

// OpenWorktree runs worktree open.
func (c *Client) OpenWorktree(ctx context.Context, req OpenWorktree) (Opened, error) {
	args := []string{"worktree", "open", "--path", req.Path, "--label", req.Label, "--no-focus"}
	if req.Workspace != "" {
		args = append(args, "--workspace", req.Workspace)
	} else {
		args = append(args, "--cwd", req.Cwd)
	}
	raw, err := c.run(ctx, args...)
	if err != nil {
		return Opened{}, err
	}
	return decodeResult[Opened](raw)
}

type statusDoc struct {
	Server struct {
		Socket string `json:"socket"`
	} `json:"server"`
}

// Socket is the current server socket from herdr status --json.
func (c *Client) Socket(ctx context.Context) (string, error) {
	raw, err := c.run(ctx, "status", "--json")
	if err != nil {
		return "", err
	}
	var doc statusDoc
	if err := json.Unmarshal(raw, &doc); err != nil {
		return "", fmt.Errorf("herdr status: %w", err)
	}
	if doc.Server.Socket == "" {
		return "", errServerDown
	}
	return doc.Server.Socket, nil
}

// MoveBlock places workspace IDs immediately before beforeID.
func (c *Client) MoveBlock(ctx context.Context, ids []string, beforeID string) error {
	sock, err := c.Socket(ctx)
	if err != nil {
		return err
	}
	payload, err := json.Marshal(map[string]any{
		"id":     "reorder_herdr_workspaces",
		"method": "workspace.move_block",
		"params": map[string]any{
			"workspace_ids":       ids,
			"before_workspace_id": beforeID,
		},
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
	if deadline, ok := ctx.Deadline(); ok {
		if err := conn.SetDeadline(deadline); err != nil {
			return fmt.Errorf("herdr socket: %w", err)
		}
	}
	if _, err := conn.Write(append(payload, '\n')); err != nil {
		return fmt.Errorf("herdr workspace.move_block: %w", err)
	}
	line, err := bufio.NewReader(conn).ReadBytes('\n')
	if err != nil {
		return fmt.Errorf("herdr workspace.move_block: %w", err)
	}
	var resp struct {
		Error *struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(line, &resp); err != nil {
		return fmt.Errorf("herdr workspace.move_block: %w", err)
	}
	if resp.Error != nil {
		return fmt.Errorf("herdr %w: %s: %s", errMoveBlock, resp.Error.Code, resp.Error.Message)
	}
	return nil
}
