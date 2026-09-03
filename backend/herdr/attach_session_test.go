package herdr

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/husniadil/olympus/backend"
)

// envValue returns the value of the LAST occurrence of key in env, matching how
// exec resolves a duplicated variable (later wins).
func envValue(env []string, key string) (string, bool) {
	val, ok := "", false
	for _, kv := range env {
		if strings.HasPrefix(kv, key+"=") {
			val, ok = kv[len(key)+1:], true
		}
	}
	return val, ok
}

// focus reads where the server's focus is right now.
func focus(t *testing.T, b *Herdr) (workspace, tab, pane string, zoomed bool) {
	t.Helper()
	out := raw(t, b, "api", "snapshot")
	snap, err := parseSnapshot(out)
	if err != nil {
		t.Fatalf("reading the snapshot: %v", err)
	}
	for _, layout := range snap.Layouts {
		if layout.TabID == snap.FocusedTabID {
			zoomed = layout.Zoomed
		}
	}
	return snap.FocusedWorkspaceID, snap.FocusedTabID, snap.FocusedPaneID, zoomed
}

// §8.10 A session-client attach onto a PANE steers the server onto it before
// the client is spawned: the workspace is focused, the tab within it, and the
// pane is zoomed within the tab. The effect is on the server, so it is
// observable without a terminal — and it IS a server call, which an earlier
// revision of this test asserted the session client never made. It does now,
// deliberately: the client shows what the server has focused and takes no
// target of its own.
//
// The pane chosen is one the server was NOT showing — the second pane of a
// split, in a workspace that was not focused — so the steering has something to
// move.
func TestSessionClientAttachSteersOntoThePane(t *testing.T) {
	requireHerdrRunnable(t)
	b := liveBackend(t)
	ctx := context.Background()

	if _, err := b.Create(ctx, backend.CreateSpec{Name: "steer-other"}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	created, err := b.Create(ctx, backend.CreateSpec{Name: "steer"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	raw(t, b, "workspace", "focus", "w1")
	panes, err := b.Panes(ctx, created.Name)
	if err != nil || len(panes) != 1 {
		t.Fatalf("Panes(%s) = %v, %v; want one pane", created.Name, panes, err)
	}
	split := raw(t, b, "pane", "split", panes[0].ID, "--direction", "right", "--no-focus")
	var reply struct {
		Result struct {
			Pane paneRow `json:"pane"`
		} `json:"result"`
	}
	if err := json.Unmarshal([]byte(split), &reply); err != nil || reply.Result.Pane.PaneID == "" {
		t.Fatalf("pane split answered no pane id: %v\n%s", err, split)
	}
	target := reply.Result.Pane.PaneID

	att, err := b.Attach(ctx, target, backend.AttachSpec{
		Role:          backend.RoleController,
		Supersede:     true,
		SessionClient: true,
	})
	if err != nil {
		t.Fatalf("session-client Attach(%s): %v", target, err)
	}
	ws, tab, pane, zoomed := focus(t, b)
	if ws != created.ID || tab != reply.Result.Pane.TabID || pane != target || !zoomed {
		t.Errorf("after steering onto %s the server shows workspace %s, tab %s, pane %s (zoomed %v); want %s, %s, %s, zoomed",
			target, ws, tab, pane, zoomed, created.ID, reply.Result.Pane.TabID, target)
	}

	// A path-addressed server has no named session to attach, so the client
	// is plain `herdr` pointed at the socket, with this backend's own
	// configuration directory since this handle started the server.
	if got := att.Cmd.Args; len(got) != 1 || got[0] != "herdr" {
		t.Errorf("cmd args = %v, want [herdr]", got)
	}
	if v, _ := envValue(att.Cmd.Env, "HERDR_SOCKET_PATH"); v != b.Scope() {
		t.Errorf("the client's HERDR_SOCKET_PATH is %q, want %q", v, b.Scope())
	}
	if att.Cleanup != nil {
		t.Error("a non-bare session attach left a cleanup to run, but writes no temp file")
	}
	// The ambient HERDR_* identity is stripped so the client does not detect
	// it is launched from inside a pane.
	for _, key := range []string{"HERDR_PANE_ID", "HERDR_TAB_ID", "HERDR_WORKSPACE_ID", "HERDR_SESSION", "HERDR_CLIENT_SOCKET_PATH"} {
		if _, ok := envValue(att.Cmd.Env, key); ok {
			t.Errorf("session attach env carries %s; it must be stripped", key)
		}
	}
	if _, ok := envValue(att.Cmd.Env, "HERDR_CONFIG_PATH"); ok {
		t.Error("a non-bare session attach set HERDR_CONFIG_PATH; only --bare should")
	}

	// A workspace target steers onto the workspace alone: the tab and the
	// zoom are left where they were.
	if _, err := b.Attach(ctx, "steer-other", backend.AttachSpec{Role: backend.RoleController, Supersede: true, SessionClient: true}); err != nil {
		t.Fatalf("session-client Attach(steer-other): %v", err)
	}
	if ws, _, _, _ := focus(t, b); ws == created.ID {
		t.Errorf("attaching the other workspace left the focus on %s", ws)
	}
}

// §8.10 A server selected BY NAME has a named session to attach, and the
// session client attaches it by that name against the operator's
// configuration directory. The steering still runs, against the named
// server's socket.
func TestSessionClientAttachOnANamedServerAttachesByName(t *testing.T) {
	requireHerdrRunnable(t)
	owner := liveBackend(t)
	ctx := context.Background()
	created, err := owner.Create(ctx, backend.CreateSpec{Name: "named"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	named := New(WithServerSocket("work", owner.Scope()))
	att, err := named.Attach(ctx, created.Name, backend.AttachSpec{
		Role:          backend.RoleController,
		Supersede:     true,
		SessionClient: true,
	})
	if err != nil {
		t.Fatalf("session-client Attach on a named server: %v", err)
	}
	if got := att.Cmd.Args; len(got) != 4 || got[1] != "session" || got[2] != "attach" || got[3] != "work" {
		t.Fatalf("cmd args = %v, want [herdr session attach work]", got)
	}
	if ws, _, _, _ := focus(t, owner); ws != created.ID {
		t.Errorf("the named server was not steered onto %s; focus is on %s", created.ID, ws)
	}
	if v, ok := envValue(att.Cmd.Env, "XDG_CONFIG_HOME"); ok && strings.Contains(v, owner.StateHome()) {
		t.Errorf("a named-session attach imposes Olympus's own configuration directory: %s", v)
	}
}

// §8.10 A bare session-client attach writes the stripped config to a temp
// file, points HERDR_CONFIG_PATH at it, and reaps it on Close.
func TestBareSessionClientAttachWritesStrippedConfig(t *testing.T) {
	requireHerdrRunnable(t)
	b := liveBackend(t)
	ctx := context.Background()
	created, err := b.Create(ctx, backend.CreateSpec{Name: "bare"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	att, err := b.Attach(ctx, created.Name, backend.AttachSpec{
		Role:          backend.RoleController,
		Supersede:     true,
		SessionClient: true,
		Bare:          true,
	})
	if err != nil {
		t.Fatalf("bare session-client Attach: %v", err)
	}
	path, ok := envValue(att.Cmd.Env, "HERDR_CONFIG_PATH")
	if !ok {
		t.Fatal("a bare session attach did not set HERDR_CONFIG_PATH")
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading the stripped config at %s: %v", path, err)
	}
	if string(content) != bareSessionConfig {
		t.Errorf("the written config does not match bareSessionConfig")
	}
	if att.Cleanup == nil {
		t.Fatal("a bare session attach left no cleanup to reap the temp config")
	}
	if err := att.Close(); err != nil {
		t.Errorf("closing the bare attach: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("the temp config survived Close: stat err = %v", err)
	}
}

// herdr's session client has no co-attach control, so an explicit opt-out of
// supersession is reported rather than silently dropped.
func TestSessionClientAttachReportsUnhonoredKeepOthers(t *testing.T) {
	requireHerdrRunnable(t)
	b := liveBackend(t)
	ctx := context.Background()
	created, err := b.Create(ctx, backend.CreateSpec{Name: "keep"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	att, err := b.Attach(ctx, created.Name, backend.AttachSpec{
		Role:          backend.RoleController,
		Supersede:     false,
		SessionClient: true,
	})
	if err != nil {
		t.Fatalf("session-client Attach: %v", err)
	}
	if len(att.Notices) == 0 {
		t.Fatal("--keep-others on a session attach produced no notice")
	}
	joined := strings.Join(att.Notices, " ")
	if !strings.Contains(joined, "keep-others") {
		t.Errorf("notice does not mention the unhonored option: %q", joined)
	}
}

// §8.10 A session-client attach onto nothing is not-found before any client is
// spawned, the same gate the raw attach has (§8.1). Asserted against a socket
// no server could answer on.
func TestSessionClientAttachOntoNothingIsNotFound(t *testing.T) {
	t.Parallel()
	b := New(WithSocketPath(filepath.Join(shortDir(t), "h.sock")))
	_, err := b.Attach(context.Background(), "nobody", backend.AttachSpec{
		Role:          backend.RoleController,
		Supersede:     true,
		SessionClient: true,
	})
	if backend.CodeOf(err) != backend.CodeSessionNotFound {
		t.Errorf("a session-client attach onto nothing is %q, want %q", backend.CodeOf(err), backend.CodeSessionNotFound)
	}
}

// The stripped config is what herdr accepts: `herdr config check` reports it ok.
// Guarded on the binary being installed, so a host without herdr still passes.
func TestBareSessionConfigValidatesAgainstHerdr(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("herdr"); err != nil {
		t.Skip("herdr not installed")
	}
	dir := shortDir(t)
	path := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(path, []byte(bareSessionConfig), 0o600); err != nil {
		t.Fatalf("writing config: %v", err)
	}
	cmd := exec.Command("herdr", "config", "check")
	cmd.Env = append(os.Environ(), "HERDR_CONFIG_PATH="+path)
	out, _ := cmd.CombinedOutput()
	if !strings.Contains(string(out), "config: ok") {
		t.Errorf("herdr config check did not accept the stripped config:\n%s", out)
	}
}
