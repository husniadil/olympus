package herdr

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/husniadil/olympus/backend"
)

// A snapshot is the part of `herdr api snapshot` Olympus reads: the server's
// whole hierarchy in one request, which is what every listing and every target
// resolution consumes (§3.6).
//
// One request rather than three: `workspace list`, `tab list` and `pane list`
// each answer one level, and a resolution that walked them would read three
// moments of a server that changes between them. The snapshot is one moment,
// and it is also the only listing that reports which pane each tab is showing —
// the `layouts` rows — which is what turns a workspace target into the pane a
// verb acts on.
type snapshot struct {
	FocusedWorkspaceID string         `json:"focused_workspace_id"`
	FocusedTabID       string         `json:"focused_tab_id"`
	FocusedPaneID      string         `json:"focused_pane_id"`
	Workspaces         []workspaceRow `json:"workspaces"`
	Tabs               []tabRow       `json:"tabs"`
	Panes              []paneRow      `json:"panes"`
	Layouts            []layoutRow    `json:"layouts"`
}

// A workspaceRow is the part of herdr's workspace shape Olympus reads. A
// workspace is an Olympus session (§3.6).
type workspaceRow struct {
	WorkspaceID string `json:"workspace_id"`
	Label       string `json:"label"`
	Number      int    `json:"number"`
	ActiveTabID string `json:"active_tab_id"`
	Focused     bool   `json:"focused"`
	// Tokens is the display-only metadata a workspace carries, keyed by token
	// name, which is where a session's status lives (§13.1).
	Tokens map[string]string `json:"tokens"`
}

// A tabRow is the part of herdr's tab shape Olympus reads. A tab is an Olympus
// window, and its number is the window index (§3.6).
type tabRow struct {
	TabID       string `json:"tab_id"`
	WorkspaceID string `json:"workspace_id"`
	Label       string `json:"label"`
	Number      int    `json:"number"`
	Focused     bool   `json:"focused"`
}

// A paneRow is the part of herdr's pane shape Olympus reads.
type paneRow struct {
	PaneID string `json:"pane_id"`
	// Label is the pane's own name, set by `pane rename`; absent until then.
	Label       string `json:"label"`
	TerminalID  string `json:"terminal_id"`
	TabID       string `json:"tab_id"`
	WorkspaceID string `json:"workspace_id"`
	CWD         string `json:"cwd"`
	// ForegroundCWD tracks the foreground process rather than the pane's
	// spawn directory, which is what makes current_path live here (§3.4).
	ForegroundCWD string            `json:"foreground_cwd"`
	Tokens        map[string]string `json:"tokens"`
	Scroll        *struct {
		OffsetFromBottom int `json:"offset_from_bottom"`
		ViewportRows     int `json:"viewport_rows"`
	} `json:"scroll"`
}

// A layoutRow is one tab's layout, of which Olympus reads the pane the tab is
// showing. It is the only place the snapshot says which pane a tab has focused,
// and a pane row's own `focused` flag is not a substitute: measured, after
// focusing a second tab the first tab's pane still reported focused, so the
// flag is focus WITHIN the tab, and only the layout row ties a tab to it.
type layoutRow struct {
	TabID         string `json:"tab_id"`
	WorkspaceID   string `json:"workspace_id"`
	FocusedPaneID string `json:"focused_pane_id"`
	Zoomed        bool   `json:"zoomed"`
}

// snapshot reads the server's hierarchy. No server running is an empty
// snapshot, not an error (§3.3).
func (h *Herdr) snapshot(ctx context.Context) (snapshot, error) {
	out, err := h.run(ctx, "api", "snapshot")
	if err != nil {
		if errors.Is(err, errNoServer) {
			// There is nothing to find; nothing went wrong asking (§3.3).
			return snapshot{}, nil
		}
		return snapshot{}, err
	}
	return parseSnapshot(out)
}

// parseSnapshot reads the hierarchy out of `herdr api snapshot`.
func parseSnapshot(out string) (snapshot, error) {
	var reply struct {
		Result struct {
			Snapshot snapshot `json:"snapshot"`
		} `json:"result"`
	}
	if err := json.Unmarshal([]byte(out), &reply); err != nil {
		return snapshot{}, backend.Wrapf(backend.CodeUnexpected, err, "reading the herdr snapshot")
	}
	return reply.Result.Snapshot, nil
}

// A targetKind is which level of the hierarchy a target names (§3.6).
type targetKind int

const (
	// kindWorkspace is a workspace, by id ("w5") or by label.
	kindWorkspace targetKind = iota
	// kindTab is a tab, by id ("w5:t2").
	kindTab
	// kindPane is a pane, by id ("w5:p3").
	kindPane
)

func (k targetKind) String() string {
	switch k {
	case kindTab:
		return "tab"
	case kindPane:
		return "pane"
	}
	return "workspace"
}

// classifyTarget reads which level a target addresses from its spelling alone. A
// target spelled like none of the ids is a workspace label — the one spelling a
// caller chooses, and the one creation keeps out of the id shapes (§10).
func classifyTarget(target string) targetKind {
	switch {
	case backend.IndexedPaneID(target):
		return kindPane
	case backend.IndexedTabID(target):
		return kindTab
	}
	return kindWorkspace
}

