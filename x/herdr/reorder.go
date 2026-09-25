package herdr

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/lewtec/lewkit/x/dotfiles"
	"github.com/lewtec/lewkit/x/git"
	"github.com/lewtec/lewkit/x/taskgroup"
)

var (
	errNoWorkspace = errors.New("no workspace")
	errWorktree    = errors.New("worktree failed")
	errNoRepo      = errors.New("no git repo")
	errAmbiguous   = errors.New("repo matches more than one checkout")
)

// Options selects the checkout that sorts first and the REPO:BRANCH rows to ensure.
// An empty Pin uses the dotfiles root. Home defaults to the user home directory.
// Status, when set, receives the phase name for a taskgroup progress view.
type Options struct {
	Pin    string
	Specs  []RepoBranch
	Home   string
	Client *Client
	Git    *git.Git
	Status *taskgroup.Status
}

// Report is the workspace order after a reorder pass.
type Report struct {
	Pin   string
	Order []Space
}

// Space is one workspace after git and layout classification.
type Space struct {
	ID       string
	Label    string
	Number   int
	Identity string
	Checkout string
	RepoRoot string
	Linked   bool
	Source   string
	RepoName string
	Branch   string
}

// Reorder ensures each spec, then nests, parks, relabels, and sorts workspaces.
func Reorder(ctx context.Context, opts Options) (Report, error) {
	home := opts.Home
	if home == "" {
		var err error
		home, err = os.UserHomeDir()
		if err != nil {
			return Report{}, err
		}
	}
	client := opts.Client
	if client == nil {
		client = &Client{}
	}
	g := opts.Git
	if g == nil {
		g = &git.Git{}
	}
	pin := opts.Pin
	if pin == "" {
		var err error
		pin, err = dotfiles.Root(home)
		if err != nil {
			return Report{}, err
		}
	}
	if info, ok := g.Info(ctx, pin); ok {
		pin = info.Toplevel
	} else {
		pin = git.Resolve(pin)
	}
	p := &plan{
		ctx:         ctx,
		client:      client,
		git:         g,
		pin:         pin,
		home:        home,
		grok:        filepath.Join(home, ".grok", "worktrees"),
		roots:       []string{filepath.Join(home, ".grok", "worktrees"), filepath.Join(home, ".herdr", "worktrees")},
		sessionFile: filepath.Join(home, ".config", "herdr", "session.json"),
		status:      opts.Status,
	}
	if err := p.see(); err != nil {
		return Report{}, err
	}
	for _, spec := range opts.Specs {
		if err := p.ensure(spec); err != nil {
			return Report{}, err
		}
	}
	if len(opts.Specs) > 0 {
		g.Clear()
		if err := p.see(); err != nil {
			return Report{}, err
		}
	}
	if err := p.towards(); err != nil {
		return Report{}, err
	}
	return Report{Pin: p.pin, Order: p.order()}, nil
}

type plan struct {
	ctx         context.Context
	client      *Client
	git         *git.Git
	pin         string
	home        string
	grok        string
	roots       []string
	sessionFile string
	status      *taskgroup.Status
	spaces      []Space
	raw         []Workspace
}

func (p *plan) phase(name string) {
	if p.status != nil {
		p.status.Update(name)
	}
}

func (p *plan) printf(code, format string, args ...any) {
	text := strings.TrimPrefix(fmt.Sprintf(format, args...), "+ ")
	switch code {
	case "33":
		slog.WarnContext(p.ctx, text)
	case "2":
		slog.DebugContext(p.ctx, text)
	default:
		slog.InfoContext(p.ctx, text)
	}
}

