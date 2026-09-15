package herdr

import (
	"bufio"
	"context"
	"encoding/json"
	"net"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"unicode"

	"github.com/husniadil/olympus/backend"
)

// §8.10 Whether a server moves one client's view, and whether it reports
// when a client has applied one, is read from the capabilities its `ping`
// answers with, never from its version: a build that carries the request
// reports the same version as one that does not.
func TestClientViewCapabilityIsReadFromThePong(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		pong string
		want serverCaps
	}{
		{"advertised", `{"id":"x","result":{"type":"pong","version":"0.9.0","protocol":22,"capabilities":{"health_check":true,"client_view_focus":true}}}`, serverCaps{viewFocus: true}},
		{"advertised as false", `{"id":"x","result":{"type":"pong","version":"0.9.0","protocol":22,"capabilities":{"client_view_focus":false}}}`, serverCaps{}},
		{"a server that predates it", `{"id":"x","result":{"type":"pong","version":"0.9.0","protocol":22,"capabilities":{"health_check":true}}}`, serverCaps{}},
		{"a server with no capabilities at all", `{"id":"x","result":{"type":"pong","version":"0.8.2","protocol":19}}`, serverCaps{}},
		{"the view acknowledged as well", `{"id":"x","result":{"type":"pong","version":"0.9.0+agm.2","protocol":22,"capabilities":{"client_view_focus":true,"client_view_ack":true}}}`, serverCaps{viewFocus: true, viewAck: true}},
		{"the view acknowledged as false", `{"id":"x","result":{"type":"pong","version":"0.9.0","protocol":22,"capabilities":{"client_view_focus":true,"client_view_ack":false}}}`, serverCaps{viewFocus: true}},
		{"a pane focused for one client", `{"id":"x","result":{"type":"pong","version":"0.9.0+agm.3","protocol":22,"capabilities":{"client_view_focus":true,"client_view_ack":true,"client_view_pane":true}}}`, serverCaps{viewFocus: true, viewAck: true, viewPane: true}},
	}
	for _, c := range cases {
		got, err := parsePong(json.RawMessage(resultOf(t, c.pong)))
		if err != nil {
			t.Errorf("%s: %v", c.name, err)
			continue
		}
		if got != c.want {
			t.Errorf("%s: the capabilities read as %+v, want %+v", c.name, got, c.want)
		}
	}
	if _, err := parsePong(json.RawMessage(`{"type":"workspace_list"}`)); backend.CodeOf(err) != backend.CodeUnexpected {
		t.Errorf("an answer that is not a pong is %q, want %q", backend.CodeOf(err), backend.CodeUnexpected)
	}
}

func resultOf(t *testing.T, line string) string {
	t.Helper()
	var reply struct {
		Result json.RawMessage `json:"result"`
	}
	if err := json.Unmarshal([]byte(line), &reply); err != nil {
		t.Fatalf("fixture: %v", err)
	}
	return string(reply.Result)
}

// fakeAPI answers one line per connection on a unix socket, the way herdr's
// API socket does, and counts the requests per method.
type fakeAPI struct {
	path   string
	counts map[string]*atomic.Int32
	answer func(method string, params json.RawMessage) string
}

func newFakeAPI(t *testing.T, answer func(method string, params json.RawMessage) string) *fakeAPI {
	t.Helper()
	path := filepath.Join(shortDir(t), "h.sock")
	l, err := net.Listen("unix", path)
	if err != nil {
		t.Fatalf("listening on %s: %v", path, err)
	}
	t.Cleanup(func() { _ = l.Close() })
	api := &fakeAPI{path: path, answer: answer, counts: map[string]*atomic.Int32{
		"ping": {}, "client.list": {}, "client.view.focus": {}, "client.view.wait": {},
	}}
	go func() {
		for {
			conn, err := l.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				line, err := bufio.NewReader(conn).ReadString('\n')
				if err != nil {
					return
				}
				var req struct {
					ID     string          `json:"id"`
					Method string          `json:"method"`
					Params json.RawMessage `json:"params"`
				}
				if err := json.Unmarshal([]byte(line), &req); err != nil {
					return
				}
				if n, ok := api.counts[req.Method]; ok {
					n.Add(1)
				}
				_, _ = conn.Write([]byte(api.answer(req.Method, req.Params) + "\n"))
			}()
		}
	}()
	return api
}

