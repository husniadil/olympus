//go:build darwin || linux

package cli_test

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/husniadil/olympus"
	"github.com/husniadil/olympus/backend"
)

// §13.5 `clients` is a verb on every backend and answers UNSUPPORTED where
// the server cannot say which client shows what, rather than an empty list.
// A tag no server would hold is USAGE before anything is asked.
func TestClientsIsRefusedWhereItCannotAnswer(t *testing.T) {
	flags := isolation(t)

	got := run(t, append(flags, "clients", "--json")...)
	if got.code != 7 {
		t.Fatalf("clients on tmux exits %d, want 7 (UNSUPPORTED)\n%s%s", got.code, got.stdout, got.stderr)
	}
	if e := got.envelope(t); e.OK || e.Error == nil || e.Error.Code != backend.CodeUnsupported {
		t.Errorf("clients on tmux is %+v, want an UNSUPPORTED envelope", e)
	}

	bad := run(t, append(flags, "clients", "--tag", "a\tb", "--json")...)
	if bad.code != 2 {
		t.Errorf("clients --tag with a control character exits %d, want 2 (USAGE)\n%s%s", bad.code, bad.stdout, bad.stderr)
	}
	if e := bad.envelope(t); e.Error == nil || e.Error.Code != backend.CodeUsage {
		t.Errorf("clients --tag with a control character is %v, want USAGE", e.Error)
	}
}

// §8.10 `attach --client-tag` names the client a bare attach launches on a
// herdr server that moves one client's view, and only that client: on tmux,
// and without --bare, it is USAGE rather than a tag silently dropped.
func TestAttachClientTagIsUsageWhereNoTaggedClientIsLaunched(t *testing.T) {
	flags := isolation(t)
	session := name()
	if got := run(t, append(flags, "start", session)...); got.code != 0 {
		t.Fatalf("start exited %d: %s", got.code, got.stderr)
	}
	for _, args := range [][]string{
		{"attach", "--bare", "--client-tag", "mine", session},
		{"attach", "--client-tag", "mine", session},
	} {
		got := run(t, append(flags, args...)...)
		if got.code != 2 {
			t.Errorf("`%s` on tmux exits %d, want 2 (USAGE)\n%s%s", strings.Join(args, " "), got.code, got.stdout, got.stderr)
		}
		if !strings.Contains(got.stderr, "client tag") {
			t.Errorf("`%s` does not say what it refused: %q", strings.Join(args, " "), got.stderr)
		}
	}
}