func (p *plan) see() error {
	p.phase("perceive")
	p.printf("36", "+ herdr workspace list")
	workspaces, err := p.client.Workspaces(p.ctx)
	if err != nil {
		return err
	}
	p.printf("2", "  %d workspaces", len(workspaces))
	p.printf("36", "+ herdr tab list")
	tabs, err := p.client.Tabs(p.ctx)
	if err != nil {
		return err
	}
	p.printf("2", "  %d tabs", len(tabs))
	p.printf("36", "+ herdr pane list")
	panes, err := p.client.Panes(p.ctx)
	if err != nil {
		return err
	}
	p.printf("2", "  %d panes", len(panes))

	frozen := sessionIdentity(p.sessionFile)
	firstTab := map[string]string{}
	for _, tab := range tabs {
		if _, ok := firstTab[tab.WorkspaceID]; !ok {
			firstTab[tab.WorkspaceID] = tab.ID
		}
	}
	cwdByTab := map[string]string{}
	cwdByWS := map[string]string{}
	for _, pane := range panes {
		if pane.Cwd == "" {
			continue
		}
		if _, ok := cwdByTab[pane.TabID]; !ok {
			cwdByTab[pane.TabID] = pane.Cwd
		}
		if _, ok := cwdByWS[pane.WorkspaceID]; !ok {
			cwdByWS[pane.WorkspaceID] = pane.Cwd
		}
	}
	spaces := make([]Space, 0, len(workspaces))
	probed := 0
	for _, ws := range workspaces {
		ident := frozen[ws.ID]
		if ident == "" {
			raw := ""
			if tid := firstTab[ws.ID]; tid != "" {
				raw = cwdByTab[tid]
			}
			if raw == "" {
				raw = cwdByWS[ws.ID]
			}
			if raw != "" {
				ident = git.Resolve(raw)
			}
		}
		space := p.classify(ws, ident)
		herdrCO := ""
		if ws.Worktree != nil && ws.Worktree.Checkout != "" {
			herdrCO = git.Resolve(ws.Worktree.Checkout)
		}
		if herdrCO != "" && space.Checkout != "" && herdrCO != space.Checkout {
			p.printf("33", "  %s: identity %s (herdr bound %s)", ws.Label, space.Checkout, herdrCO)
		}
		if space.Source != "herdr" {
			probed++
			target := ident
			if target == "" {
				target = "-"
			}
			p.printf("36", "+ git -C %s rev-parse --show-toplevel --git-common-dir", target)
			if space.RepoRoot == "" {
				p.printf("33", "  %s: not a git repo", ws.Label)
			} else {
				kind := "main"
				if space.Linked {
					kind = "worktree"
				}
				p.printf("32", "  %s: %s of %s", ws.Label, kind, space.RepoRoot)
			}
		}
		spaces = append(spaces, space)
	}
	p.attachLayout(spaces)
	p.printf("2", "  herdr worktree on %d, git probe on %d", len(spaces)-probed, probed)
	p.spaces = spaces
	p.raw = workspaces
	return nil
}

func (p *plan) classify(ws Workspace, ident string) Space {
	info, ok := p.git.Info(p.ctx, ident)
	var herdrCO string
	if ws.Worktree != nil && ws.Worktree.Checkout != "" {
		herdrCO = git.Resolve(ws.Worktree.Checkout)
	}
	if ok {
		agrees := herdrCO != "" && herdrCO == info.Toplevel
		source := "git"
		if agrees {
			source = "herdr"
		}
		name := filepath.Base(info.Root)
		if ws.Worktree != nil && ws.Worktree.Name != "" {
			name = ws.Worktree.Name
		}
		branch, _ := p.git.Branch(p.ctx, info.Toplevel)
		return Space{
			ID: ws.ID, Label: ws.Label, Number: ws.Number,
			Identity: ident, Checkout: info.Toplevel, RepoRoot: info.Root,
			Linked: info.Linked, Source: source, RepoName: name, Branch: branch,
		}
	}
	if ws.Worktree != nil {
		checkout := herdrCO
		if checkout == "" {
			checkout = git.Resolve(ws.Worktree.Checkout)
		}
		root := git.Resolve(ws.Worktree.Root)
		branch, _ := p.git.Branch(p.ctx, checkout)
		return Space{
			ID: ws.ID, Label: ws.Label, Number: ws.Number,
			Identity: ident, Checkout: checkout, RepoRoot: root,
			Linked: ws.Worktree.Linked, Source: "herdr", RepoName: ws.Worktree.Name, Branch: branch,
		}
	}
	return Space{
		ID: ws.ID, Label: ws.Label, Number: ws.Number,
		Identity: ident, Checkout: ident, Source: "git",
	}
}