// §8.10 The capability is asked once per server a handle drives, since every
// bare attach and every go would otherwise pay a round trip for an answer
// that does not change while the server runs. A failed ask is not kept: the
// next attach asks again rather than taking a hiccup for the answer.
func TestClientViewCapabilityIsAskedOncePerServer(t *testing.T) {
	t.Parallel()
	var failing atomic.Bool
	failing.Store(true)
	api := newFakeAPI(t, func(method string, _ json.RawMessage) string {
		if failing.Load() {
			return `{"id":"x","error":{"code":"internal","message":"not yet"}}`
		}
		return `{"id":"x","result":{"type":"pong","version":"0.9.0","protocol":22,"capabilities":{"client_view_focus":true}}}`
	})
	b := New(WithSocketPath(api.path))
	ctx := context.Background()

	if _, err := b.clientViews(ctx); err == nil {
		t.Fatal("a ping answered with an error read as a capability")
	}
	failing.Store(false)
	for i := 0; i < 3; i++ {
		got, err := b.clientViews(ctx)
		if err != nil || !got {
			t.Fatalf("clientViews = %v, %v; want true", got, err)
		}
	}
	if n := api.counts["ping"].Load(); n != 2 {
		t.Errorf("the server was pinged %d times, want 2: the failed ask and one that was kept", n)
	}
}

// §8.10 A request over the socket is refused in the same vocabulary as one
// through the CLI: a workspace that is gone is not-found, and anything this
// backend does not name is unexpected, with herdr's own message kept.
func TestAnAPIRefusalIsClassifiedLikeTheCLIs(t *testing.T) {
	t.Parallel()
	api := newFakeAPI(t, func(method string, _ json.RawMessage) string {
		if method == "client.list" {
			return `{"id":"x","error":{"code":"client_not_found","message":"no client is tagged \"t\""}}`
		}
		return `{"id":"x","error":{"code":"workspace_not_found","message":"workspace w9 not found"}}`
	})
	b := New(WithSocketPath(api.path))
	ctx := context.Background()

	_, err := b.call(ctx, "client.view.focus", map[string]string{"client_tag": "t", "workspace_id": "w9"})
	if backend.CodeOf(err) != backend.CodeSessionNotFound {
		t.Errorf("a workspace the server does not have is %q, want %q", backend.CodeOf(err), backend.CodeSessionNotFound)
	}
	_, err = b.call(ctx, "client.list", struct{}{})
	if backend.CodeOf(err) != backend.CodeUnexpected || !strings.Contains(err.Error(), `no client is tagged "t"`) {
		t.Errorf("an unnamed refusal is %q (%v), want %q carrying herdr's message", backend.CodeOf(err), err, backend.CodeUnexpected)
	}

	gone := New(WithSocketPath(filepath.Join(shortDir(t), "none.sock")))
	if _, err := gone.call(ctx, "ping", struct{}{}); backend.CodeOf(err) != backend.CodeBackendUnavailable {
		t.Errorf("a socket with no server behind it is %q (%v), want %q", backend.CodeOf(err), err, backend.CodeBackendUnavailable)
	}
}

// §8.10, §17.1 A client's tag is what every later request addresses it by,
// so each attach draws its own, inside the bounds herdr accepts: 1 to 128
// bytes with no control characters.
func TestEveryClientTagIsItsOwnAndOneHerdrAccepts(t *testing.T) {
	t.Parallel()
	seen := map[string]bool{}
	for i := 0; i < 64; i++ {
		tag, err := newClientTag()
		if err != nil {
			t.Fatalf("newClientTag: %v", err)
		}
		if len(tag) == 0 || len(tag) > 128 {
			t.Errorf("tag %q is %d bytes", tag, len(tag))
		}
		if strings.IndexFunc(tag, unicode.IsControl) >= 0 {
			t.Errorf("tag %q carries a control character", tag)
		}
		if !strings.HasPrefix(tag, clientTagPrefix) {
			t.Errorf("tag %q does not carry the reserved prefix %q", tag, clientTagPrefix)
		}
		if seen[tag] {
			t.Errorf("tag %q was drawn twice", tag)
		}
		seen[tag] = true
	}
}