// A resolved target is the workspace a target belongs to, the tab within it,
// and the pane a verb acts on — the target's own pane for a pane, the focused
// pane of the tab for a tab, and the focused pane of the active tab for a
// workspace (§3.6).
type resolved struct {
	kind      targetKind
	workspace workspaceRow
	tab       tabRow
	// pane is the zero paneRow when the level has no pane to act on, which a
	// snapshot mid-teardown can report. Verbs that need one check PaneID.
	pane paneRow
	// zoomed is whether the tab is showing `pane` alone (§8.10): a zoom left
	// by an earlier pane attach, which a workspace or tab attach undoes.
	zoomed bool
}

// id is the exact id of the level the target addressed: the one spelling that
// resolves to this row and no other.
func (r resolved) id() string {
	switch r.kind {
	case kindPane:
		return r.pane.PaneID
	case kindTab:
		return r.tab.TabID
	}
	return r.workspace.WorkspaceID
}

// resolve turns a target into the rows every herdr verb addresses (§3.6, §10).
//
// A workspace label is not unique — herdr will let two workspaces carry the
// same one, and workspaces this backend did not create are not held to the
// uniqueness Create enforces (§2.1). The lowest-numbered match wins, so the
// answer is at least stable across calls rather than following whatever order
// the server listed them in. An id matches at most one row, so the tie-break
// never applies there.
func (s snapshot) resolve(target string) (resolved, error) {
	if target == "" {
		return resolved{}, backend.Errorf(backend.CodeUsage, "no target given")
	}
	switch classifyTarget(target) {
	case kindPane:
		for _, pane := range s.Panes {
			if pane.PaneID != target {
				continue
			}
			ws, _ := s.workspaceByID(pane.WorkspaceID)
			tab, _ := s.tabByID(pane.TabID)
			return resolved{kind: kindPane, workspace: ws, tab: tab, pane: pane, zoomed: s.zoomedOf(pane.TabID)}, nil
		}
	case kindTab:
		if tab, ok := s.tabByID(target); ok {
			ws, _ := s.workspaceByID(tab.WorkspaceID)
			return resolved{kind: kindTab, workspace: ws, tab: tab, pane: s.focusedPaneOf(tab.TabID), zoomed: s.zoomedOf(tab.TabID)}, nil
		}
	default:
		var found *workspaceRow
		for i := range s.Workspaces {
			ws := &s.Workspaces[i]
			if ws.WorkspaceID == target {
				// An exact id is unambiguous and beats any label match.
				found = ws
				break
			}
			if ws.Label != "" && ws.Label == target && (found == nil || ws.Number < found.Number) {
				found = ws
			}
		}
		if found != nil {
			tab, _ := s.tabByID(found.ActiveTabID)
			return resolved{kind: kindWorkspace, workspace: *found, tab: tab, pane: s.focusedPaneOf(found.ActiveTabID), zoomed: s.zoomedOf(found.ActiveTabID)}, nil
		}
	}
	return resolved{}, backend.Errorf(backend.CodeSessionNotFound, "no session %s", target)
}

func (s snapshot) workspaceByID(id string) (workspaceRow, bool) {
	for _, ws := range s.Workspaces {
		if ws.WorkspaceID == id {
			return ws, true
		}
	}
	return workspaceRow{}, false
}

func (s snapshot) tabByID(id string) (tabRow, bool) {
	for _, tab := range s.Tabs {
		if tab.TabID == id {
			return tab, true
		}
	}
	return tabRow{}, false
}

func (s snapshot) paneByID(id string) (paneRow, bool) {
	for _, pane := range s.Panes {
		if pane.PaneID == id {
			return pane, true
		}
	}
	return paneRow{}, false
}

// focusedPaneOf is the pane a tab is showing, from its layout row. A tab with
// no layout — or a layout naming a pane the snapshot no longer lists — has no
// pane to act on, and the zero row says so.
func (s snapshot) focusedPaneOf(tabID string) paneRow {
	for _, layout := range s.Layouts {
		if layout.TabID != tabID {
			continue
		}
		pane, _ := s.paneByID(layout.FocusedPaneID)
		return pane
	}
	return paneRow{}
}

// zoomedOf is whether a tab's layout is zoomed onto one pane, read from the
// same layout row that names the tab's focused pane.
func (s snapshot) zoomedOf(tabID string) bool {
	for _, layout := range s.Layouts {
		if layout.TabID == tabID {
			return layout.Zoomed
		}
	}
	return false
}

// displayName is the name a workspace answers to: its label when it carries
// one, because that is what a human or another tool chose to call it; its id
// otherwise, because a session has to have a name and inventing one would be a
// name nothing else in the system knows (§3.6).
func displayName(ws workspaceRow) string {
	if ws.Label != "" {
		return ws.Label
	}
	return ws.WorkspaceID
}

// contains reports whether a pane belongs to the resolved target: the target's
// own pane for a pane, any pane of the tab for a tab, any pane of the workspace
// for a workspace.
func (r resolved) contains(pane paneRow) bool {
	switch r.kind {
	case kindPane:
		return pane.PaneID == r.pane.PaneID
	case kindTab:
		return pane.TabID == r.tab.TabID
	}
	return pane.WorkspaceID == r.workspace.WorkspaceID
}