func sessionIdentity(path string) map[string]string {
	out := map[string]string{}
	raw, err := os.ReadFile(path)
	if err != nil {
		return out
	}
	var doc struct {
		Workspaces []struct {
			ID  string `json:"id"`
			Cwd string `json:"identity_cwd"`
		} `json:"workspaces"`
	}
	if json.Unmarshal(raw, &doc) != nil {
		return out
	}
	for _, ws := range doc.Workspaces {
		if ws.ID != "" && ws.Cwd != "" {
			out[ws.ID] = git.Resolve(ws.Cwd)
		}
	}
	return out
}

func (p *plan) attachLayout(spaces []Space) {
	var known []nameRoot
	for _, space := range spaces {
		if space.RepoRoot == "" {
			continue
		}
		known = append(known, nameRoot{strings.ToLower(filepath.Base(space.RepoRoot)), space.RepoRoot})
		if space.RepoName != "" {
			known = append(known, nameRoot{strings.ToLower(space.RepoName), space.RepoRoot})
		}
	}
	for i := range spaces {
		space := &spaces[i]
		if space.RepoRoot != "" || space.Checkout == "" {
			continue
		}
		slug := layoutSlug(space.Checkout, p.roots)
		if slug == "" {
			continue
		}
		p.printf("36", "+ layout %s", space.Checkout)
		root := matchSlug(slug, known)
		if root == "" {
			root = p.inferRepoRoot(space.Checkout)
		}
		if root == "" {
			p.printf("33", "  %s: worktree layout %s, no main repo found", space.Label, slug)
			continue
		}
		space.Linked = true
		space.Source = "layout"
		space.RepoRoot = root
		space.RepoName = filepath.Base(root)
		p.printf("35", "  %s: worktree of %s", space.Label, root)
	}
}

type nameRoot struct {
	name string
	root string
}

func layoutSlug(path string, roots []string) string {
	resolved := git.Resolve(path)
	for _, root := range roots {
		rel, err := filepath.Rel(git.Resolve(root), resolved)
		if err != nil || rel == "." || strings.HasPrefix(rel, "..") {
			continue
		}
		parts := strings.Split(rel, string(filepath.Separator))
		if len(parts) >= 2 {
			return parts[0]
		}
	}
	return ""
}

func matchSlug(slug string, known []nameRoot) string {
	slug = strings.ToLower(slug)
	bestName, bestRoot := "", ""
	for _, item := range known {
		if slug == item.name || strings.HasSuffix(slug, "-"+item.name) {
			if len(item.name) > len(bestName) {
				bestName, bestRoot = item.name, item.root
			}
		}
	}
	return bestRoot
}

func (p *plan) inferRepoRoot(checkout string) string {
	parent := filepath.Dir(checkout)
	entries, err := os.ReadDir(parent)
	if err != nil {
		return ""
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			names = append(names, entry.Name())
		}
	}
	slices.Sort(names)
	base := filepath.Base(checkout)
	for _, name := range names {
		if name == base {
			continue
		}
		if info, ok := p.git.Info(p.ctx, filepath.Join(parent, name)); ok {
			return info.Root
		}
	}
	return ""
}

func (p *plan) towards() error {
	for range 6 {
		nest := p.nest()
		park := p.park()
		relabel := p.relabel()
		if nest || park || relabel {
			p.git.Clear()
			if err := p.see(); err != nil {
				return err
			}
			continue
		}
		break
	}
	wanted := p.order()
	slog.InfoContext(p.ctx, "workspace order", "count", len(wanted))
	return p.sort(wanted)
}

func (p *plan) byID() map[string]Space {
	out := make(map[string]Space, len(p.spaces))
	for _, space := range p.spaces {
		out[space.ID] = space
	}
	return out
}

