//go:build darwin || linux

package herdr

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/husniadil/olympus/backend"
	"github.com/husniadil/olympus/internal/engine"
)

// clientViewHerdrEnv names a herdr binary whose server advertises
// `client_view_focus`, for the leg that drives a bare client the way such a
// server allows (§8.10). The suite otherwise runs whatever herdr is on PATH,
// and a released herdr without the capability takes the walk.
const clientViewHerdrEnv = "OLYMPUS_TEST_HERDR_CLIENT_VIEW"

// requireClientViewHerdr puts the binary clientViewHerdrEnv names first on
// PATH for this test, or skips saying how to run the leg. A binary that is
// named and does not run, or whose server does not advertise the
// capability, fails rather than skips: somebody asked for this leg.
//
// PATH is set for the test's life with t.Setenv, which keeps the test off
// the parallel schedule; every herdr invocation this backend makes finds the
// binary by name.
func requireClientViewHerdr(t *testing.T) *Herdr {
	t.Helper()
	if testing.Short() {
		t.Skip("driving a real multiplexer; run `make test-full` for this")
	}
	path := os.Getenv(clientViewHerdrEnv)
	if path == "" {
		t.Skipf("%s is not set to a herdr binary whose server advertises client_view_focus, so the per-client view leg is not being run", clientViewHerdrEnv)
	}
	if filepath.Base(path) != "herdr" {
		t.Fatalf("%s=%s: the binary must be named herdr, since every invocation finds it by that name", clientViewHerdrEnv, path)
	}
	if out, err := exec.Command(path, "--version").CombinedOutput(); err != nil {
		t.Fatalf("%s=%s does not run: %v\n%s", clientViewHerdrEnv, path, err, out)
	}
	t.Setenv("PATH", filepath.Dir(path)+string(os.PathListSeparator)+os.Getenv("PATH"))
	b := liveBackend(t)
	views, err := b.clientViews(context.Background())
	if err != nil {
		t.Fatalf("asking the server for its capabilities: %v", err)
	}
	if !views {
		t.Fatalf("%s=%s: its server does not advertise client_view_focus", clientViewHerdrEnv, path)
	}
	return b
}

// A liveClient is a bare attach running under the engine, as a consumer
// runs one: its input a pipe that carries the in-band controls.
type liveClient struct {
	tag    string
	in     *os.File
	cancel context.CancelFunc
	// done is closed once the engine has returned.
	done chan struct{}
}

func startBare(t *testing.T, b *Herdr, target string) *liveClient {
	t.Helper()
	return startBareTagged(t, b, target, "")
}

// startBareTagged is startBare with the client's tag chosen by the caller;
// an empty tag leaves it to the backend.
func startBareTagged(t *testing.T, b *Herdr, target, tag string) *liveClient {
	t.Helper()
	spec := backend.AttachSpec{Role: backend.RoleController, Supersede: true, SessionClient: true, Bare: true, Cols: 120, Rows: 40, ClientTag: tag}
	ctx, cancel := context.WithCancel(context.Background())
	att, err := b.Attach(ctx, target, spec)
	if err != nil {
		cancel()
		t.Fatalf("bare Attach(%s): %v", target, err)
	}
	c := &liveClient{tag: flagValue(att.Cmd.Args, "--client-tag"), cancel: cancel, done: make(chan struct{})}
	inR, inW, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	outR, outW, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	c.in = inW
	tail := &outputTail{}
	go func() { _, _ = io.Copy(tail, outR) }()
	go func() {
		defer close(c.done)
		code, err := engine.Attach(ctx, att, engine.AttachIO{In: inR, Out: outW, Err: testLog{t, c.tag}}, spec, nil)
		if err != nil || ctx.Err() == nil {
			t.Logf("the bare client tagged %s ended with status %d: %v; the last it painted: %q", c.tag, code, err, tail.String())
		}
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-c.done:
		case <-time.After(10 * time.Second):
			t.Errorf("the bare client tagged %s did not end", c.tag)
		}
		_ = inW.Close()
		_ = inR.Close()
		_ = outW.Close()
		_ = outR.Close()
	})
	return c
}

// outputTail keeps the last of what a client painted, for the log of a
// client that ended on its own.
type outputTail struct {
	mu  sync.Mutex
	buf []byte
}

func (o *outputTail) Write(p []byte) (int, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.buf = append(o.buf, p...)
	if len(o.buf) > 2048 {
		o.buf = o.buf[len(o.buf)-2048:]
	}
	return len(p), nil
}

func (o *outputTail) String() string {
	o.mu.Lock()
	defer o.mu.Unlock()
	return string(o.buf)
}

func flagValue(args []string, flag string) string {
	for i, a := range args {
		if a == flag && i+1 < len(args) {
			return args[i+1]
		}
	}
	return ""
}

