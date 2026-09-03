package herdr

import (
	"context"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/husniadil/olympus/backend"
)

// fixtureSnapshot is a `herdr api snapshot` reply taken off a live 0.8.2
// server: one workspace labelled "demo" with two tabs, the first split into two
// panes and showing the first, the second holding one pane; a second workspace
// nothing has labelled. Cut down to the fields Olympus reads plus a few it
// does not, so the parser is proved to ignore what it was not told about.
const fixtureSnapshot = `{"id":"cli:api:snapshot","result":{"snapshot":{"agents":[],
"focused_pane_id":"w1:p1","focused_tab_id":"w1:t1","focused_workspace_id":"w1",
"layouts":[
 {"area":{"height":40,"width":120,"x":0,"y":0},"focused_pane_id":"w1:p1","panes":[{"focused":true,"pane_id":"w1:p1"},{"focused":false,"pane_id":"w1:p2"}],"splits":[],"tab_id":"w1:t1","workspace_id":"w1","zoomed":false},
 {"area":{"height":40,"width":120,"x":0,"y":0},"focused_pane_id":"w1:p3","panes":[{"focused":true,"pane_id":"w1:p3"}],"splits":[],"tab_id":"w1:t2","workspace_id":"w1","zoomed":false},
 {"area":{"height":40,"width":120,"x":0,"y":0},"focused_pane_id":"w4:p1","panes":[{"focused":true,"pane_id":"w4:p1"}],"splits":[],"tab_id":"w4:t1","workspace_id":"w4","zoomed":false}],
"panes":[
 {"agent_status":"unknown","cwd":"/Users/husni","focused":true,"foreground_cwd":"/Users/husni/src","label":"demo","pane_id":"w1:p1","revision":2,"scroll":{"max_offset_from_bottom":0,"offset_from_bottom":0,"viewport_rows":40},"tab_id":"w1:t1","terminal_id":"term_65a8c588eca2f1","tokens":{"status":"pane-busy"},"workspace_id":"w1"},
 {"agent_status":"unknown","cwd":"/Users/husni","focused":false,"foreground_cwd":"/Users/husni","pane_id":"w1:p2","revision":0,"scroll":{"max_offset_from_bottom":0,"offset_from_bottom":0,"viewport_rows":40},"tab_id":"w1:t1","terminal_id":"term_65a8c5b5bd6cd2","workspace_id":"w1"},
 {"agent_status":"unknown","cwd":"/Users/husni","focused":false,"foreground_cwd":"/Users/husni","pane_id":"w1:p3","revision":0,"scroll":{"max_offset_from_bottom":0,"offset_from_bottom":0,"viewport_rows":40},"tab_id":"w1:t2","terminal_id":"term_65a8c5b5cee3c3","workspace_id":"w1"},
 {"agent_status":"unknown","cwd":"/tmp","focused":true,"foreground_cwd":"/tmp","pane_id":"w4:p1","revision":0,"scroll":{"max_offset_from_bottom":0,"offset_from_bottom":0,"viewport_rows":40},"tab_id":"w4:t1","terminal_id":"term_65a8c5b5d00000","workspace_id":"w4"}],
"protocol":22,
"tabs":[
 {"agent_status":"unknown","focused":true,"label":"1","number":1,"pane_count":2,"tab_id":"w1:t1","workspace_id":"w1"},
 {"agent_status":"unknown","focused":false,"label":"second","number":2,"pane_count":1,"tab_id":"w1:t2","workspace_id":"w1"},
 {"agent_status":"unknown","focused":true,"label":"1","number":1,"pane_count":1,"tab_id":"w4:t1","workspace_id":"w4"}],
"version":"0.8.2",
"workspaces":[
 {"active_tab_id":"w1:t1","agent_status":"unknown","focused":true,"label":"demo","number":1,"pane_count":3,"tab_count":2,"tokens":{"status":"busy"},"workspace_id":"w1"},
 {"active_tab_id":"w4:t1","agent_status":"unknown","focused":false,"label":"","number":3,"pane_count":1,"tab_count":1,"workspace_id":"w4"}]},
"type":"session_snapshot"}}`

func fixture(t *testing.T) snapshot {
	t.Helper()
	snap, err := parseSnapshot(fixtureSnapshot)
	if err != nil {
		t.Fatalf("parsing the fixture: %v", err)
	}
	return snap
}