func (p *plan) openMain(root string, group []Space) (string, bool) {
	if _, ok := p.git.Info(p.ctx, root); !ok {
		p.printf("33", "  no main checkout at %s", root)
		return "", false
	}
	label := filepath.Base(root)
	for _, space := range group {
		if space.RepoName != "" {
			label = space.RepoName
			break
		}
	}
	p.printf("36", "+ herdr worktree open --cwd %s --path %s --label %s --no-focus", root, root, label)
	opened, err := p.client.OpenWorktree(p.ctx, OpenWorktree{Path: root, Label: label, Cwd: root})
	if err == nil {
		parent := opened.Workspace.ID
		extra := ""
		if opened.AlreadyOpen {
			extra = ", already open"
		}
		existing, known := p.byID()[parent]
		if !known || (!existing.Linked && existing.Checkout == root) {
			p.printf("32", "  opened main %s (%s%s)", label, parent, extra)
			return parent, !opened.AlreadyOpen
		}
		p.printf("33", "  %s: herdr main is worktree identity %s", label, existing.Checkout)
	} else {
		p.printf("33", "  herdr worktree open: %s", err.Error())
	}
	p.printf("36", "+ herdr workspace create --cwd %s --label %s --no-focus", root, label)
	created, err := p.client.CreateWorkspace(p.ctx, root, label)
	if err != nil {
		p.printf("33", "  herdr workspace create: %s", err.Error())
		return "", false
	}
	p.printf("32", "  created main %s (%s)", label, created.ID)
	return created.ID, true
}

func (p *plan) nest() bool {
	p.phase("nest")
	groups := map[string][]Space{}
	for _, space := range p.spaces {
		if space.RepoRoot != "" {
			groups[space.RepoRoot] = append(groups[space.RepoRoot], space)
		}
	}
	acted := false
	for _, root := range slices.Sorted(maps.Keys(groups)) {
		group := groups[root]
		var linked, mains []Space
		for _, space := range group {
			if space.Linked {
				linked = append(linked, space)
				continue
			}
			if space.Checkout == space.RepoRoot {
				mains = append(mains, space)
			}
		}
		if len(linked) == 0 {
			continue
		}
		parent := ""
		if len(mains) > 0 {
			parent = mains[0].ID
		} else {
			var created bool
			parent, created = p.openMain(root, group)
			if parent == "" {
				continue
			}
			if created {
				acted = true
			}
		}
		for _, space := range linked {
			if space.Source == "herdr" || space.Checkout == "" {
				continue
			}
			if space.ID == parent {
				p.printf("33", "  %s: identity is worktree, herdr still bound it as main", space.Label)
				continue
			}
			_, broken := p.git.Info(p.ctx, space.Checkout)
			broken = !broken
			if !p.relink(space.Checkout, root) {
				continue
			}
			label := space.Label
			if branch, ok := p.git.Branch(p.ctx, space.Checkout); ok {
				label = branch
			}
			p.printf("36", "+ herdr worktree open --workspace %s --path %s --label %s --no-focus", parent, space.Checkout, label)
			result, err := p.client.OpenWorktree(p.ctx, OpenWorktree{Path: space.Checkout, Label: label, Workspace: parent})
			if err != nil {
				p.printf("33", "  herdr worktree open: %s", err.Error())
				continue
			}
			extra := ""
			if result.AlreadyOpen {
				extra = ", already open"
			}
			id := result.Workspace.ID
			if id == "" {
				id = "?"
			}
			p.printf("35", "  nested %s under %s (%s%s)", label, filepath.Base(root), id, extra)
			if broken || !result.AlreadyOpen {
				acted = true
			}
		}
	}
	return acted
}

func (p *plan) park() bool {
	p.phase("park")
	acted := false
	seen := map[string]struct{}{}
	for _, space := range p.spaces {
		if space.Linked || space.RepoRoot == "" {
			continue
		}
		if _, ok := seen[space.RepoRoot]; ok {
			continue
		}
		seen[space.RepoRoot] = struct{}{}
		if p.parkOne(space) {
			acted = true
		}
	}
	return acted
}