// §8.10 Where a tagged client is, read from `client.list`.
func TestATaggedClientIsFoundInTheClientList(t *testing.T) {
	t.Parallel()
	const list = `{"type":"client_list","clients":[` +
		`{"client_id":1,"workspace_id":"w1","tab_id":"w1:t1"},` +
		`{"client_id":3,"client_tag":"olympus-client-a","workspace_id":"w2","tab_id":"w2:t2"}]}`
	got, ok, err := findClient(json.RawMessage(list), "olympus-client-a")
	if err != nil || !ok {
		t.Fatalf("findClient = %+v, %v, %v; want the tagged row", got, ok, err)
	}
	if got.ClientID != 3 || got.WorkspaceID != "w2" || got.TabID != "w2:t2" {
		t.Errorf("found %+v, want client 3 on w2, w2:t2", got)
	}
	if _, ok, err := findClient(json.RawMessage(list), "olympus-client-b"); ok || err != nil {
		t.Errorf("a tag nobody carries was found (%v, %v)", ok, err)
	}
}

// A viewServer is a fake herdr API holding one tagged client: where it is,
// whether it acknowledges snapshots, and what the server advertises. It
// answers `client.view.focus` and `client.view.wait` the way herdr does, and
// records the params each was sent.
type viewServer struct {
	api     *fakeAPI
	mu      sync.Mutex
	focused []map[string]any
	waited  []map[string]any
}

// sent is what `client.view.focus` and `client.view.wait` were sent so far.
func (v *viewServer) sent() (focused, waited []map[string]any) {
	v.mu.Lock()
	defer v.mu.Unlock()
	return append([]map[string]any(nil), v.focused...), append([]map[string]any(nil), v.waited...)
}

func newViewServer(t *testing.T, pong, client string, focus, wait func(params map[string]any) string) *viewServer {
	t.Helper()
	v := &viewServer{}
	record := func(into *[]map[string]any, params json.RawMessage) map[string]any {
		var p map[string]any
		_ = json.Unmarshal(params, &p)
		*into = append(*into, p)
		return p
	}
	v.api = newFakeAPI(t, func(method string, params json.RawMessage) string {
		v.mu.Lock()
		defer v.mu.Unlock()
		switch method {
		case "ping":
			return `{"id":"x","result":` + pong + `}`
		case "client.list":
			return `{"id":"x","result":{"type":"client_list","clients":[` + client + `]}}`
		case "client.view.focus":
			return focus(record(&v.focused, params))
		case "client.view.wait":
			return wait(record(&v.waited, params))
		}
		return `{"id":"x","error":{"code":"unknown_method","message":"unknown"}}`
	})
	return v
}

const (
	ackingPong    = `{"type":"pong","version":"0.9.0+agm.2","protocol":22,"capabilities":{"client_view_focus":true,"client_view_ack":true}}`
	focusOnlyPong = `{"type":"pong","version":"0.9.0+agm.1","protocol":22,"capabilities":{"client_view_focus":true}}`
)

func tabTarget(ws, tab string) resolved {
	return resolved{kind: kindTab, workspace: workspaceRow{WorkspaceID: ws}, tab: tabRow{TabID: tab}}
}

// noFrames fails the test if a frame is waited for.
func noFrames(t *testing.T) backend.Expect {
	return func(mark []byte) func(time.Duration) bool {
		t.Errorf("the client's output was watched for %q on a server that reports the view applied", mark)
		return func(time.Duration) bool { return true }
	}
}

// framesCounted answers every wait at once and counts them.
func framesCounted(n *atomic.Int32) backend.Expect {
	return func(mark []byte) func(time.Duration) bool {
		if string(mark) == frameEnd {
			n.Add(1)
		}
		return func(time.Duration) bool { return true }
	}
}

