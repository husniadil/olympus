package herdr

import (
	"strings"
	"testing"

	"github.com/husniadil/olympus/backend"
)

// §3.4 The focused flag is only set where every client shows the focused
// workspace.
//
// The boundary is 0.9.0, measured 2026-09-10 with two clients on one server:
// on 0.8.2 an API workspace focus moved both clients onto the new workspace,
// on 0.9.0 it moved the foreground client alone. Below the boundary the flag
// answers "what is every client showing"; at and above it the server's focus
// answers a different question, so no row carries the flag at all.
func TestSharedClientFocusBoundary(t *testing.T) {
	for _, c := range []struct {
		version string
		shared  bool
	}{
		{"0.8.0", true},
		{"0.8.2", true},
		{"0.8.10", true},
		{"0.9.0", false},
		{"0.9.3", false},
		{"0.10.0", false},
		{"1.0.0", false},
		{"v0.8.2", true},
		{"0.9.0-rc.1", false},
		// A version nothing can be read out of reads as shared: that is the
		// answer for every herdr that existed when the split arrived, and a
		// listing must not drop the flag because a build spelled its version
		// oddly.
		{"", true},
		{"herdr", true},
		{"0.9", true},
	} {
		if got := sharedClientFocus(c.version); got != c.shared {
			t.Errorf("sharedClientFocus(%q) = %v, want %v", c.version, got, c.shared)
		}
	}
}

// §3.4 A listing off a server whose clients keep their own view carries the
// flag on no row, rather than pointing at the workspace the server happens to
// have focused.
//
// Asserted through the snapshot parse and the listing shape rather than
// against a live server, because the difference is a property of what the
// server REPORTS and both versions report the same focus field.
func TestSessionsOmitFocusedAboveTheBoundary(t *testing.T) {
	const body = `{"result":{"snapshot":{"version":"%s","focused_workspace_id":"w1",` +
		`"focused_tab_id":"w1:t1","focused_pane_id":"w1:p1",` +
		`"workspaces":[{"workspace_id":"w1","label":"one","number":1,"active_tab_id":"w1:t1","focused":true},` +
		`{"workspace_id":"w2","label":"two","number":2,"active_tab_id":"w2:t1","focused":false}],` +
		`"tabs":[],"panes":[],"layouts":[]}}}`

	for _, c := range []struct {
		version string
		want    string // the id expected to carry the flag, empty for none
	}{
		{"0.8.2", "w1"},
		{"0.9.0", ""},
	} {
		snap, err := parseSnapshot(strings.Replace(body, "%s", c.version, 1))
		if err != nil {
			t.Fatalf("parsing a %s snapshot: %v", c.version, err)
		}
		if snap.Version != c.version {
			t.Fatalf("the snapshot did not carry its version: %q", snap.Version)
		}
		shared := sharedClientFocus(snap.Version)
		var focused []string
		for _, ws := range snap.Workspaces {
			if shared && ws.WorkspaceID == snap.FocusedWorkspaceID {
				focused = append(focused, ws.WorkspaceID)
			}
		}
		if c.want == "" {
			if len(focused) != 0 {
				t.Errorf("herdr %s: %d rows carry focused, want none: %v", c.version, len(focused), focused)
			}
			continue
		}
		if len(focused) != 1 || focused[0] != c.want {
			t.Errorf("herdr %s: focused rows %v, want exactly [%s]", c.version, focused, c.want)
		}
	}
}

// The listing's own type keeps the flag optional, so "no row is focused" and
// "this backend cannot say" are the same wire shape. A consumer reads the
// absence as cannot-say (§3.4), which is what makes the gate above safe.
func TestFocusedIsOmittedWhenUnset(t *testing.T) {
	var s backend.Session
	if s.Focused {
		t.Fatal("a zero session claims focus")
	}
}