func (p *plan) parkOne(space Space) bool {
	root := space.RepoRoot
	branch, ok := p.git.Branch(p.ctx, root)
	if !ok {
		p.printf("33", "  %s: detached HEAD, skip park", space.Label)
		return false
	}
	if branch == "main" || branch == "master" {
		return false
	}
	def, ok := p.git.DefaultBranch(p.ctx, root)
	if !ok {
		p.printf("33", "  %s: no main/master, skip park", space.Label)
		return false
	}
	wt, have := p.git.LinkedWorktree(p.ctx, root, branch)
	dirty := p.git.Dirty(p.ctx, root)
	msg := "reorder_herdr_workspaces:" + branch
	if dirty {
		p.printf("36", "+ git -C %s stash push -u -m %s", root, msg)
		if err := p.git.Run(p.ctx, root, "stash", "push", "-u", "-m", msg); err != nil {
			slog.WarnContext(p.ctx, "git", "err", err)
			p.printf("33", "  %s: stash failed, skip park", space.Label)
			return false
		}
	}
	if !have {
		wt = filepath.Join(p.grok, p.git.GrokSlug(p.ctx, root, p.grok), strings.ReplaceAll(branch, "/", "-"))
		if err := os.MkdirAll(filepath.Dir(wt), 0o755); err != nil {
			p.printf("33", "  %s: %s", space.Label, err.Error())
			if dirty {
				_ = p.git.Run(p.ctx, root, "stash", "pop")
			}
			return false
		}
		p.printf("36", "+ git -C %s worktree add -f %s %s", root, wt, branch)
		if err := p.git.Run(p.ctx, root, "worktree", "add", "-f", wt, branch); err != nil {
			slog.WarnContext(p.ctx, "git", "err", err)
			p.git.Clear()
			if dirty {
				_ = p.git.Run(p.ctx, root, "stash", "pop")
			}
			p.printf("33", "  %s: worktree add failed", space.Label)
			return false
		}
		p.git.Clear()
	}
	p.printf("36", "+ herdr worktree open --workspace %s --path %s --label %s --no-focus", space.ID, wt, branch)
	result, err := p.client.OpenWorktree(p.ctx, OpenWorktree{Path: wt, Label: branch, Workspace: space.ID})
	wtWS := ""
	if err != nil {
		p.printf("33", "  herdr worktree open: %s", err.Error())
	} else {
		wtWS = result.Workspace.ID
		extra := ""
		if result.AlreadyOpen {
			extra = ", already open"
		}
		p.printf("35", "  parked %s under %s (%s%s)", branch, filepath.Base(root), wtWS, extra)
	}
	p.printf("36", "+ git -C %s checkout %s", root, def)
	if err := p.git.Run(p.ctx, root, "checkout", def); err != nil {
		slog.WarnContext(p.ctx, "git", "err", err)
		p.git.Clear()
		p.printf("33", "  %s: checkout %s failed", space.Label, def)
		if dirty {
			_ = p.git.Run(p.ctx, root, "stash", "pop")
		}
		return wtWS != ""
	}
	p.git.Clear()
	p.printf("32", "  %s: %s -> %s", space.Label, branch, def)
	if dirty {
		p.printf("36", "+ git -C %s stash pop", wt)
		if err := p.git.Run(p.ctx, wt, "stash", "pop"); err != nil {
			slog.WarnContext(p.ctx, "git", "err", err)
			p.printf("33", "  stash pop in %s failed; stash kept", wt)
		} else {
			p.printf("32", "  restored dirty work in %s", branch)
		}
	}
	return true
}

func (p *plan) relabel() bool {
	p.phase("rename")
	acted := false
	for i := range p.spaces {
		space := &p.spaces[i]
		if !space.Linked || space.Checkout == "" {
			continue
		}
		branch, ok := p.git.Branch(p.ctx, space.Checkout)
		if !ok || branch == space.Label {
			continue
		}
		p.printf("36", "+ herdr workspace rename %s %s", space.ID, branch)
		if err := p.client.RenameWorkspace(p.ctx, space.ID, branch); err != nil {
			p.printf("33", "  herdr workspace rename: %s", err.Error())
			continue
		}
		p.printf("32", "  %s -> %s", space.Label, branch)
		space.Label = branch
		acted = true
	}
	return acted
}

func (p *plan) order() []Space {
	var pinMain, pinWT, nongit []Space
	groups := map[string][]Space{}
	for _, space := range p.spaces {
		switch {
		case space.Checkout != "" && space.Checkout == p.pin:
			pinMain = append(pinMain, space)
		case space.RepoRoot != "" && space.RepoRoot == p.pin:
			pinWT = append(pinWT, space)
		case space.RepoRoot != "":
			groups[space.RepoRoot] = append(groups[space.RepoRoot], space)
		default:
			nongit = append(nongit, space)
		}
	}
	key := func(a, b Space) int {
		if c := strings.Compare(a.Checkout, b.Checkout); c != 0 {
			return c
		}
		if c := strings.Compare(a.Label, b.Label); c != 0 {
			return c
		}
		return strings.Compare(a.ID, b.ID)
	}
	slices.SortFunc(pinMain, key)
	slices.SortFunc(pinWT, key)
	out := append(append([]Space{}, pinMain...), pinWT...)
	for _, root := range slices.Sorted(maps.Keys(groups)) {
		var mains, wts []Space
		for _, space := range groups[root] {
			if space.Linked {
				wts = append(wts, space)
			} else {
				mains = append(mains, space)
			}
		}
		slices.SortFunc(mains, key)
		slices.SortFunc(wts, key)
		out = append(out, mains...)
		out = append(out, wts...)
	}
	slices.SortFunc(nongit, key)
	return append(out, nongit...)
}