// §8.10 On a server that reports when a client has applied its view, a go
// that moves the client asks the move to answer once the client has it
// (`wait: true`, bounded), checks where the answer says the client is, and
// watches nothing the client paints.
func TestAMoveOntoAnAckingClientWaitsForTheServersAnswerNotAFrame(t *testing.T) {
	t.Parallel()
	v := newViewServer(t, ackingPong,
		`{"client_id":3,"client_tag":"tag","workspace_id":"w1","tab_id":"w1:t1","pane_id":"w1:p1","zoomed":false,"snapshot_acks":true,"view_applied":true}`,
		func(p map[string]any) string {
			return `{"id":"x","result":{"type":"client_view_focus","client":{"client_id":3,"client_tag":"tag","workspace_id":"w2","tab_id":"w2:t2","pane_id":"w2:p2","zoomed":false,"snapshot_acks":true,"view_applied":true}}}`
		},
		func(map[string]any) string { t.Error("client.view.wait was asked for a move"); return `{}` })
	b := New(WithSocketPath(v.api.path))
	to := tabTarget("w2", "w2:t2")
	at := &bareClient{at: tabTarget("w1", "w1:t1")}
	if err := b.showOnClient(context.Background(), "tag", to, at, noFrames(t)); err != nil {
		t.Fatalf("showOnClient: %v", err)
	}
	focused, _ := v.sent()
	if len(focused) != 1 {
		t.Fatalf("client.view.focus was asked %d times, want 1", len(focused))
	}
	p := focused[0]
	if p["wait"] != true || p["client_tag"] != "tag" || p["workspace_id"] != "w2" || p["tab_id"] != "w2:t2" {
		t.Errorf("client.view.focus was sent %v, want the tag, w2, w2:t2 and wait: true", p)
	}
	if ms, ok := p["timeout_ms"].(float64); !ok || ms <= 0 || ms > 60000 {
		t.Errorf("client.view.focus was sent timeout_ms %v, want a bound herdr accepts", p["timeout_ms"])
	}
	if at.id() != "w2:t2" {
		t.Errorf("the client is recorded on %s, want w2:t2", at.id())
	}
}

// §8.10 Where the client already shows the target, nothing moves it, and the
// server is still asked to answer once the client has applied the view it
// holds now: that covers the first placement of a client just launched, and
// a zoom that moved the focus of the tab it shows.
func TestAnAckingClientAlreadyOnItsTargetIsWaitedForByTheServer(t *testing.T) {
	t.Parallel()
	const on = `{"client_id":3,"client_tag":"tag","workspace_id":"w2","tab_id":"w2:t2","pane_id":"w2:p2","zoomed":false,"snapshot_acks":true,"view_applied":true}`
	v := newViewServer(t, ackingPong, on,
		func(map[string]any) string { t.Error("client.view.focus was asked with no move to make"); return `{}` },
		func(map[string]any) string {
			return `{"id":"x","result":{"type":"client_view_wait","client":` + on + `}}`
		})
	b := New(WithSocketPath(v.api.path))
	if err := b.showOnClient(context.Background(), "tag", tabTarget("w2", "w2:t2"), &bareClient{}, noFrames(t)); err != nil {
		t.Fatalf("showOnClient: %v", err)
	}
	_, waited := v.sent()
	if len(waited) != 1 || waited[0]["client_tag"] != "tag" {
		t.Fatalf("client.view.wait was sent %v, want once for the tag", waited)
	}
	if ms, ok := waited[0]["timeout_ms"].(float64); !ok || ms <= 0 || ms > 60000 {
		t.Errorf("client.view.wait was sent timeout_ms %v, want a bound herdr accepts", waited[0]["timeout_ms"])
	}
}

