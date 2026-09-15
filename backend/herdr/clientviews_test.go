package herdr

import (
	"bufio"
	"context"
	"encoding/json"
	"net"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"unicode"

	"github.com/husniadil/olympus/backend"
)

// §8.10 Whether a server moves one client's view is read from the
// capabilities its `ping` answers with, never from its version: a build that
// carries the request reports the same version as one that does not.
func TestClientViewCapabilityIsReadFromThePong(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		pong string
		want bool
	}{
		{"advertised", `{"id":"x","result":{"type":"pong","version":"0.9.0","protocol":22,"capabilities":{"health_check":true,"client_view_focus":true}}}`, true},
		{"advertised as false", `{"id":"x","result":{"type":"pong","version":"0.9.0","protocol":22,"capabilities":{"client_view_focus":false}}}`, false},
		{"a server that predates it", `{"id":"x","result":{"type":"pong","version":"0.9.0","protocol":22,"capabilities":{"health_check":true}}}`, false},
		{"a server with no capabilities at all", `{"id":"x","result":{"type":"pong","version":"0.8.2","protocol":19}}`, false},
	}
	for _, c := range cases {
		got, err := parsePong(json.RawMessage(resultOf(t, c.pong)))
		if err != nil {
			t.Errorf("%s: %v", c.name, err)
			continue
		}
		if got != c.want {
			t.Errorf("%s: client_view_focus read as %v, want %v", c.name, got, c.want)
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
		"ping": {}, "client.list": {}, "client.view.focus": {},
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