// waitClientOn waits for the tagged client to be listed on a workspace.
func waitClientOn(t *testing.T, b *Herdr, c *liveClient, workspace string) clientRow {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	var last clientRow
	for time.Now().Before(deadline) {
		select {
		case <-c.done:
			t.Fatalf("the bare client tagged %s ended while waiting for it on %s", c.tag, workspace)
		default:
		}
		row, ok, err := b.taggedClient(context.Background(), c.tag)
		if err != nil {
			t.Fatalf("client.list: %v", err)
		}
		if ok && row.WorkspaceID == workspace {
			return row
		}
		last = row
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("the client tagged %s is on %q, not %s", c.tag, last.WorkspaceID, workspace)
	return clientRow{}
}

// waitScreen waits for text on a target's screen.
func waitScreen(t *testing.T, b *Herdr, target, text string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	var screen string
	for time.Now().Before(deadline) {
		capture, err := b.Screen(context.Background(), target, backend.ScreenOpts{})
		if err == nil {
			screen = capture.Text
			if strings.Contains(screen, text) {
				return
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	clients, _ := b.call(context.Background(), "client.list", struct{}{})
	t.Fatalf("%s never showed %q; the clients: %s; its screen:\n%s", target, text, clients, screen)
}

func screenShows(t *testing.T, b *Herdr, target, text string) bool {
	t.Helper()
	capture, err := b.Screen(context.Background(), target, backend.ScreenOpts{})
	if err != nil {
		t.Fatalf("Screen(%s): %v", target, err)
	}
	return strings.Contains(capture.Text, text)
}

func goControl(target string) string { return "\x1b]olympus;go;" + target + "\x07" }

// §8.10 On a server that moves one client's view, a bare client is launched
// onto its workspace and named, and nothing on the server moves: not its
// focus, not another client. No key is pressed and no title is waited for.
func TestAPerClientViewBareAttachLaunchesOntoItsWorkspace(t *testing.T) {
	b := requireClientViewHerdr(t)
	ctx := context.Background()
	var ids []string
	for _, name := range []string{"first", "second", "third"} {
		created, err := b.Create(ctx, backend.CreateSpec{Name: name})
		if err != nil {
			t.Fatalf("Create: %v", err)
		}
		ids = append(ids, created.ID)
	}
	raw(t, b, "workspace", "focus", ids[0])

	spec := backend.AttachSpec{Role: backend.RoleController, Supersede: true, SessionClient: true, Bare: true}
	att, err := b.Attach(ctx, "third", spec)
	if err != nil {
		t.Fatalf("bare Attach(third): %v", err)
	}
	defer func() { _ = att.Close() }()
	if got := flagValue(att.Cmd.Args, "--workspace"); got != ids[2] {
		t.Errorf("the client is launched with --workspace %q, want %s (args %v)", got, ids[2], att.Cmd.Args)
	}
	if tag := flagValue(att.Cmd.Args, "--client-tag"); !strings.HasPrefix(tag, clientTagPrefix) {
		t.Errorf("the client is launched with --client-tag %q, want one beginning %s (args %v)", tag, clientTagPrefix, att.Cmd.Args)
	}
	if ws, _, _, _ := focus(t, b); ws != ids[0] {
		t.Errorf("building the attach moved the server's focus to %s", ws)
	}

	c := startBare(t, b, "third")
	waitClientOn(t, b, c, ids[2])
	if ws, _, _, _ := focus(t, b); ws != ids[0] {
		t.Errorf("the client coming up moved the server's focus to %s", ws)
	}
	// Typed input lands in the workspace the client came up on.
	_, _ = c.in.WriteString("echo olympus-landed-third\r")
	waitScreen(t, b, ids[2], "olympus-landed-third")
}

// §8.10 A go on such a server moves the client by its tag and presses no
// key: the keys writer stays empty and no title is ever expected, only the
// frame the client paints once it knows where it is.
func TestAPerClientViewGoPressesNoKey(t *testing.T) {
	b := requireClientViewHerdr(t)
	ctx := context.Background()
	for _, name := range []string{"first", "second"} {
		if _, err := b.Create(ctx, backend.CreateSpec{Name: name}); err != nil {
			t.Fatalf("Create: %v", err)
		}
	}
	spec := backend.AttachSpec{Role: backend.RoleController, Supersede: true, SessionClient: true, Bare: true, Cols: 120, Rows: 40}
	att, err := b.Attach(ctx, "first", spec)
	if err != nil {
		t.Fatalf("bare Attach(first): %v", err)
	}
	if att.Go == nil || att.Probe == nil {
		t.Fatal("a bare attach on a per-client-view server cannot be moved or probed")
	}
	inR, inW, _ := os.Pipe()
	outR, outW, _ := os.Pipe()
	defer func() { _ = inW.Close(); _ = inR.Close(); _ = outW.Close(); _ = outR.Close() }()
	go func() { _, _ = io.Copy(io.Discard, outR) }()
	runCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = engine.Attach(runCtx, att, engine.AttachIO{In: inR, Out: outW, Err: io.Discard}, spec, nil)
	}()
	defer func() { cancel(); <-done }()
	c := &liveClient{tag: flagValue(att.Cmd.Args, "--client-tag"), done: done}
	first, _ := b.resolve(ctx, "first")
	second, _ := b.resolve(ctx, "second")
	waitClientOn(t, b, c, first.workspace.WorkspaceID)

	var keys strings.Builder
	// The repaint's frame may be waited for; a window title may not, since
	// there is none to wait for on this path.
	noTitle := func(mark []byte) func(time.Duration) bool {
		if string(mark) != frameEnd {
			t.Errorf("the go waited for %q from the client", mark)
		}
		return func(time.Duration) bool { return true }
	}
	if err := att.Go(ctx, "second", &keys, noTitle); err != nil {
		t.Fatalf("Go(second): %v", err)
	}
	if keys.Len() != 0 {
		t.Errorf("the go pressed %q", keys.String())
	}
	row, ok, err := b.taggedClient(ctx, c.tag)
	if err != nil || !ok || row.WorkspaceID != second.workspace.WorkspaceID {
		t.Errorf("after Go(second) returned the client is %+v (%v, %v), want on %s", row, ok, err, second.workspace.WorkspaceID)
	}
	if err := att.Go(ctx, "nowhere", &keys, noTitle); backend.CodeOf(err) != backend.CodeSessionNotFound {
		t.Errorf("a go onto a target that does not exist is %q (%v), want %q", backend.CodeOf(err), err, backend.CodeSessionNotFound)
	}
}

// §8.10 The two-client case the walk could not hold: with two bare clients
// on one server, herdr paints the foreground client the title of the
// SERVER's focus and skips a title it has already sent, so a press that
// landed painted nothing, was made again, and every later go landed one
// workspace off. With the client moved by its tag, each go lands where it
// was asked, every time, the other client stays where it is, the server's
// focus does not move, and what is typed after a go lands in the workspace
// the go took the client to.
func TestTwoPerClientViewBareClientsEachLandWhereTheyAreSent(t *testing.T) {
	twoBareClientsEachLandWhereTheyAreSent(t, requireClientViewHerdr(t))
}

// §8.10, §13.2 The same, on a server selected by NAME: the client is herdr's
// `session attach <name>` with the launch options after the name, and it
// resolves the session under the configuration tree rather than the socket.
// The named server is brought up under a private HOME and configuration
// tree, never the operator's.
func TestTwoPerClientViewBareClientsOnANamedServerEachLandWhereTheyAreSent(t *testing.T) {
	b := namedClientViewServer(t, "n")
	created, err := b.Create(context.Background(), backend.CreateSpec{Name: "probe"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	att, err := b.Attach(context.Background(), "probe", backend.AttachSpec{Role: backend.RoleController, Supersede: true, SessionClient: true, Bare: true})
	if err != nil {
		t.Fatalf("bare Attach(probe) on the named server: %v", err)
	}
	args := att.Cmd.Args
	_ = att.Close()
	if len(args) < 4 || args[1] != "session" || args[2] != "attach" || args[3] != "n" ||
		flagValue(args, "--workspace") != created.ID || !strings.HasPrefix(flagValue(args, "--client-tag"), clientTagPrefix) {
		t.Fatalf("the client is launched as %v, want [herdr session attach n --workspace %s --client-tag %s…]", args, created.ID, clientTagPrefix)
	}
	if v, ok := envValue(att.Cmd.Env, "HERDR_SOCKET_PATH"); ok {
		t.Errorf("the named client carries a socket override %s", v)
	}
	raw(t, b, "workspace", "close", created.ID)
	twoBareClientsEachLandWhereTheyAreSent(t, b)
}

// namedClientViewServer brings up a named herdr server (`herdr --session
// <name> server`) under a private HOME and XDG tree, finds it by name the
// way `--server` does, and stops it afterwards.
func namedClientViewServer(t *testing.T, name string) *Herdr {
	t.Helper()
	requireClientViewHerdr(t)
	home := shortDir(t)
	t.Setenv("HOME", home)
	// The configuration home is HOME itself: a deeper one puts herdr's
	// derived client socket over the platform's path budget.
	t.Setenv("XDG_CONFIG_HOME", home)
	t.Setenv("XDG_STATE_HOME", filepath.Join(home, "state"))
	dir := filepath.Join(home, "herdr", "sessions", name)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("preparing %s: %v", dir, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	socket := filepath.Join(dir, "herdr.sock")
	if err := (&Herdr{}).StartServer(ctx, backend.Server{Name: name, SocketPath: socket}); err != nil {
		t.Fatalf("starting the named server: %v", err)
	}
	t.Cleanup(func() {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer stopCancel()
		if err := (&Herdr{}).StopServer(stopCtx, name); err != nil {
			t.Errorf("stopping the named server %s: %v", name, err)
		}
	})
	found, err := LookupServer(ctx, name)
	if err != nil {
		t.Fatalf("looking the named server up: %v", err)
	}
	if found.SocketPath != socket || !found.Running {
		t.Fatalf("the named server is listed as %+v, want running on %s", found, socket)
	}
	b := New(WithServerSocket(name, found.SocketPath))
	views, err := b.clientViews(ctx)
	if err != nil || !views {
		t.Fatalf("the named server does not advertise client_view_focus (%v, %v)", views, err)
	}
	return b
}

func twoBareClientsEachLandWhereTheyAreSent(t *testing.T, b *Herdr) {
	t.Helper()
	ctx := context.Background()
	names := []string{"first", "second", "third"}
	ids := map[string]string{}
	for _, name := range names[:2] {
		created, err := b.Create(ctx, backend.CreateSpec{Name: name})
		if err != nil {
			t.Fatalf("Create: %v", err)
		}
		ids[name] = created.ID
	}
	raw(t, b, "workspace", "focus", ids["first"])
	a := startBare(t, b, "first")
	waitClientOn(t, b, a, ids["first"])
	bb := startBare(t, b, "second")
	waitClientOn(t, b, bb, ids["second"])

	// A third workspace, made while both clients are up.
	created, err := b.Create(ctx, backend.CreateSpec{Name: "third"})
	if err != nil {
		t.Fatalf("Create(third): %v", err)
	}
	ids["third"] = created.ID
	serverFocus, _, _, _ := focus(t, b)

	on := map[*liveClient]string{a: "first", bb: "second"}
	clients := map[*liveClient]string{a: "a", bb: "b"}
	moves := []struct {
		c  *liveClient
		to string
	}{
		{a, "third"}, {bb, "first"}, {a, "second"}, {bb, "third"},
		{a, "first"}, {bb, "second"}, {a, "third"}, {bb, "first"},
		{a, "second"}, {bb, "third"}, {a, "first"}, {bb, "second"},
	}
	for i, m := range moves {
		marker := fmt.Sprintf("olympus-marker-%s-%d", clients[m.c], i)
		// The go and the marker in one write, as a consumer's stream carries
		// them: the marker is forwarded only once the client is on the
		// target.
		target := m.to
		if i%2 == 1 {
			target = ids[m.to]
		}
		if _, err := m.c.in.WriteString(goControl(target) + "echo " + marker + "\r"); err != nil {
			t.Fatalf("writing move %d: %v", i, err)
		}
		waitScreen(t, b, ids[m.to], marker)
		waitClientOn(t, b, m.c, ids[m.to])
		on[m.c] = m.to
		for other, at := range on {
			if other == m.c {
				continue
			}
			row, ok, err := b.taggedClient(ctx, other.tag)
			if err != nil || !ok || row.WorkspaceID != ids[at] {
				t.Errorf("move %d (%s to %s) moved client %s to %+v (%v, %v), want it still on %s",
					i, clients[m.c], m.to, clients[other], row, ok, err, at)
			}
		}
		for _, name := range names {
			if name != m.to && screenShows(t, b, ids[name], marker) {
				t.Errorf("move %d: %s typed after going to %s landed in %s", i, marker, m.to, name)
			}
		}
		if ws, _, _, _ := focus(t, b); ws != serverFocus {
			t.Errorf("move %d (%s to %s) moved the server's focus from %s to %s", i, clients[m.c], m.to, serverFocus, ws)
		}
	}
}

// §8.10 A go onto a pane shows the pane's tab on that client alone and zooms
// the pane, which is the tab's own state: the other client stays where it
// is, and the probe follows the client, so closing the workspace the attach
// was made for does not end it.
func TestAPerClientViewGoOntoAPaneShowsItsTabAndTheProbeFollows(t *testing.T) {
	b := requireClientViewHerdr(t)
	ctx := context.Background()
	ids := map[string]string{}
	for _, name := range []string{"first", "second", "third"} {
		created, err := b.Create(ctx, backend.CreateSpec{Name: name})
		if err != nil {
			t.Fatalf("Create: %v", err)
		}
		ids[name] = created.ID
	}
	raw(t, b, "workspace", "focus", ids["first"])
	panes, err := b.Panes(ctx, "third")
	if err != nil || len(panes) != 1 {
		t.Fatalf("Panes(third) = %v, %v; want one pane", panes, err)
	}
	var tabbed struct {
		Result struct {
			Tab  tabRow  `json:"tab"`
			Root paneRow `json:"root_pane"`
		} `json:"result"`
	}
	out := raw(t, b, "tab", "create", "--workspace", ids["third"], "--no-focus")
	if err := json.Unmarshal([]byte(out), &tabbed); err != nil || tabbed.Result.Tab.TabID == "" {
		t.Fatalf("tab create answered no tab: %v\n%s", err, out)
	}
	var split struct {
		Result struct {
			Pane paneRow `json:"pane"`
		} `json:"result"`
	}
	out = raw(t, b, "pane", "split", tabbed.Result.Root.PaneID, "--direction", "right", "--no-focus")
	if err := json.Unmarshal([]byte(out), &split); err != nil || split.Result.Pane.PaneID == "" {
		t.Fatalf("pane split answered no pane: %v\n%s", err, out)
	}
	target := split.Result.Pane.PaneID

	a := startBare(t, b, "first")
	waitClientOn(t, b, a, ids["first"])
	other := startBare(t, b, "second")
	waitClientOn(t, b, other, ids["second"])
	// The zoom focuses the pane, and herdr moves its own focus with it; no
	// client moves with that focus, which is what is held here.
	_, _ = a.in.WriteString(goControl(target) + "echo olympus-on-the-pane\r")
	waitScreen(t, b, target, "olympus-on-the-pane")
	row := waitClientOn(t, b, a, ids["third"])
	if row.TabID != tabbed.Result.Tab.TabID {
		t.Errorf("the client shows tab %s, want the pane's tab %s", row.TabID, tabbed.Result.Tab.TabID)
	}
	snap, err := b.snapshot(ctx)
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	if !snap.zoomedOf(tabbed.Result.Tab.TabID) {
		t.Errorf("the pane's tab is not zoomed onto it")
	}
	if got, ok, _ := b.taggedClient(ctx, other.tag); !ok || got.WorkspaceID != ids["second"] {
		t.Errorf("the go onto a pane moved the other client to %+v", got)
	}

	// The workspace the attach was made for closes; the client is on third,
	// so the attach lives.
	raw(t, b, "workspace", "close", ids["first"])
	time.Sleep(3 * engine.TargetPollInterval)
	select {
	case <-a.done:
		t.Fatal("closing the attach's first target ended it; the client is on third")
	default:
	}
	// The workspace it IS on closes: the attach ends.
	raw(t, b, "workspace", "close", ids["third"])
	select {
	case <-a.done:
	case <-time.After(10 * time.Second):
		t.Fatal("closing the workspace the client is on did not end the attach")
	}
}

// splitRight splits a pane without focusing the new one, and returns the new
// pane's id.
func splitRight(t *testing.T, b *Herdr, pane string) string {
	t.Helper()
	var split struct {
		Result struct {
			Pane paneRow `json:"pane"`
		} `json:"result"`
	}
	out := raw(t, b, "pane", "split", pane, "--direction", "right", "--no-focus")
	if err := json.Unmarshal([]byte(out), &split); err != nil || split.Result.Pane.PaneID == "" {
		t.Fatalf("pane split answered no pane: %v\n%s", err, out)
	}
	return split.Result.Pane.PaneID
}

// zoomedOnto reports whether a pane's tab is zoomed with that pane focused,
// which is what the server accepts a client's input for and nothing else.
func zoomedOnto(t *testing.T, b *Herdr, pane string) bool {
	t.Helper()
	snap, err := b.snapshot(context.Background())
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	row, ok := snap.paneByID(pane)
	return ok && snap.zoomedOf(row.TabID) && snap.focusedPaneOf(row.TabID).PaneID == pane
}

// §8.10 A bare attach onto a pane of its workspace's shown tab zooms the pane
// before the client is spawned: the client addresses what it sends to the
// pane its own copy of the tab has focused, and herdr drops input for any
// other pane of a zoomed tab, so a zoom made once the client is up is typed
// past until a repaint reaches it, and no frame the client paints tells that
// repaint from an earlier one. Zoomed before the client exists, the first
// state it is sent already has the pane focused.
func TestAPerClientViewAttachOntoAPaneZoomsItBeforeTheClientIsSpawned(t *testing.T) {
	b := requireClientViewHerdr(t)
	ctx := context.Background()
	if _, err := b.Create(ctx, backend.CreateSpec{Name: "split"}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	panes, err := b.Panes(ctx, "split")
	if err != nil || len(panes) != 1 {
		t.Fatalf("Panes(split) = %v, %v; want one pane", panes, err)
	}
	target := splitRight(t, b, panes[0].ID)

	spec := backend.AttachSpec{Role: backend.RoleController, Supersede: true, SessionClient: true, Bare: true, Cols: 120, Rows: 40}
	att, err := b.Attach(ctx, target, spec)
	if err != nil {
		t.Fatalf("bare Attach(%s): %v", target, err)
	}
	_ = att.Close()
	if !zoomedOnto(t, b, target) {
		t.Fatalf("building the attach onto %s left its tab unzoomed or focused elsewhere", target)
	}

	raw(t, b, "pane", "zoom", "--pane", panes[0].ID, "--off")
	c := startBare(t, b, target)
	_, _ = c.in.WriteString("echo olympus-on-the-split\r")
	waitScreen(t, b, target, "olympus-on-the-split")
	if screenShows(t, b, panes[0].ID, "olympus-on-the-split") {
		t.Errorf("the marker typed after attaching %s landed in %s", target, panes[0].ID)
	}
}

// withoutViewAck makes a handle read its server as one that does not report
// when a client has applied its view, the way a server without
// `client_view_ack` is read, so the frame path runs against a server that
// has it (§8.10).
func withoutViewAck(t *testing.T, b *Herdr) {
	t.Helper()
	if _, err := b.capabilities(context.Background()); err != nil {
		t.Fatalf("asking the server for its capabilities: %v", err)
	}
	b.mu.Lock()
	b.caps.viewAck = false
	b.mu.Unlock()
}

// §8.10 A go onto a pane in a tab the client does not show zooms the pane
// before the client's view is moved, so the repaint the go waits for is the
// one that moved the view, and the state that repaint carries already has the
// pane focused. Waited for the other way round, the zoom's own wait was met by
// a frame the view change painted late (measured: 17 of 20 goes under load),
// and a marker typed after the go was dropped by the server whenever the
// zoom's repaint had not yet reached the client (4 of those 20). This is the
// frame path, a server without `client_view_ack`.
func TestAPerClientViewGoOntoAPaneInAnotherTabZoomsBeforeTheViewMoves(t *testing.T) {
	b := requireClientViewHerdr(t)
	withoutViewAck(t, b)
	ctx := context.Background()
	for _, name := range []string{"first", "second"} {
		if _, err := b.Create(ctx, backend.CreateSpec{Name: name}); err != nil {
			t.Fatalf("Create: %v", err)
		}
	}
	second, err := b.resolve(ctx, "second")
	if err != nil {
		t.Fatalf("resolve(second): %v", err)
	}
	var tabbed struct {
		Result struct {
			Tab  tabRow  `json:"tab"`
			Root paneRow `json:"root_pane"`
		} `json:"result"`
	}
	out := raw(t, b, "tab", "create", "--workspace", second.workspace.WorkspaceID, "--no-focus")
	if err := json.Unmarshal([]byte(out), &tabbed); err != nil || tabbed.Result.Tab.TabID == "" {
		t.Fatalf("tab create answered no tab: %v\n%s", err, out)
	}
	target := splitRight(t, b, tabbed.Result.Root.PaneID)

	spec := backend.AttachSpec{Role: backend.RoleController, Supersede: true, SessionClient: true, Bare: true, Cols: 120, Rows: 40}
	att, err := b.Attach(ctx, "first", spec)
	if err != nil {
		t.Fatalf("bare Attach(first): %v", err)
	}
	inR, inW, _ := os.Pipe()
	outR, outW, _ := os.Pipe()
	defer func() { _ = inW.Close(); _ = inR.Close(); _ = outW.Close(); _ = outR.Close() }()
	go func() { _, _ = io.Copy(io.Discard, outR) }()
	runCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = engine.Attach(runCtx, att, engine.AttachIO{In: inR, Out: outW, Err: io.Discard}, spec, nil)
	}()
	defer func() { cancel(); <-done }()
	c := &liveClient{tag: flagValue(att.Cmd.Args, "--client-tag"), done: done}
	first, _ := b.resolve(ctx, "first")
	waitClientOn(t, b, c, first.workspace.WorkspaceID)
	// Input is forwarded only once the attach has settled, and settling puts
	// the client on its target: a move made before then is undone by it.
	_, _ = inW.WriteString("echo olympus-settled\r")
	waitScreen(t, b, first.workspace.WorkspaceID, "olympus-settled")

	waits := 0
	zoomedFirst := func(mark []byte) func(time.Duration) bool {
		waits++
		if row, _, _ := b.taggedClient(ctx, c.tag); row.TabID != tabbed.Result.Tab.TabID && !zoomedOnto(t, b, target) {
			t.Errorf("a repaint was waited for with the client on %s and %s not yet zoomed", row.TabID, target)
		}
		return func(time.Duration) bool { return true }
	}
	if err := att.Go(ctx, target, io.Discard, zoomedFirst); err != nil {
		t.Fatalf("Go(%s): %v", target, err)
	}
	if waits == 0 {
		t.Error("the go returned without waiting for the client's repaint")
	}
	row, ok, err := b.taggedClient(ctx, c.tag)
	if err != nil || !ok || row.TabID != tabbed.Result.Tab.TabID {
		t.Errorf("after Go(%s) the client is %+v (%v, %v), want on %s", target, row, ok, err, tabbed.Result.Tab.TabID)
	}
	if !zoomedOnto(t, b, target) {
		t.Errorf("after Go(%s) its tab is not zoomed onto it", target)
	}
}

// testLog carries what the engine narrates about a client into the test log.
type testLog struct {
	t   *testing.T
	tag string
}

func (l testLog) Write(p []byte) (int, error) {
	l.t.Logf("%s: %s", l.tag, strings.TrimSpace(string(p)))
	return len(p), nil
}

// §8.10 A client somebody else moves by its id is followed: the probe reads
// where `client.list` has it, so closing the workspace this backend last put
// it on does not end the attach, and closing the one it was moved to does.
func TestAPerClientViewProbeFollowsAClientMovedByItsID(t *testing.T) {
	b := requireClientViewHerdr(t)
	ctx := context.Background()
	ids := map[string]string{}
	for _, name := range []string{"first", "second", "third"} {
		created, err := b.Create(ctx, backend.CreateSpec{Name: name})
		if err != nil {
			t.Fatalf("Create: %v", err)
		}
		ids[name] = created.ID
	}
	c := startBare(t, b, "first")
	row := waitClientOn(t, b, c, ids["first"])
	// Input is forwarded only once the attach has settled, and settling puts
	// the client on its target: a move made before then is undone by it.
	_, _ = c.in.WriteString("echo olympus-settled\r")
	waitScreen(t, b, ids["first"], "olympus-settled")

	if _, err := b.call(ctx, "client.view.focus", map[string]any{"client_id": row.ClientID, "workspace_id": ids["second"]}); err != nil {
		t.Fatalf("moving the client by its id: %v", err)
	}
	waitClientOn(t, b, c, ids["second"])
	time.Sleep(3 * engine.TargetPollInterval)
	raw(t, b, "workspace", "close", ids["first"])
	time.Sleep(3 * engine.TargetPollInterval)
	select {
	case <-c.done:
		t.Fatal("closing the workspace the client was moved away from ended the attach")
	default:
	}
	raw(t, b, "workspace", "close", ids["second"])
	select {
	case <-c.done:
	case <-time.After(10 * time.Second):
		t.Fatal("closing the workspace the client was moved to did not end the attach")
	}
}

// goAndType writes a go and a marker in one write, as a consumer's stream
// carries them, and waits for the marker on the pane the go named; it fails
// where the marker shows on any other pane given.
func goAndType(t *testing.T, b *Herdr, c *liveClient, target, marker string, others ...string) {
	t.Helper()
	if _, err := c.in.WriteString(goControl(target) + "echo " + marker + "\r"); err != nil {
		t.Fatalf("writing the go onto %s: %v", target, err)
	}
	waitScreen(t, b, target, marker)
	for _, other := range others {
		if other != target && screenShows(t, b, other, marker) {
			t.Errorf("%s typed after going onto %s landed in %s", marker, target, other)
		}
	}
}

// §8.10 A go onto the other pane of the tab the client already shows has no
// view change to carry it: the zoom moves the tab's focus, and what is typed
// after the go must reach the pane the zoom focused, not the one the client
// had focused before it was told.
func TestAPerClientViewGoOntoAPaneInTheSameTabLandsWhatIsTyped(t *testing.T) {
	b := requireClientViewHerdr(t)
	ctx := context.Background()
	created, err := b.Create(ctx, backend.CreateSpec{Name: "split"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	panes, err := b.Panes(ctx, "split")
	if err != nil || len(panes) != 1 {
		t.Fatalf("Panes(split) = %v, %v; want one pane", panes, err)
	}
	left := panes[0].ID
	right := splitRight(t, b, left)

	c := startBare(t, b, "split")
	waitClientOn(t, b, c, created.ID)
	_, _ = c.in.WriteString("echo olympus-settled\r")
	waitScreen(t, b, left, "olympus-settled")

	goAndType(t, b, c, right, "olympus-onto-the-right", left)
	if !zoomedOnto(t, b, right) {
		t.Errorf("after the go onto %s its tab is not zoomed onto it", right)
	}
	goAndType(t, b, c, left, "olympus-back-onto-the-left", right)
	if !zoomedOnto(t, b, left) {
		t.Errorf("after the go onto %s its tab is not zoomed onto it", left)
	}
}

// §8.10, §8.11 Goes in a row between the panes of two split tabs, in the same
// tab and across workspaces, each with a marker typed straight after it:
// every marker lands in the pane its go named. This is the shape a consumer's
// quick tab switching takes, and the one a confirmation met by a stale frame
// loses input in.
func TestAPerClientViewGoesBetweenPanesInARowLandEveryMarker(t *testing.T) {
	b := requireClientViewHerdr(t)
	ctx := context.Background()
	var panes []string
	for _, name := range []string{"first", "second"} {
		if _, err := b.Create(ctx, backend.CreateSpec{Name: name}); err != nil {
			t.Fatalf("Create(%s): %v", name, err)
		}
		rows, err := b.Panes(ctx, name)
		if err != nil || len(rows) != 1 {
			t.Fatalf("Panes(%s) = %v, %v; want one pane", name, rows, err)
		}
		panes = append(panes, rows[0].ID, splitRight(t, b, rows[0].ID))
	}
	c := startBare(t, b, "first")
	_, _ = c.in.WriteString("echo olympus-settled\r")
	waitScreen(t, b, panes[0], "olympus-settled")

	order := []int{1, 0, 3, 2, 1, 2, 0, 3, 1, 0, 2, 3, 0, 1, 3, 2}
	for i, p := range order {
		goAndType(t, b, c, panes[p], fmt.Sprintf("olympus-row-%d", i), panes...)
	}
}

// §8.10 On a server that reports when a client has applied its view, a go
// onto a pane in another tab, and one onto the other pane of the tab it
// shows, watch nothing the client paints: each returns once the server says
// the client has applied a view with that pane focused and its tab zoomed.
func TestAPerClientViewGoOnAnAckingServerReturnsOnceTheViewIsApplied(t *testing.T) {
	b := requireClientViewHerdr(t)
	ctx := context.Background()
	if caps, err := b.capabilities(ctx); err != nil || !caps.viewAck {
		t.Skipf("the server does not advertise client_view_ack (%+v, %v), so the acknowledged go is not being run", caps, err)
	}
	for _, name := range []string{"first", "second"} {
		if _, err := b.Create(ctx, backend.CreateSpec{Name: name}); err != nil {
			t.Fatalf("Create: %v", err)
		}
	}
	second, err := b.resolve(ctx, "second")
	if err != nil {
		t.Fatalf("resolve(second): %v", err)
	}
	var tabbed struct {
		Result struct {
			Tab  tabRow  `json:"tab"`
			Root paneRow `json:"root_pane"`
		} `json:"result"`
	}
	out := raw(t, b, "tab", "create", "--workspace", second.workspace.WorkspaceID, "--no-focus")
	if err := json.Unmarshal([]byte(out), &tabbed); err != nil || tabbed.Result.Tab.TabID == "" {
		t.Fatalf("tab create answered no tab: %v\n%s", err, out)
	}
	left := tabbed.Result.Root.PaneID
	right := splitRight(t, b, left)

	spec := backend.AttachSpec{Role: backend.RoleController, Supersede: true, SessionClient: true, Bare: true, Cols: 120, Rows: 40}
	att, err := b.Attach(ctx, "first", spec)
	if err != nil {
		t.Fatalf("bare Attach(first): %v", err)
	}
	inR, inW, _ := os.Pipe()
	outR, outW, _ := os.Pipe()
	defer func() { _ = inW.Close(); _ = inR.Close(); _ = outW.Close(); _ = outR.Close() }()
	go func() { _, _ = io.Copy(io.Discard, outR) }()
	runCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = engine.Attach(runCtx, att, engine.AttachIO{In: inR, Out: outW, Err: io.Discard}, spec, nil)
	}()
	defer func() { cancel(); <-done }()
	tag := flagValue(att.Cmd.Args, "--client-tag")
	_, _ = inW.WriteString("echo olympus-settled\r")
	waitScreen(t, b, "first", "olympus-settled")

	noFrame := func(mark []byte) func(time.Duration) bool {
		t.Errorf("the go watched the client's output for %q", mark)
		return func(time.Duration) bool { return true }
	}
	for _, pane := range []string{right, left} {
		if err := att.Go(ctx, pane, io.Discard, noFrame); err != nil {
			t.Fatalf("Go(%s): %v", pane, err)
		}
		row, ok, err := b.taggedClient(ctx, tag)
		if err != nil || !ok {
			t.Fatalf("client.list after Go(%s): %+v, %v, %v", pane, row, ok, err)
		}
		if row.TabID != tabbed.Result.Tab.TabID || row.PaneID != pane || !row.Zoomed || !row.ViewApplied {
			t.Errorf("after Go(%s) returned the client is %+v, want on %s with %s focused, zoomed and applied",
				pane, row, tabbed.Result.Tab.TabID, pane)
		}
	}
}

// §8.10 A bare attach onto the only pane of a workspace, and a go onto the
// only pane of another, land what is typed: the zoom steps run for a lone
// pane too, and the confirmation takes the view the server reports for it.
func TestAPerClientViewAttachAndGoOntoALonePaneLandWhatIsTyped(t *testing.T) {
	b := requireClientViewHerdr(t)
	ctx := context.Background()
	var lone []string
	for _, name := range []string{"first", "second"} {
		if _, err := b.Create(ctx, backend.CreateSpec{Name: name}); err != nil {
			t.Fatalf("Create(%s): %v", name, err)
		}
		rows, err := b.Panes(ctx, name)
		if err != nil || len(rows) != 1 {
			t.Fatalf("Panes(%s) = %v, %v; want one pane", name, rows, err)
		}
		lone = append(lone, rows[0].ID)
	}
	c := startBare(t, b, lone[0])
	_, _ = c.in.WriteString("echo olympus-on-the-lone-pane\r")
	waitScreen(t, b, lone[0], "olympus-on-the-lone-pane")
	goAndType(t, b, c, lone[1], "olympus-onto-the-other-lone-pane", lone[0])
}

// isolateHome moves HOME and the XDG configuration and state homes into a
// directory the test owns, so a client or server that reads them never reads
// or writes the operator's own (§2.9).
func isolateHome(t *testing.T) {
	t.Helper()
	home := shortDir(t)
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", home)
	t.Setenv("XDG_STATE_HOME", filepath.Join(home, "state"))
}

// waitListed waits for the client carrying a tag to be listed in a way
// `ok` accepts, and fails with the last listing otherwise.
func waitListed(t *testing.T, b *Herdr, tag string, ok func(backend.Client) bool) backend.Client {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	var rows []backend.Client
	for time.Now().Before(deadline) {
		var err error
		rows, err = b.Clients(context.Background())
		if err != nil {
			t.Fatalf("Clients: %v", err)
		}
		for _, row := range rows {
			if row.Tag == tag && ok(row) {
				return row
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("no client tagged %s was listed as expected; the listing: %+v", tag, rows)
	return backend.Client{}
}

// §8.10, §13.5 A caller names its own bare client, and the listing reports
// which workspace, tab and pane that client shows, including a pane focused
// on the server after the client came up: the client does not move, the
// focused pane of the tab it shows does, and the row follows it.
func TestAClientTaggedByTheCallerIsListedWithThePaneItShows(t *testing.T) {
	isolateHome(t)
	b := requireClientViewHerdr(t)
	ctx := context.Background()
	created, err := b.Create(ctx, backend.CreateSpec{Name: "split"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	panes, err := b.Panes(ctx, "split")
	if err != nil || len(panes) != 1 {
		t.Fatalf("Panes(split) = %v, %v; want one pane", panes, err)
	}
	left := panes[0].ID
	right := splitRight(t, b, left)
	snap, err := b.snapshot(ctx)
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	row, found := snap.paneByID(left)
	if !found {
		t.Fatalf("the snapshot has no pane %s", left)
	}
	tab := row.TabID

	const tag = "browser-1 of the caller"
	c := startBareTagged(t, b, "split", tag)
	if c.tag != tag {
		t.Fatalf("the client is launched with --client-tag %q, want %q", c.tag, tag)
	}
	shown := waitListed(t, b, tag, func(r backend.Client) bool {
		return r.PaneID == left && r.ViewApplied != nil && *r.ViewApplied
	})
	if shown.SessionID != created.ID || shown.WindowID != tab || shown.ID == "" {
		t.Errorf("the client is listed as %+v, want id set, on %s, %s", shown, created.ID, tab)
	}
	if shown.Zoomed == nil || *shown.Zoomed {
		t.Errorf("the client's tab is listed as zoomed %v, want reported and false", shown.Zoomed)
	}

	raw(t, b, "pane", "focus", "--pane", left, "--direction", "right")
	moved := waitListed(t, b, tag, func(r backend.Client) bool { return r.PaneID == right })
	if moved.SessionID != created.ID || moved.WindowID != tab || moved.ID != shown.ID {
		t.Errorf("after the pane focus the client is listed as %+v, want client %s still on %s, %s", moved, shown.ID, created.ID, tab)
	}

	// The same move made the way a person makes it: the client's own
	// focus-pane-left key, behind the default prefix, spelled the way the
	// client reads keys once it has asked for the kitty protocol.
	if _, err := c.in.WriteString("\x1b[98;5u" + "h"); err != nil {
		t.Fatalf("pressing the client's own pane key: %v", err)
	}
	back := waitListed(t, b, tag, func(r backend.Client) bool { return r.PaneID == left })
	if back.SessionID != created.ID || back.WindowID != tab || back.ID != shown.ID {
		t.Errorf("after the client's own pane key the client is listed as %+v, want client %s still on %s, %s", back, shown.ID, created.ID, tab)
	}
}