// §3.6 A snapshot carries the whole hierarchy: workspaces, tabs, panes and the
// pane each tab is showing. Every level Olympus maps is read from it.
func TestSnapshotParsesEveryLevel(t *testing.T) {
	t.Parallel()
	snap := fixture(t)
	if len(snap.Workspaces) != 2 || len(snap.Tabs) != 3 || len(snap.Panes) != 4 || len(snap.Layouts) != 3 {
		t.Fatalf("snapshot has %d workspaces, %d tabs, %d panes, %d layouts; want 2, 3, 4, 3",
			len(snap.Workspaces), len(snap.Tabs), len(snap.Panes), len(snap.Layouts))
	}
	if got := snap.Workspaces[0].Tokens["status"]; got != "busy" {
		t.Errorf("the workspace's status token is %q, want %q", got, "busy")
	}
	if got := snap.Panes[0].Tokens["status"]; got != "pane-busy" {
		t.Errorf("the pane's status token is %q, want %q", got, "pane-busy")
	}
	if got := snap.focusedPaneOf("w1:t2").PaneID; got != "w1:p3" {
		t.Errorf("the second tab shows %q, want %q", got, "w1:p3")
	}
	if got := snap.focusedPaneOf("w9:t9").PaneID; got != "" {
		t.Errorf("a tab with no layout shows %q, want nothing", got)
	}
	if _, err := parseSnapshot("not json"); backend.CodeOf(err) != backend.CodeUnexpected {
		t.Errorf("an unparseable snapshot is %q, want %q", backend.CodeOf(err), backend.CodeUnexpected)
	}
}

// §3.6 A target's spelling says which level it addresses: a pane id, a tab id,
// and anything else a workspace — by id or by label.
func TestTargetShapesClassify(t *testing.T) {
	t.Parallel()
	for target, want := range map[string]targetKind{
		"w1:p2":   kindPane,
		"w4Y:pA":  kindPane,
		"w1:t2":   kindTab,
		"wA:tA":   kindTab,
		"w1":      kindWorkspace,
		"demo":    kindWorkspace,
		"w1p1":    kindWorkspace,
		"w1:pane": kindWorkspace,
		"work:t1": kindWorkspace,
	} {
		if got := classifyTarget(target); got != want {
			t.Errorf("%q classifies as %s, want %s", target, got, want)
		}
	}
}

// §3.6 Resolution answers, for each shape, the pane a verb acts on: a pane is
// itself, a tab is the pane it shows, a workspace is the pane its active tab
// shows. A workspace is addressed by id or by label; one with no label is
// addressed by its id alone.
func TestResolutionFindsThePaneAVerbActsOn(t *testing.T) {
	t.Parallel()
	snap := fixture(t)
	cases := []struct {
		target    string
		kind      targetKind
		workspace string
		tab       string
		pane      string
	}{
		{"w1", kindWorkspace, "w1", "w1:t1", "w1:p1"},
		{"demo", kindWorkspace, "w1", "w1:t1", "w1:p1"},
		{"w1:t2", kindTab, "w1", "w1:t2", "w1:p3"},
		{"w1:p2", kindPane, "w1", "w1:t1", "w1:p2"},
		{"w1:p3", kindPane, "w1", "w1:t2", "w1:p3"},
		{"w4", kindWorkspace, "w4", "w4:t1", "w4:p1"},
	}
	for _, c := range cases {
		r, err := snap.resolve(c.target)
		if err != nil {
			t.Errorf("resolving %q: %v", c.target, err)
			continue
		}
		if r.kind != c.kind || r.workspace.WorkspaceID != c.workspace || r.tab.TabID != c.tab || r.pane.PaneID != c.pane {
			t.Errorf("%q resolved to %s %s/%s/%s, want %s %s/%s/%s",
				c.target, r.kind, r.workspace.WorkspaceID, r.tab.TabID, r.pane.PaneID,
				c.kind, c.workspace, c.tab, c.pane)
		}
	}
	for _, absent := range []string{"", "w9", "w1:t9", "w1:p9", "nobody", "w4:p2"} {
		_, err := snap.resolve(absent)
		want := backend.CodeSessionNotFound
		if absent == "" {
			want = backend.CodeUsage
		}
		if backend.CodeOf(err) != want {
			t.Errorf("resolving %q is %q, want %q", absent, backend.CodeOf(err), want)
		}
	}
}

// §3.6 A workspace's name is its label, else its id.
func TestAWorkspaceIsNamedByItsLabelElseItsID(t *testing.T) {
	t.Parallel()
	snap := fixture(t)
	if got := displayName(snap.Workspaces[0]); got != "demo" {
		t.Errorf("a labelled workspace is named %q, want its label", got)
	}
	if got := displayName(snap.Workspaces[1]); got != "w4" {
		t.Errorf("an unlabelled workspace is named %q, want its id", got)
	}
}