// §8.10, §8.11 A server that moves one client's view but does not report
// when it is applied (a released herdr, or the fork before it), and a client
// that does not acknowledge snapshots on a server that would, are confirmed
// by the frame the client paints, as before: no wait is asked of the server.
func TestAClientViewWithoutAcknowledgementIsConfirmedByItsFrame(t *testing.T) {
	t.Parallel()
	cases := []struct{ name, pong, client string }{
		{"a server without client_view_ack", focusOnlyPong,
			`{"client_id":3,"client_tag":"tag","workspace_id":"w1","tab_id":"w1:t1"}`},
		{"a client without snapshot_acks", ackingPong,
			`{"client_id":3,"client_tag":"tag","workspace_id":"w1","tab_id":"w1:t1","pane_id":"w1:p1","snapshot_acks":false,"view_applied":false}`},
	}
	for _, c := range cases {
		v := newViewServer(t, c.pong, c.client,
			func(p map[string]any) string {
				return `{"id":"x","result":{"type":"client_view_focus","client":{"client_id":3,"client_tag":"tag","workspace_id":"w2","tab_id":"w2:t2"}}}`
			},
			func(map[string]any) string { t.Errorf("%s: client.view.wait was asked", c.name); return `{}` })
		b := New(WithSocketPath(v.api.path))
		var frames atomic.Int32
		if err := b.showOnClient(context.Background(), "tag", tabTarget("w2", "w2:t2"), &bareClient{}, framesCounted(&frames)); err != nil {
			t.Fatalf("%s: showOnClient: %v", c.name, err)
		}
		focused, _ := v.sent()
		if len(focused) != 1 {
			t.Fatalf("%s: client.view.focus was asked %d times, want 1", c.name, len(focused))
		}
		if _, ok := focused[0]["wait"]; ok {
			t.Errorf("%s: client.view.focus was sent %v, want no wait", c.name, focused[0])
		}
		if frames.Load() != 1 {
			t.Errorf("%s: the frame end was waited for %d times, want 1", c.name, frames.Load())
		}
	}
}

// §8.10 A client that has not applied its view by the deadline fails the go
// as a timeout, loudly, rather than having what was typed forwarded into a
// pane it may not address.
func TestAnAckingClientThatDoesNotApplyItsViewFailsTheGo(t *testing.T) {
	t.Parallel()
	v := newViewServer(t, ackingPong,
		`{"client_id":3,"client_tag":"tag","workspace_id":"w1","tab_id":"w1:t1","pane_id":"w1:p1","snapshot_acks":true,"view_applied":true}`,
		func(map[string]any) string {
			return `{"id":"x","error":{"code":"timeout","message":"client 3 did not apply a snapshot showing its view in time"}}`
		},
		func(map[string]any) string { return `{}` })
	b := New(WithSocketPath(v.api.path))
	at := &bareClient{at: tabTarget("w1", "w1:t1")}
	err := b.showOnClient(context.Background(), "tag", tabTarget("w2", "w2:t2"), at, noFrames(t))
	if backend.CodeOf(err) != backend.CodeTimeout || !strings.Contains(err.Error(), "did not apply") {
		t.Errorf("a view the client never applied is %q (%v), want %q saying so", backend.CodeOf(err), err, backend.CodeTimeout)
	}
	if at.id() != "w1:t1" {
		t.Errorf("a failed go recorded the client on %s", at.id())
	}
}

// §8.10 The client an acknowledged wait answers with must show the target:
// its workspace, its tab where the target names one, and for a pane the pane
// focused and its tab zoomed as the zoom step left it. Anything else fails
// the go.
func TestTheAcknowledgedViewMustShowTheTarget(t *testing.T) {
	t.Parallel()
	pane := resolved{kind: kindPane, workspace: workspaceRow{WorkspaceID: "w2"}, tab: tabRow{TabID: "w2:t2"}, pane: paneRow{PaneID: "w2:p2"}}
	on := clientRow{WorkspaceID: "w2", TabID: "w2:t2", PaneID: "w2:p2", Zoomed: true, ViewApplied: true}
	cases := []struct {
		name   string
		row    func(clientRow) clientRow
		to     resolved
		zoomed bool
		want   bool
	}{
		{"the pane, zoomed and applied", func(r clientRow) clientRow { return r }, pane, true, true},
		{"another workspace", func(r clientRow) clientRow { r.WorkspaceID = "w1"; return r }, pane, true, false},
		{"another tab", func(r clientRow) clientRow { r.TabID = "w2:t1"; return r }, pane, true, false},
		{"another pane focused", func(r clientRow) clientRow { r.PaneID = "w2:p1"; return r }, pane, true, false},
		{"the pane not zoomed", func(r clientRow) clientRow { r.Zoomed = false; return r }, pane, true, false},
		{"a lone pane the zoom left unzoomed", func(r clientRow) clientRow { r.Zoomed = false; return r }, pane, false, true},
		{"zoomed where the step left the tab unzoomed", func(r clientRow) clientRow { r.Zoomed = true; return r }, pane, false, false},
		{"not applied", func(r clientRow) clientRow { r.ViewApplied = false; return r }, pane, true, false},
		{"a tab target shows any pane of its tab", func(r clientRow) clientRow { r.PaneID, r.Zoomed = "w2:p1", false; return r }, tabTarget("w2", "w2:t2"), true, true},
		{"a workspace target shows any tab of it", func(r clientRow) clientRow { r.TabID = "w2:t9"; return r },
			resolved{kind: kindWorkspace, workspace: workspaceRow{WorkspaceID: "w2"}, tab: tabRow{TabID: "w2:t2"}}, true, true},
	}
	for _, c := range cases {
		if got := viewShows(c.row(on), c.to, c.zoomed); got != c.want {
			t.Errorf("%s: viewShows = %v, want %v", c.name, got, c.want)
		}
	}
}

