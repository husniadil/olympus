package herdr

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

// settle runs the attachment's deferred steering where there is one: on a
// herdr whose clients keep their own view the steering waits for the client
// (§8.10), and these tests have no client to paint, so they run it by hand.
func settle(t *testing.T, ctx context.Context, att backend.Attachment) {
	t.Helper()
	if att.Settle == nil {
		return
	}
	if err := att.Settle(ctx, io.Discard, met); err != nil {
		t.Fatalf("Settle: %v", err)
	}
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
	settle(t, ctx, att)
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
	other, err := b.Attach(ctx, "steer-other", backend.AttachSpec{Role: backend.RoleController, Supersede: true, SessionClient: true})
	if err != nil {
		t.Fatalf("session-client Attach(steer-other): %v", err)
	}
	settle(t, ctx, other)
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
	if string(content) != bareSessionConfig(rawConfiguredPrefix(filepath.Dir(b.socketPath))) {
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

// §8.9 The stripped config keeps the operator's prefix and the pane keys
// behind it, and unbinds what leaves the workspace.
func TestBareSessionConfigKeepsThePrefixAndThePaneKeys(t *testing.T) {
	t.Parallel()
	cfg := bareSessionConfig("ctrl+space")
	if !strings.Contains(cfg, `prefix = "ctrl+space"`) {
		t.Errorf("the config does not carry the prefix it was given:\n%s", cfg)
	}
	for _, key := range []string{"split_vertical", "split_horizontal", "close_pane", "zoom", "resize_mode", "focus_pane_left"} {
		if strings.Contains(cfg, key+" = ") {
			t.Errorf("%s is set in the bare config; a pane key keeps herdr's own binding", key)
		}
	}
	for _, key := range []string{"new_tab", "close_tab", "new_workspace", "close_workspace", "workspace_picker", "toggle_sidebar"} {
		if !strings.Contains(cfg, key+` = ""`) {
			t.Errorf("%s is not unbound; it leaves the workspace or changes what the session holds", key)
		}
	}
	if !strings.Contains(cfg, "pane_borders = true") {
		t.Errorf("pane_borders is off, so a split would draw no divider")
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
	if err := os.WriteFile(path, []byte(bareSessionConfig(defaultPrefix)), 0o600); err != nil {
		t.Fatalf("writing config: %v", err)
	}
	cmd := exec.Command("herdr", "config", "check")
	cmd.Env = append(os.Environ(), "HERDR_CONFIG_PATH="+path)
	out, _ := cmd.CombinedOutput()
	if !strings.Contains(string(out), "config: ok") {
		t.Errorf("herdr config check did not accept the stripped config:\n%s", out)
	}
}

// §8.10 A bare attach on a herdr whose clients keep their own view (0.9.0
// and up) moves nothing on the server: the client is walked onto its
// workspace with its own keys, which the attachment's Settle writes. Below
// 0.9.0, and for a client with the operator's configuration, the server is
// steered in Attach and there is nothing to settle.
func TestBareAttachWalksTheClientWhereViewsArePerClient(t *testing.T) {
	requireHerdrRunnable(t)
	b := liveBackend(t)
	ctx := context.Background()
	var ids []string
	for _, name := range []string{"first", "second", "third"} {
		created, err := b.Create(ctx, backend.CreateSpec{Name: name})
		if err != nil {
			t.Fatalf("Create: %v", err)
		}
		ids = append(ids, created.ID)
	}
	raw(t, b, "workspace", "focus", ids[1])
	version, err := b.Version(ctx)
	if err != nil {
		t.Fatalf("Version: %v", err)
	}
	spec := backend.AttachSpec{Role: backend.RoleController, Supersede: true, SessionClient: true, Bare: true}
	att, err := b.Attach(ctx, "third", spec)
	if err != nil {
		t.Fatalf("bare Attach(third): %v", err)
	}
	if att.Cleanup != nil {
		defer func() { _ = att.Cleanup() }()
	}
	ws, _, _, _ := focus(t, b)
	if sharedClientFocus(version) {
		if att.Settle != nil {
			t.Errorf("herdr %s shares one focus across clients, yet the attachment walks the client", version)
		}
		if ws != ids[2] {
			t.Errorf("herdr %s: Attach left the focus on %s, want %s", version, ws, ids[2])
		}
		return
	}
	if att.Settle == nil {
		t.Fatalf("herdr %s keeps a view per client, yet the attachment steers the server", version)
	}
	if string(att.SettleAfter) != kittyPush {
		t.Errorf("SettleAfter = %q, want the client's kitty push", att.SettleAfter)
	}
	if ws != ids[1] {
		t.Errorf("herdr %s: Attach moved the server's focus to %s, which moves every client", version, ws)
	}
	// The focus moves on before the walk runs — another client's walk, in
	// the race this holds against — and the walk still counts from where
	// the client came up, which was read when the attach was built.
	raw(t, b, "workspace", "focus", ids[0])
	var keys strings.Builder
	if err := att.Settle(ctx, &keys, met); err != nil {
		t.Fatalf("Settle: %v", err)
	}
	// The client came up on `second`, the server's focus at the attach;
	// `third` is the next one along.
	if got := keys.String(); got != nextWorkspaceKey {
		t.Errorf("the walk wrote %q, want one next-workspace press", got)
	}
	if ws, _, _, _ := focus(t, b); ws != ids[0] {
		t.Errorf("the walk moved the server's focus to %s; it must move the client alone", ws)
	}
	// The operator's own client has no keys this backend can count on: it
	// is steered on the server as before.
	plain, err := b.Attach(ctx, "first", backend.AttachSpec{Role: backend.RoleController, Supersede: true, SessionClient: true})
	if err != nil {
		t.Fatalf("Attach(first): %v", err)
	}
	if plain.Settle != nil {
		t.Error("a client with the operator's configuration was given a walk it has no keys for")
	}
	if ws, _, _, _ := focus(t, b); ws != ids[0] {
		t.Errorf("Attach(first) left the focus on %s, want %s", ws, ids[0])
	}
}

// §8.10 Bare attaches onto one server are built one at a time: the second
// waits until the first has walked, or ended without walking, so the focus
// it reads is the one its client comes up on.
func TestBareAttachesOntoOneServerWalkOneAtATime(t *testing.T) {
	requireHerdrRunnable(t)
	b := liveBackend(t)
	ctx := context.Background()
	for _, name := range []string{"first", "second"} {
		if _, err := b.Create(ctx, backend.CreateSpec{Name: name}); err != nil {
			t.Fatalf("Create: %v", err)
		}
	}
	version, err := b.Version(ctx)
	if err != nil {
		t.Fatalf("Version: %v", err)
	}
	if sharedClientFocus(version) {
		t.Skipf("herdr %s shares one focus across clients; nothing walks", version)
	}
	spec := backend.AttachSpec{Role: backend.RoleController, Supersede: true, SessionClient: true, Bare: true}
	attach := func(target string) (backend.Attachment, error) {
		att, err := b.Attach(ctx, target, spec)
		if err == nil && att.Cleanup != nil {
			t.Cleanup(func() { _ = att.Cleanup() })
		}
		return att, err
	}
	first, err := attach("first")
	if err != nil {
		t.Fatalf("Attach(first): %v", err)
	}
	type built struct {
		att backend.Attachment
		err error
	}
	second := make(chan built, 1)
	go func() {
		att, err := attach("second")
		second <- built{att, err}
	}()
	select {
	case got := <-second:
		t.Fatalf("the second bare attach was built (err %v) while the first had not walked", got.err)
	case <-time.After(400 * time.Millisecond):
	}
	if err := first.Settle(ctx, io.Discard, met); err != nil {
		t.Fatalf("Settle(first): %v", err)
	}
	var next backend.Attachment
	select {
	case got := <-second:
		if got.err != nil {
			t.Fatalf("Attach(second): %v", got.err)
		}
		next = got.att
	case <-time.After(5 * time.Second):
		t.Fatal("the second bare attach did not go on once the first had walked")
	}
	// A client that ends before it settles drops the lock on its cleanup.
	if err := next.Cleanup(); err != nil {
		t.Fatalf("Cleanup(second): %v", err)
	}
	third := make(chan built, 1)
	go func() {
		att, err := attach("first")
		third <- built{att, err}
	}()
	select {
	case got := <-third:
		if got.err != nil {
			t.Fatalf("Attach(first) again: %v", got.err)
		}
		if err := got.att.Settle(ctx, io.Discard, met); err != nil {
			t.Fatalf("Settle: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("a bare attach did not go on once the last one was cleaned up unwalked")
	}
}

// §8.10 A go walks the bare client from where it IS, not from the server's
// focus, and the probe follows it: the attach ends with the target the
// client is on, not the one it was made for.
func TestAGoWalksTheBareClientFromWhereItIsAndTheProbeFollows(t *testing.T) {
	requireHerdrRunnable(t)
	b := liveBackend(t)
	ctx := context.Background()
	var ids []string
	for _, name := range []string{"first", "second", "third"} {
		created, err := b.Create(ctx, backend.CreateSpec{Name: name})
		if err != nil {
			t.Fatalf("Create: %v", err)
		}
		ids = append(ids, created.ID)
	}
	raw(t, b, "workspace", "focus", ids[1])
	version, err := b.Version(ctx)
	if err != nil {
		t.Fatalf("Version: %v", err)
	}
	if sharedClientFocus(version) {
		t.Skipf("herdr %s shares one focus across clients; nothing walks", version)
	}
	spec := backend.AttachSpec{Role: backend.RoleController, Supersede: true, SessionClient: true, Bare: true}
	att, err := b.Attach(ctx, "third", spec)
	if err != nil {
		t.Fatalf("Attach(third): %v", err)
	}
	defer func() { _ = att.Cleanup() }()
	if att.Go == nil {
		t.Fatal("a bare client on a per-view herdr cannot be moved")
	}
	if err := att.Settle(ctx, io.Discard, met); err != nil {
		t.Fatalf("Settle: %v", err)
	}
	// The server's focus is elsewhere; the client is on `third`, and
	// `first` is the next one along the ring from there.
	raw(t, b, "workspace", "focus", ids[1])
	var keys strings.Builder
	if err := att.Go(ctx, "first", &keys, met); err != nil {
		t.Fatalf("Go(first): %v", err)
	}
	if got := keys.String(); got != nextWorkspaceKey {
		t.Errorf("the go wrote %q, want one next-workspace press (third → first round the ring)", got)
	}
	if err := att.Go(ctx, "nowhere", io.Discard, met); err == nil {
		t.Error("a go onto a target that does not exist was not refused")
	}
	// The workspace the attach was FOR closes: the client is on `first`
	// now, so the attach lives.
	raw(t, b, "workspace", "close", ids[2])
	if got := att.Probe(ctx); got != backend.StatePresent {
		t.Errorf("after closing the attach's own target the probe answered %v; the client is on first", got)
	}
	raw(t, b, "workspace", "close", ids[0])
	if got := att.Probe(ctx); got != backend.StateAbsent {
		t.Errorf("after closing the workspace the client is on the probe answered %v, want absent", got)
	}
}

// met stands in for the client's answer where the keys go into a buffer
// rather than a client: every press is confirmed at once.
func met([]byte) func(time.Duration) bool { return func(time.Duration) bool { return true } }

// §8.10 The walk takes the shorter way round the ring the workspace keys
// step through, in the order of the workspaces' numbers.
func TestWorkspaceStepsTakeTheShorterWayRound(t *testing.T) {
	rows := []workspaceRow{
		{WorkspaceID: "w3", Number: 3},
		{WorkspaceID: "w1", Number: 1},
		{WorkspaceID: "w4", Number: 4},
		{WorkspaceID: "w2", Number: 2},
		{WorkspaceID: "w5", Number: 5},
	}
	cases := []struct {
		from, to string
		want     int
	}{
		{"w1", "w1", 0},
		{"w1", "w2", 1},
		{"w1", "w3", 2},
		{"w1", "w4", -2},
		{"w1", "w5", -1},
		{"w5", "w1", 1},
		{"w4", "w2", -2},
		{"w1", "w9", 0},
		{"w9", "w1", 0},
	}
	for _, c := range cases {
		if got := workspaceSteps(rows, c.from, c.to); got != c.want {
			t.Errorf("workspaceSteps(%s → %s) = %d, want %d", c.from, c.to, got, c.want)
		}
	}
}