// §8.10, §13.5 The whole door: a bare attach given a tag of the caller's, and
// `clients --tag` answering which workspace, tab and pane that client shows,
// following a pane focused on the server after it came up. Runs against a
// herdr whose server advertises `client_view_focus`, on a private socket
// under a private HOME and XDG tree (§2.9).
func TestClientsTagReportsWhatTheCallersBareClientShows(t *testing.T) {
	skipUnlessFull(t)
	bin := os.Getenv("OLYMPUS_TEST_HERDR_CLIENT_VIEW")
	if bin == "" {
		t.Skip("OLYMPUS_TEST_HERDR_CLIENT_VIEW is not set to a herdr binary whose server advertises client_view_focus")
	}
	t.Setenv("PATH", filepath.Dir(bin)+string(os.PathListSeparator)+os.Getenv("PATH"))
	home, err := os.MkdirTemp(os.TempDir(), "olyc")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(home) })
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", home)
	t.Setenv("XDG_STATE_HOME", filepath.Join(home, "state"))
	socket := filepath.Join(home, "h.sock")
	where := []string{"--backend", "herdr", "--socket-path", socket}
	on := func(args ...string) result { return run(t, append(args, where...)...) }

	// The server is started through this handle, so this handle may stop
	// it: a server the CLI started in another process is not its to stop.
	ol, err := olympus.Open(olympus.WithBackend("herdr"), olympus.WithSocketPath(socket))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		stopper, ok := ol.Raw().(interface{ Stop(context.Context) error })
		if !ok {
			t.Error("the herdr backend has no Stop, so the test server is left running")
			return
		}
		if err := stopper.Stop(ctx); err != nil {
			t.Errorf("stopping the test server: %v", err)
		}
		_ = ol.Close()
	})
	if _, err := ol.Session(context.Background(), "tagged"); err != nil {
		t.Fatalf("Session(tagged): %v", err)
	}

	if rows := clientRows(t, on("clients", "--json")); len(rows) != 0 {
		t.Fatalf("a server with no client attached lists %v", rows)
	}

	herdr := func(args ...string) string {
		cmd := exec.Command("herdr", args...)
		cmd.Env = append(os.Environ(), "HERDR_SOCKET_PATH="+socket)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("herdr %v: %v\n%s", args, err, out)
		}
		return string(out)
	}
	panes := on("panes", "tagged", "--json")
	var listed struct {
		Data []backend.Pane `json:"data"`
	}
	if err := json.Unmarshal([]byte(panes.stdout), &listed); err != nil || len(listed.Data) != 1 {
		t.Fatalf("panes tagged = %s (%v); want one pane", panes.stdout, err)
	}
	left := listed.Data[0].ID
	var split struct {
		Result struct {
			Pane struct {
				PaneID string `json:"pane_id"`
				TabID  string `json:"tab_id"`
			} `json:"pane"`
		} `json:"result"`
	}
	if out := herdr("pane", "split", left, "--direction", "right", "--no-focus"); json.Unmarshal([]byte(out), &split) != nil || split.Result.Pane.PaneID == "" {
		t.Fatalf("pane split answered no pane:\n%s", out)
	}
	right, tab := split.Result.Pane.PaneID, split.Result.Pane.TabID

	session, err := ol.Open(context.Background(), "tagged")
	if err != nil {
		t.Fatalf("Open(tagged): %v", err)
	}
	inR, inW, _ := os.Pipe()
	outR, outW, _ := os.Pipe()
	errR, errW, _ := os.Pipe()
	go func() { _, _ = io.Copy(io.Discard, outR) }()
	go func() { _, _ = io.Copy(io.Discard, errR) }()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	const tag = "browser-7"
	go func() {
		defer close(done)
		_, err := session.Attach(ctx, inR, outW, errW, olympus.AsBare(), olympus.BareClientTag(tag), olympus.AttachSize(120, 40))
		if err != nil && ctx.Err() == nil {
			t.Errorf("the tagged bare attach ended: %v", err)
		}
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			t.Error("the tagged bare attach did not end")
		}
		for _, f := range []*os.File{inR, inW, outR, outW, errR, errW} {
			_ = f.Close()
		}
	})

	shown := waitClientRow(t, on, tag, func(row map[string]any) bool {
		return row["pane_id"] == left && row["view_applied"] == true
	})
	if shown["session_id"] != listed.Data[0].SessionID || shown["window_id"] != tab || shown["zoomed"] != false || shown["id"] == "" {
		t.Errorf("clients --tag %s is %v, want id set, on %s, %s, zoomed false", tag, shown, listed.Data[0].SessionID, tab)
	}

	herdr("pane", "focus", "--pane", left, "--direction", "right")
	moved := waitClientRow(t, on, tag, func(row map[string]any) bool { return row["pane_id"] == right })
	if moved["id"] != shown["id"] || moved["window_id"] != tab {
		t.Errorf("after the pane focus clients --tag %s is %v, want client %v on %s", tag, moved, shown["id"], tab)
	}

	human := on("clients")
	if human.code != 0 || !strings.Contains(human.stdout, tag) || !strings.Contains(human.stdout, right) {
		t.Errorf("the human listing (exit %d) does not show the client:\n%s%s", human.code, human.stdout, human.stderr)
	}
	missing := on("clients", "--tag", "nobody", "--json")
	if missing.code != 3 {
		t.Errorf("clients --tag for a tag no client carries exits %d, want 3 (SESSION_NOT_FOUND)\n%s", missing.code, missing.stdout)
	}
}

// clientRows reads the rows out of a successful `clients --json`.
func clientRows(t *testing.T, got result) []map[string]any {
	t.Helper()
	if got.code != 0 {
		t.Fatalf("clients exited %d: %s%s", got.code, got.stdout, got.stderr)
	}
	var e struct {
		OK   bool             `json:"ok"`
		Data []map[string]any `json:"data"`
	}
	if err := json.Unmarshal([]byte(got.stdout), &e); err != nil || !e.OK || e.Data == nil {
		t.Fatalf("clients --json is not a successful listing (%v):\n%s", err, got.stdout)
	}
	return e.Data
}

// waitClientRow polls `clients --tag` until its one row is accepted.
func waitClientRow(t *testing.T, on func(...string) result, tag string, ok func(map[string]any) bool) map[string]any {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	last := ""
	for time.Now().Before(deadline) {
		got := on("clients", "--tag", tag, "--json")
		last = got.stdout
		if got.code == 0 {
			if rows := clientRows(t, got); len(rows) == 1 && ok(rows[0]) {
				return rows[0]
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("clients --tag %s never answered as expected; the last answer:\n%s", tag, last)
	return nil
}