func (p *plan) criterion(space Space) string {
	switch {
	case space.Checkout != "" && space.Checkout == p.pin:
		return "this checkout first; on main/master"
	case space.RepoRoot != "" && space.RepoRoot == p.pin:
		return "worktree nested under this checkout; label is branch"
	case space.Linked && space.RepoRoot != "":
		return fmt.Sprintf("worktree nested under %s; label is branch", space.RepoRoot)
	case space.RepoRoot != "":
		return fmt.Sprintf("main checkout %s on main/master", space.RepoRoot)
	default:
		return "not a git repo, last"
	}
}

// Kind is repo, worktree, or dir.
func Kind(space Space) string {
	if space.Linked {
		return "worktree"
	}
	if space.RepoRoot != "" {
		return "repo"
	}
	return "dir"
}

// Nodes is the order as a progress tree. A linked worktree hangs under the
// main checkout of the same repo. The workspace row is the label, then the
// checkout path. A main lists repo when it differs from the checkout.
// Identity is listed when it differs from the checkout.
func (r Report) Nodes() []taskgroup.Node {
	mainByRoot := map[string]string{}
	for _, space := range r.Order {
		if space.Linked || space.RepoRoot == "" || space.Checkout != space.RepoRoot {
			continue
		}
		if _, ok := mainByRoot[space.RepoRoot]; !ok {
			mainByRoot[space.RepoRoot] = space.ID
		}
	}
	parentOf := map[string]string{}
	children := map[string][]Space{}
	for _, space := range r.Order {
		if !space.Linked {
			continue
		}
		parent, ok := mainByRoot[space.RepoRoot]
		if !ok || parent == space.ID {
			continue
		}
		parentOf[space.ID] = parent
		children[parent] = append(children[parent], space)
	}
	var nodes []taskgroup.Node
	var seq taskgroup.ID
	next := func() taskgroup.ID {
		seq++
		return seq
	}
	var add func(space Space, parent taskgroup.ID)
	add = func(space Space, parent taskgroup.ID) {
		id := next()
		pool := taskgroup.IO
		emoji := ""
		if space.RepoRoot == "" {
			emoji = "📁"
		} else if space.Linked {
			pool = taskgroup.CPU
		}
		nodes = append(nodes, taskgroup.Node{
			ID:      id,
			Parent:  parent,
			Name:    space.Label,
			Message: space.Checkout,
			Emoji:   emoji,
			State:   taskgroup.Done,
			Pool:    pool,
		})
		detail := func(name, value string) {
			if value == "" {
				return
			}
			nodes = append(nodes, taskgroup.Node{
				ID:      next(),
				Parent:  id,
				Name:    name,
				Message: value,
				State:   taskgroup.Done,
				Pool:    taskgroup.Control,
			})
		}
		if !space.Linked && space.RepoRoot != space.Checkout {
			detail("repo", space.RepoRoot)
		}
		if space.Identity != "" && space.Identity != space.Checkout {
			detail("identity", space.Identity)
		}
		for _, child := range children[space.ID] {
			add(child, id)
		}
	}
	for _, space := range r.Order {
		if _, nested := parentOf[space.ID]; nested {
			continue
		}
		add(space, 0)
	}
	return nodes
}