// §13.5 Every client `client.list` reports becomes a row, in the server's
// order, in Olympus's names: a workspace is a session, a tab a window. Where
// the server reports when a client has applied its view, a row carries the
// pane, the zoom and, for a client that acknowledges snapshots, whether the
// view is applied; where it does not, those are omitted rather than false,
// since false would claim an answer the server never gave.
func TestTheClientListIsReadIntoRows(t *testing.T) {
	t.Parallel()
	const list = `{"type":"client_list","clients":[` +
		`{"client_id":1,"workspace_id":"w1","tab_id":"w1:t1","pane_id":"w1:p1","zoomed":false,"snapshot_acks":false,"view_applied":false},` +
		`{"client_id":7,"client_tag":"browser-1","workspace_id":"w2","tab_id":"w2:t3","pane_id":"w2:p4","zoomed":true,"snapshot_acks":true,"revision":9,"view_revision":8,"applied_revision":9,"view_applied":true},` +
		`{"client_id":9,"client_tag":"early","snapshot_acks":true,"view_applied":false}]}`
	yes, no := true, false

	acked, err := clientsOf(json.RawMessage(list), true)
	if err != nil {
		t.Fatalf("clientsOf: %v", err)
	}
	want := []backend.Client{
		{ID: "1", SessionID: "w1", WindowID: "w1:t1", PaneID: "w1:p1", Zoomed: &no},
		{ID: "7", Tag: "browser-1", SessionID: "w2", WindowID: "w2:t3", PaneID: "w2:p4", Zoomed: &yes, ViewApplied: &yes},
		{ID: "9", Tag: "early", ViewApplied: &no},
	}
	assertClients(t, "with client_view_ack", acked, want)

	const focusOnly = `{"type":"client_list","clients":[{"client_id":4,"client_tag":"t","workspace_id":"w5","tab_id":"w5:t1"}]}`
	plain, err := clientsOf(json.RawMessage(focusOnly), false)
	if err != nil {
		t.Fatalf("clientsOf: %v", err)
	}
	assertClients(t, "without client_view_ack", plain, []backend.Client{{ID: "4", Tag: "t", SessionID: "w5", WindowID: "w5:t1"}})

	empty, err := clientsOf(json.RawMessage(`{"type":"client_list","clients":[]}`), true)
	if err != nil || empty == nil || len(empty) != 0 {
		t.Errorf("an empty client list is %#v, %v; want an empty, non-nil slice", empty, err)
	}
	if _, err := clientsOf(json.RawMessage(`{"clients":"nope"}`), true); backend.CodeOf(err) != backend.CodeUnexpected {
		t.Errorf("an unreadable client list is %q, want %q", backend.CodeOf(err), backend.CodeUnexpected)
	}
}

func assertClients(t *testing.T, name string, got, want []backend.Client) {
	t.Helper()
	g, _ := json.Marshal(got)
	w, _ := json.Marshal(want)
	if string(g) != string(w) {
		t.Errorf("%s: the rows are\n\t%s\nwant\n\t%s", name, g, w)
	}
}