// §3.6 A target contains its panes: one for a pane, the tab's for a tab, the
// workspace's for a workspace.
func TestATargetContainsItsPanes(t *testing.T) {
	t.Parallel()
	snap := fixture(t)
	for target, want := range map[string][]string{
		"w1":    {"w1:p1", "w1:p2", "w1:p3"},
		"demo":  {"w1:p1", "w1:p2", "w1:p3"},
		"w1:t1": {"w1:p1", "w1:p2"},
		"w1:t2": {"w1:p3"},
		"w1:p2": {"w1:p2"},
		"w4":    {"w4:p1"},
	} {
		r, err := snap.resolve(target)
		if err != nil {
			t.Fatalf("resolving %q: %v", target, err)
		}
		var got []string
		for _, pane := range snap.Panes {
			if r.contains(pane) {
				got = append(got, pane.PaneID)
			}
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%q contains %v, want %v", target, got, want)
		}
	}
}

// §8.10 The session client is steered onto the target level by level: the
// workspace, then the tab within it, then the pane within that.
func TestSteeringFocusesTheTargetLevelByLevel(t *testing.T) {
	t.Parallel()
	snap := fixture(t)
	for target, want := range map[string][][]string{
		"demo":  {{"workspace", "focus", "w1"}},
		"w1:t2": {{"workspace", "focus", "w1"}, {"tab", "focus", "w1:t2"}},
		"w1:p2": {{"workspace", "focus", "w1"}, {"tab", "focus", "w1:t1"}, {"pane", "zoom", "--pane", "w1:p2", "--on"}},
	} {
		r, err := snap.resolve(target)
		if err != nil {
			t.Fatalf("resolving %q: %v", target, err)
		}
		if got := steeringArgs(r); !reflect.DeepEqual(got, want) {
			t.Errorf("steering onto %q runs %v, want %v", target, got, want)
		}
	}
}

// §8.10 A zoom an earlier pane attach left on the tab is undone by a workspace
// or tab attach onto it — the caller asked for the split — and left alone by a
// pane attach, which zooms anyway.
func TestSteeringOntoAZoomedTabZoomsOut(t *testing.T) {
	t.Parallel()
	snap, err := parseSnapshot(strings.Replace(fixtureSnapshot, `"tab_id":"w1:t1","workspace_id":"w1","zoomed":false`, `"tab_id":"w1:t1","workspace_id":"w1","zoomed":true`, 1))
	if err != nil {
		t.Fatalf("parsing the fixture: %v", err)
	}
	for target, want := range map[string][][]string{
		"demo":  {{"workspace", "focus", "w1"}, {"pane", "zoom", "--pane", "w1:p1", "--off"}},
		"w1:t1": {{"workspace", "focus", "w1"}, {"tab", "focus", "w1:t1"}, {"pane", "zoom", "--pane", "w1:p1", "--off"}},
		"w1:t2": {{"workspace", "focus", "w1"}, {"tab", "focus", "w1:t2"}},
		"w1:p2": {{"workspace", "focus", "w1"}, {"tab", "focus", "w1:t1"}, {"pane", "zoom", "--pane", "w1:p2", "--on"}},
	} {
		r, err := snap.resolve(target)
		if err != nil {
			t.Fatalf("resolving %q: %v", target, err)
		}
		if got := steeringArgs(r); !reflect.DeepEqual(got, want) {
			t.Errorf("steering onto %q runs %v, want %v", target, got, want)
		}
	}
}

// §10 A session may not be named like any id in the hierarchy, because
// resolution reads those spellings as the id, and a workspace nothing has
// labelled is NAMED by its id.
func TestASessionMayNotBeNamedLikeAnyID(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"w1", "wA", "w12", "w1:t2", "w1:tA", "w1:p1", "w4Y:pA"} {
		err := validateName(name)
		if backend.CodeOf(err) != backend.CodeUsage {
			t.Errorf("the name %q is %q, want %q", name, backend.CodeOf(err), backend.CodeUsage)
		}
	}
	for _, name := range []string{"w", "w1p1", "work", "w1:", "w1:x2", "wo:t1", "demo"} {
		if err := validateName(name); err != nil {
			t.Errorf("the ordinary name %q was rejected: %v", name, err)
		}
	}
}

// §3.3 With no server running, resolution has nothing to find and every
// target-addressed read says so as not-found rather than as a transport error.
func TestResolvingWithNoServerIsNotFound(t *testing.T) {
	t.Parallel()
	b := New(WithSocketPath(filepath.Join(shortDir(t), "h.sock")))
	_, err := b.resolve(context.Background(), "w1")
	if backend.CodeOf(err) != backend.CodeSessionNotFound {
		t.Errorf("resolving with no server is %q, want %q", backend.CodeOf(err), backend.CodeSessionNotFound)
	}
	if !strings.Contains(err.Error(), "w1") {
		t.Errorf("the error %q does not name the target", err.Error())
	}
}