func (p *plan) sort(wanted []Space) error {
	p.phase("order")
	current := make([]string, len(p.raw))
	for i, ws := range p.raw {
		current[i] = ws.ID
	}
	wantedIDs := make([]string, len(wanted))
	for i, space := range wanted {
		wantedIDs[i] = space.ID
	}
	if slices.Equal(current, wantedIDs) {
		p.printf("32", "already in order, no move")
		return nil
	}
	if len(wantedIDs) <= 1 {
		return nil
	}
	anchor := wanted[len(wanted)-1]
	p.printf("36", "+ workspace.move_block %d ids before %s (%s)", len(wantedIDs)-1, anchor.Label, anchor.ID)
	if err := p.client.MoveBlock(p.ctx, wantedIDs[:len(wantedIDs)-1], wantedIDs[len(wantedIDs)-1]); err != nil {
		return err
	}
	p.printf("32", "ok")
	return nil
}

func (p *plan) parentWorkspace(root string) (string, bool) {
	var group []Space
	for _, space := range p.spaces {
		if space.RepoRoot == root {
			group = append(group, space)
		}
	}
	for _, space := range group {
		if !space.Linked && space.Checkout == root {
			p.printf("32", "  main %s (%s)", filepath.Base(root), space.ID)
			return space.ID, true
		}
	}
	parent, created := p.openMain(root, group)
	if parent == "" {
		return "", created
	}
	return parent, true
}

func (p *plan) ensure(spec RepoBranch) error {
	p.phase("ensure")
	root, err := p.resolveRepo(spec.Repo)
	if err != nil {
		return err
	}
	p.printf("36", "+ ensure %s:%s", filepath.Base(root), spec.Branch)
	parent, ok := p.parentWorkspace(root)
	if !ok {
		return fmt.Errorf("%w for %s", errNoWorkspace, root)
	}
	if !p.ensureWorktree(root, spec.Branch, parent) {
		return fmt.Errorf("%w for %s:%s", errWorktree, spec.Repo, spec.Branch)
	}
	return nil
}

func (p *plan) resolveRepo(token string) (string, error) {
	var roots []string
	add := func(root string) {
		if root == "" || slices.Contains(roots, root) {
			return
		}
		roots = append(roots, root)
	}
	for _, path := range p.candidatePaths(token) {
		if info, ok := p.git.Info(p.ctx, path); ok {
			add(info.Root)
		}
	}
	if len(roots) == 0 {
		for _, space := range p.spaces {
			if space.RepoRoot == "" {
				continue
			}
			if token == filepath.Base(space.RepoRoot) || token == space.RepoName {
				add(space.RepoRoot)
			}
		}
	}
	switch len(roots) {
	case 1:
		return roots[0], nil
	case 0:
		return "", fmt.Errorf("%w for %q", errNoRepo, token)
	default:
		return "", fmt.Errorf("%w: %q matches %s", errAmbiguous, token, strings.Join(roots, ", "))
	}
}

func (p *plan) candidatePaths(token string) []string {
	raw := expandUser(token, p.home)
	var found []string
	if _, err := os.Stat(raw); err == nil {
		return []string{raw}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil
	}
	homeToken := filepath.Join(p.home, token)
	if st, err := os.Stat(homeToken); err == nil && st.IsDir() {
		found = append(found, homeToken)
	}
	workspace := filepath.Join(p.home, "WORKSPACE")
	for _, pattern := range []string{filepath.Join(workspace, "*", token), filepath.Join(workspace, "*", "*", token)} {
		matches, err := filepath.Glob(pattern)
		if err != nil {
			continue
		}
		for _, match := range matches {
			if st, err := os.Stat(match); err == nil && st.IsDir() {
				found = append(found, match)
			}
		}
	}
	return found
}

func expandUser(token, home string) string {
	if token == "~" {
		return home
	}
	if strings.HasPrefix(token, "~/") {
		return filepath.Join(home, token[2:])
	}
	return token
}