// §13.5 The listing is the server's: a server that advertises
// `client_view_focus` is asked `client.list`; one that does not cannot say
// which client shows what, and is UNSUPPORTED rather than an empty list, which
// would claim there are no clients.
func TestClientsAreListedOnlyWhereTheServerAdvertisesThem(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	const client = `{"client_id":2,"client_tag":"mine","workspace_id":"w1","tab_id":"w1:t1","pane_id":"w1:p2","zoomed":false,"snapshot_acks":true,"view_applied":true}`

	v := newViewServer(t, ackingPong, client, nil, nil)
	rows, err := New(WithSocketPath(v.api.path)).Clients(ctx)
	if err != nil || len(rows) != 1 || rows[0].Tag != "mine" || rows[0].PaneID != "w1:p2" || rows[0].ViewApplied == nil || !*rows[0].ViewApplied {
		t.Errorf("Clients on an advertising server = %+v, %v; want the one tagged client on w1:p2, applied", rows, err)
	}

	plain := newViewServer(t, `{"type":"pong","version":"0.9.0","protocol":22,"capabilities":{"health_check":true}}`, client, nil, nil)
	_, err = New(WithSocketPath(plain.api.path)).Clients(ctx)
	if backend.CodeOf(err) != backend.CodeUnsupported {
		t.Errorf("Clients on a server without client_view_focus is %q (%v), want %q", backend.CodeOf(err), err, backend.CodeUnsupported)
	}
	if n := plain.api.counts["client.list"].Load(); n != 0 {
		t.Errorf("a server without the capability was asked client.list %d times", n)
	}
}

// §3.3, §12.3 No server running is an empty listing, not an error: there is
// no client to find, and nothing went wrong asking.
func TestClientsWithNoServerRunningIsAnEmptyList(t *testing.T) {
	t.Parallel()
	rows, err := New(WithSocketPath(filepath.Join(shortDir(t), "none.sock"))).Clients(context.Background())
	if err != nil || rows == nil || len(rows) != 0 {
		t.Errorf("Clients with no server = %#v, %v; want an empty, non-nil list", rows, err)
	}
}

// §8.10, §13.5 A caller-chosen client tag is refused as usage wherever it
// cannot name the client: a tag herdr would refuse, an attach that is not
// bare (the only one launched with a tag), and a server that does not
// advertise `client_view_focus`, which launches no client with a tag at all.
// Each is refused before the target is resolved and before anything runs, so
// a fake socket with no CLI behind it is enough to answer.
func TestACallerClientTagIsUsageWhereItCannotNameTheClient(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	bare := backend.AttachSpec{Role: backend.RoleController, Supersede: true, SessionClient: true, Bare: true, ClientTag: "mine"}

	plain := newViewServer(t, `{"type":"pong","version":"0.9.0","protocol":22,"capabilities":{}}`, ``, nil, nil)
	if _, err := New(WithSocketPath(plain.api.path)).Attach(ctx, "w1", bare); backend.CodeOf(err) != backend.CodeUsage {
		t.Errorf("a client tag on a server without client_view_focus is %q (%v), want %q", backend.CodeOf(err), err, backend.CodeUsage)
	}

	v := newViewServer(t, ackingPong, ``, nil, nil)
	b := New(WithSocketPath(v.api.path))
	invalid := bare
	invalid.ClientTag = "line\nbreak"
	if _, err := b.Attach(ctx, "w1", invalid); backend.CodeOf(err) != backend.CodeUsage {
		t.Errorf("a client tag with a control character is %q (%v), want %q", backend.CodeOf(err), err, backend.CodeUsage)
	}
	for _, spec := range []backend.AttachSpec{
		{Role: backend.RoleController, Supersede: true, SessionClient: true, ClientTag: "mine"},
		{Role: backend.RoleController, Supersede: true, ClientTag: "mine"},
	} {
		if _, err := b.Attach(ctx, "w1", spec); backend.CodeOf(err) != backend.CodeUsage {
			t.Errorf("a client tag on an attach that is not bare (%+v) is %q (%v), want %q", spec, backend.CodeOf(err), err, backend.CodeUsage)
		}
	}
}
