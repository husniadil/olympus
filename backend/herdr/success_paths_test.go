package herdr

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// §7.5 A server that advertises pane_redraw is asked to redraw the ONE pane a
// target resolves to, by its id. The fork-only capability had only its refusal
// tested, so the request a supporting server receives was never checked.
//
// The snapshot comes from a stand-in herdr on PATH, since the backend reads it
// with the CLI, and the request from a fake API socket.
func TestRedrawAsksTheServerToRedrawTheResolvedPane(t *testing.T) {
	stub := shortDir(t)
	script := "#!/bin/sh\ncat <<'EOF'\n" + fixtureSnapshot + "\nEOF\n"
	if err := os.WriteFile(filepath.Join(stub, "herdr"), []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", stub+string(os.PathListSeparator)+os.Getenv("PATH"))

	var mu sync.Mutex
	var redrawn []string
	api := newFakeAPI(t, func(method string, params json.RawMessage) string {
		switch method {
		case "ping":
			return `{"id":"x","result":{"type":"pong","version":"0.9.0+agm.4","protocol":22,"capabilities":{"pane_redraw":true}}}`
		case "pane.redraw":
			var p struct {
				PaneID string `json:"pane_id"`
			}
			_ = json.Unmarshal(params, &p)
			mu.Lock()
			redrawn = append(redrawn, p.PaneID)
			mu.Unlock()
			return `{"id":"x","result":{"type":"ok"}}`
		}
		return `{"id":"x","error":{"code":"unknown_method","message":"unknown"}}`
	})
	b := New(WithSocketPath(api.path))

	// A workspace resolves to the focused pane of its active tab (§3.6), and a
	// pane id to itself.
	for target, want := range map[string]string{"demo": "w1:p1", "w1:p3": "w1:p3"} {
		mu.Lock()
		redrawn = nil
		mu.Unlock()
		if err := b.Redraw(context.Background(), target); err != nil {
			t.Fatalf("Redraw(%s): %v", target, err)
		}
		mu.Lock()
		got := append([]string(nil), redrawn...)
		mu.Unlock()
		if len(got) != 1 || got[0] != want {
			t.Errorf("Redraw(%s) sent pane.redraw for %v, want once for %s", target, got, want)
		}
	}
}

// §8.10 On a server that focuses a pane for one client (client_view_pane), a
// focus sends the client's tag with the pane, its tab and its workspace, waits
// for the server's answer where the client acknowledges, and leaves the
// client recorded on the TAB. Only its refusals were tested.
func TestAPaneIsFocusedForTheTaggedClientAlone(t *testing.T) {
	t.Parallel()
	const pong = `{"type":"pong","version":"0.9.0+agm.3","protocol":22,"capabilities":{"client_view_focus":true,"client_view_ack":true,"client_view_pane":true}}`
	v := newViewServer(t, pong,
		`{"client_id":3,"client_tag":"tag","workspace_id":"w1","tab_id":"w1:t1","pane_id":"w1:p1","zoomed":false,"snapshot_acks":true,"view_applied":true}`,
		func(map[string]any) string {
			return `{"id":"x","result":{"type":"client_view_focus","client":{"client_id":3,"client_tag":"tag","workspace_id":"w1","tab_id":"w1:t2","pane_id":"w1:p3","zoomed":false,"snapshot_acks":true,"view_applied":true}}}`
		},
		func(map[string]any) string { t.Error("client.view.wait was asked for a focus"); return `{}` })
	b := New(WithSocketPath(v.api.path))
	to := resolved{kind: kindPane, workspace: workspaceRow{WorkspaceID: "w1"}, tab: tabRow{TabID: "w1:t2"}, pane: paneRow{PaneID: "w1:p3"}}
	at := &bareClient{at: tabTarget("w1", "w1:t1")}

	if err := b.focusOnClient(context.Background(), "tag", to, at, noFrames(t)); err != nil {
		t.Fatalf("focusOnClient: %v", err)
	}
	focused, _ := v.sent()
	if len(focused) != 1 {
		t.Fatalf("client.view.focus was asked %d times, want 1", len(focused))
	}
	p := focused[0]
	if p["client_tag"] != "tag" || p["workspace_id"] != "w1" || p["tab_id"] != "w1:t2" || p["pane_id"] != "w1:p3" || p["wait"] != true {
		t.Errorf("client.view.focus was sent %v, want the tag, w1, w1:t2, w1:p3 and wait: true", p)
	}
	if at.id() != "w1:t2" {
		t.Errorf("the client is recorded on %s, want the tab w1:t2", at.id())
	}
}