func (p *plan) ensureWorktree(root, branch, parent string) bool {
	if existing, ok := p.git.LinkedWorktree(p.ctx, root, branch); ok {
		p.printf("36", "+ herdr worktree open --workspace %s --path %s --label %s --no-focus", parent, existing, branch)
		if _, err := p.client.OpenWorktree(p.ctx, OpenWorktree{Path: existing, Label: branch, Workspace: parent}); err != nil {
			p.printf("33", "  herdr worktree open: %s", err.Error())
			return false
		}
		return true
	}
	head, _ := p.git.Branch(p.ctx, root)
	if head == branch && (branch == "main" || branch == "master") {
		p.printf("32", "  %s already on %s", filepath.Base(root), branch)
		return true
	}
	if head == branch {
		space, ok := p.byID()[parent]
		if !ok {
			space = Space{
				ID: parent, Label: filepath.Base(root), Identity: root, Checkout: root,
				RepoRoot: root, Source: "git", RepoName: filepath.Base(root), Branch: head,
			}
		}
		return p.parkOne(space)
	}
	path := filepath.Join(p.grok, p.git.GrokSlug(p.ctx, root, p.grok), strings.ReplaceAll(branch, "/", "-"))
	if !p.addWorktree(root, branch, path) {
		return false
	}
	p.printf("36", "+ herdr worktree open --workspace %s --path %s --label %s --no-focus", parent, path, branch)
	if _, err := p.client.OpenWorktree(p.ctx, OpenWorktree{Path: path, Label: branch, Workspace: parent}); err != nil {
		p.printf("33", "  herdr worktree open: %s", err.Error())
		return false
	}
	return true
}

func (p *plan) addWorktree(root, branch, path string) bool {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		p.printf("33", "  %s", err.Error())
		return false
	}
	var args []string
	if p.ensureBranch(root, branch) {
		args = []string{"worktree", "add", path, branch}
	} else {
		base, ok := p.git.DefaultBranch(p.ctx, root)
		if !ok {
			p.printf("33", "  %s: no branch and no main/master in %s", branch, root)
			return false
		}
		args = []string{"worktree", "add", "-b", branch, path, base}
	}
	p.printf("36", "+ git -C %s %s", root, strings.Join(args, " "))
	if err := p.git.Run(p.ctx, root, args...); err != nil {
		slog.WarnContext(p.ctx, "git", "err", err)
		p.git.Clear()
		p.printf("33", "  worktree add failed for %s", branch)
		return false
	}
	p.git.Clear()
	return true
}

func (p *plan) ensureBranch(repo, name string) bool {
	if p.git.HasRef(p.ctx, repo, "refs/heads/"+name) {
		return true
	}
	if !p.git.HasRef(p.ctx, repo, "refs/remotes/origin/"+name) {
		return false
	}
	p.printf("36", "+ git -C %s branch --track %s origin/%s", repo, name, name)
	if err := p.git.Run(p.ctx, repo, "branch", "--track", name, "origin/"+name); err != nil {
		slog.WarnContext(p.ctx, "git", "err", err)
		return false
	}
	return true
}

func (p *plan) relink(checkout, repoRoot string) bool {
	if _, ok := p.git.Info(p.ctx, checkout); ok {
		return true
	}
	name := filepath.Base(checkout)
	if !p.ensureBranch(repoRoot, name) {
		p.printf("33", "  no branch %s in %s", name, repoRoot)
		return false
	}
	gitdir := filepath.Join(repoRoot, ".git")
	st, err := os.Stat(gitdir)
	if err != nil || !st.IsDir() {
		p.printf("33", "  %s has no .git directory", repoRoot)
		return false
	}
	admin := filepath.Join(gitdir, "worktrees", name)
	p.printf("36", "+ relink %s -> %s", checkout, admin)
	if err := os.MkdirAll(admin, 0o755); err != nil {
		p.printf("33", "  relink failed")
		return false
	}
	files := map[string]string{
		filepath.Join(admin, "commondir"): "../..\n",
		filepath.Join(admin, "gitdir"):    filepath.Join(checkout, ".git") + "\n",
		filepath.Join(admin, "HEAD"):      "ref: refs/heads/" + name + "\n",
	}
	for path, text := range files {
		if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
			p.printf("33", "  relink failed")
			return false
		}
	}
	if err := os.WriteFile(filepath.Join(checkout, ".git"), []byte("gitdir: "+admin+"\n"), 0o644); err != nil {
		p.printf("33", "  relink failed")
		return false
	}
	_ = p.git.Run(p.ctx, repoRoot, "worktree", "repair", checkout)
	p.git.Clear()
	if _, ok := p.git.Info(p.ctx, checkout); !ok {
		p.printf("33", "  relink failed")
		return false
	}
	p.printf("32", "  git worktree restored")
	return true
}
