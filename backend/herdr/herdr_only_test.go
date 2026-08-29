package herdr

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/creack/pty"

	"github.com/husniadil/olympus/backend"
)

// §2.3.1 A backend that cannot spawn a command refuses it before doing anything.
//
// Before anything, and not merely eventually: the refusal must not depend on a
// server being reachable, or the contract would read differently on a cold host
// than on a warm one. This asserts it against a socket no server could ever
// answer on.
func TestSpawningACommandIsRefusedWithoutTouchingAServer(t *testing.T) {
	t.Parallel()
	b := New(WithSocketPath(filepath.Join(shortDir(t), "h.sock")))

	_, err := b.Create(context.Background(), backend.CreateSpec{
		Name:    "oly-unit-1",
		Command: []string{"sh", "-c", "sleep 30"},
		Cols:    80,
		Rows:    24,
	})
	if err == nil {
		t.Fatal("creating a session on an argv succeeded")
	}
	if !errors.Is(err, backend.ErrUnsupported) {
		t.Errorf("creating a session on an argv is %q, want %q", backend.CodeOf(err), backend.CodeUnsupported)
	}
	if b.Capabilities().SpawnCommand {
		t.Error("the capability says a command can be spawned, but Create refuses one")
	}
}

// §2.7 remain-on-exit is refused the same way, and for the same reason: a pane
// whose process exits is closed, so there is no corpse to inspect.
func TestRemainOnExitIsRefusedWithoutTouchingAServer(t *testing.T) {
	t.Parallel()
	b := New(WithSocketPath(filepath.Join(shortDir(t), "h.sock")))

	_, err := b.Create(context.Background(), backend.CreateSpec{Name: "oly-unit-2", RemainOnExit: true})
	if !errors.Is(err, backend.ErrUnsupported) {
		t.Errorf("creating with remain-on-exit is %q, want %q", backend.CodeOf(err), backend.CodeUnsupported)
	}
}

// §2.5 The socket path carries a budget, and it is the DERIVED client socket
// that has to fit.
//
// herdr binds two sockets and makes the second by inserting "-client" into the
// first, so a path that fits on its own can still produce one that does not.
// Without this check the failure names neither Olympus nor the path: the server
// exits with a sockaddr_un message and every later call reports no server.
func TestAnOverlongSocketPathIsRejectedByName(t *testing.T) {
	t.Parallel()
	long := filepath.Join(os.TempDir(), strings.Repeat("d", 100), "h.sock")
	b := New(WithSocketPath(long))

	_, err := b.Sessions(context.Background())
	if err == nil {
		t.Fatal("an over-long socket path was accepted")
	}
	if backend.CodeOf(err) != backend.CodeUsage {
		t.Errorf("an over-long socket path is %q, want %q", backend.CodeOf(err), backend.CodeUsage)
	}
	if !strings.Contains(err.Error(), long) {
		t.Errorf("the error %q does not name the path it rejected", err.Error())
	}
}

// §2.9 Configuration and state move WITH the socket, not merely alongside it.
//
// This is the isolation trap this backend pays for: herdr keeps the unnamed
// session's persisted layout in its configuration directory rather than beside
// its socket, so a private socket alone would still have a test server
// overwrite the operator's saved workspaces. The pairing is derived rather than
// configurable so it cannot be half-applied.
func TestStateFollowsTheSocket(t *testing.T) {
	t.Parallel()
	dir := shortDir(t)
	b := New(WithSocketPath(filepath.Join(dir, "h.sock")))

	home := b.StateHome()
	if !strings.HasPrefix(home, dir) {
		t.Errorf("state home %q is not inside the socket's own directory %q", home, dir)
	}
	if home == dir {
		t.Errorf("state home is the socket's directory itself, so herdr's own files would sit beside the socket")
	}
}

// §17.2 The default posture is Olympus's own server, never the operator's.
func TestTheDefaultSocketIsNotTheOperatorsOwn(t *testing.T) {
	t.Parallel()
	got := DefaultSocketPath()
	if !strings.Contains(got, DefaultSocketName) {
		t.Errorf("the default socket %q is not inside Olympus's reserved directory", got)
	}
	if strings.Contains(got, filepath.Join(".config", "herdr")) {
		t.Errorf("the default socket %q is the operator's own server", got)
	}
}

// §10 A session may not be named like a pane id, because resolution reads that
// spelling as a pane and the session would be shadowed by every listing.
//
// Asserted against the validator rather than through Create, deliberately: a
// name that is ACCEPTED reaches the server-start path, and a unit test that
// booted a real server to prove a name is legal would leave one behind for
// every legal name it checked.
func TestASessionMayNotBeNamedLikeAPaneID(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"w1:p1", "w12:p3"} {
		err := validateName(name)
		if backend.CodeOf(err) != backend.CodeUsage {
			t.Errorf("the name %q is %q, want %q", name, backend.CodeOf(err), backend.CodeUsage)
		}
	}
	// A name that merely looks similar is a perfectly ordinary name.
	for _, name := range []string{"w1p1", "work:p1", "w1:pane", "wp:1"} {
		if err := validateName(name); err != nil {
			t.Errorf("the ordinary name %q was rejected: %v", name, err)
		}
	}
	if err := validateName(""); backend.CodeOf(err) != backend.CodeUsage {
		t.Errorf("an empty name is %q, want %q", backend.CodeOf(err), backend.CodeUsage)
	}
}

// §8.7 There is no read-only terminal client, so a viewer attach is refused
// rather than downgraded into a controller nobody expects to be one.
func TestAViewerAttachIsRefused(t *testing.T) {
	t.Parallel()
	b := New(WithSocketPath(filepath.Join(shortDir(t), "h.sock")))

	_, err := b.Attach(context.Background(), "oly-unit-3", backend.AttachSpec{Role: backend.RoleViewer})
	if !errors.Is(err, backend.ErrUnsupported) {
		t.Errorf("a viewer attach is %q, want %q", backend.CodeOf(err), backend.CodeUnsupported)
	}
}

// §3.3 No server running is an empty list, not an error. Asserted against a
// socket nothing has ever bound, so the answer cannot come from a leftover.
func TestListingWithNoServerIsEmpty(t *testing.T) {
	t.Parallel()
	b := New(WithSocketPath(filepath.Join(shortDir(t), "h.sock")))

	sessions, err := b.Sessions(context.Background())
	if err != nil {
		t.Fatalf("listing with no server running returned an error: %v", err)
	}
	if len(sessions) != 0 {
		t.Errorf("listing with no server running returned %d sessions", len(sessions))
	}
	if got := b.Probe(context.Background(), "oly-unit-4"); got != backend.StateAbsent {
		t.Errorf("probing with no server running is %q, want %q", got, backend.StateAbsent)
	}
}

// §3.4 created_at is derived from the terminal id's embedded timestamp, because
// herdr exposes no creation time anywhere in its API.
//
// Pinned against a real id rather than a synthetic one: the whole risk of the
// derivation is that herdr's format moves, and a fixture invented here would
// only pin what this file already believes. This id came off a live server.
func TestCreatedAtIsDerivedFromTheTerminalID(t *testing.T) {
	t.Parallel()
	// term_65a32838b72772 was allocated at 2026-08-30T00:01:02Z.
	const observed = "term_65a32838b72772"
	const want = 1788022862

	if got := createdAt(observed); got != want {
		t.Errorf("created_at for %s is %d, want %d", observed, got, want)
	}
	// A shape this backend cannot read must produce an implausible epoch
	// rather than a wrong one, so the conformance suite fails loudly instead
	// of the column quietly going zero-ish.
	for _, id := range []string{"", "terminal-7", "term_zzz"} {
		if got := createdAt(id); got != 0 {
			t.Errorf("created_at for an unreadable id %q is %d, want 0", id, got)
		}
	}
}

// §1.1 The spawn hygiene is applied per pane, through the creation request,
// because a pane inherits the SERVER's environment and that is not ours to
// change from a client.
func TestSpawnHygieneTravelsWithTheCreationRequest(t *testing.T) {
	t.Parallel()
	args := strings.Join(spawnEnvArgs(), " ")
	for _, want := range []string{
		"TERM=xterm-256color",
		"TMUX=",
		"TMUX_PANE=",
		"ZMX_SESSION=",
		"ZMX_SESSION_PREFIX=",
	} {
		if !strings.Contains(args, want) {
			t.Errorf("the creation request does not carry %q", want)
		}
	}
	if !strings.Contains(args, "LANG=") {
		t.Error("the creation request does not default LANG")
	}
}

// §1.1 herdr's own session and socket variables are stripped from every
// invocation.
//
// HERDR_SESSION selects a NAMED server whose socket lives under herdr's
// configuration directory. herdr resolves HERDR_SOCKET_PATH first and forces
// the session override off when it is set (src/session.rs:81-83), so this is
// defence rather than a fix — but the ordering is herdr's to change, and the
// consequence of it changing is a test suite driving the operator's live
// session.
func TestHerdrsOwnAddressingVariablesAreStripped(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HERDR_SESSION", "operator")
	t.Setenv("HERDR_CLIENT_SOCKET_PATH", "/tmp/somebody-elses-client.sock")

	b := New(WithSocketPath(filepath.Join(dir, "h.sock")))
	env := strings.Join(b.env(invocationEnv()), "\n")
	for _, banned := range []string{"HERDR_SESSION=operator", "HERDR_CLIENT_SOCKET_PATH="} {
		if strings.Contains(env, banned) {
			t.Errorf("an invocation carries %q, which retargets it at a server the caller did not choose", banned)
		}
	}
	if want := "HERDR_SOCKET_PATH=" + filepath.Join(dir, "h.sock"); !strings.Contains(env, want) {
		t.Errorf("an invocation does not carry %q", want)
	}
}

// Stop is safe against a server that is not running: it is the desired state
// already, and a cleanup that failed for having succeeded would fail every case.
func TestStoppingAServerThatIsNotRunningSucceeds(t *testing.T) {
	t.Parallel()
	b := New(WithSocketPath(filepath.Join(shortDir(t), "h.sock")))

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := b.Stop(ctx); err != nil {
		t.Errorf("stopping a server that is not running: %v", err)
	}
}

// shortDir is a private directory whose PATH is short enough for a unix socket.
//
// Deliberately not t.TempDir(): that one embeds the test's name, and the socket
// path budget is small enough that a descriptive name alone exhausts it — which
// is exactly what these tests would then be measuring. Measured: a t.TempDir()
// under macOS leaves 107 bytes for a 103-byte budget before the test has
// contributed anything.
func shortDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp(os.TempDir(), "olyh")
	if err != nil {
		t.Fatalf("creating a private directory: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

// liveBackend brings up a server on a private socket and stops it afterwards.
//
// Stopping is not optional and not best-effort: a leaked server holds a login
// shell open for as long as the machine runs, and these cases start one each.
func liveBackend(t *testing.T) *Herdr {
	t.Helper()
	b := New(WithSocketPath(filepath.Join(shortDir(t), "h.sock")))
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	if err := b.ensureServer(ctx); err != nil {
		t.Fatalf("starting a private server: %v", err)
	}
	t.Cleanup(func() {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer stopCancel()
		if err := b.Stop(stopCtx); err != nil {
			t.Errorf("stopping the test server at %s: %v", b.Scope(), err)
		}
	})
	return b
}

// raw runs a herdr command against a backend's server, the way something that
// is not Olympus would.
func raw(t *testing.T, b *Herdr, args ...string) string {
	t.Helper()
	cmd := exec.Command("herdr", args...)
	cmd.Env = b.env(invocationEnv())
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("herdr %v: %v\n%s", args, err, out)
	}
	return string(out)
}

// §3.4 A pane nothing labelled is still a session, addressed by its pane id.
//
// This is the case the backend exists for. The panes worth driving are usually
// the ones something else created — a box's own headless herdr, a human's
// `pane split`, another tool's workspace — and none of them carry a label.
// Measured on a live server before this was fixed: three panes, none labelled,
// so the listing answered nothing and even a pane id resolved to not-found.
func TestAnUnlabelledPaneIsAFullSession(t *testing.T) {
	b := liveBackend(t)
	ctx := context.Background()

	// Created the way anything that is not Olympus creates one: no label.
	// A headless server may boot with no pane at all (measured: a fresh
	// server in a clean environment lists zero panes), so the created pane
	// is found by the id herdr answered with rather than by counting.
	created := raw(t, b, "workspace", "create", "--no-focus")
	var reply struct {
		Result struct {
			RootPane struct {
				PaneID string `json:"pane_id"`
			} `json:"root_pane"`
		} `json:"result"`
	}
	if err := json.Unmarshal([]byte(created), &reply); err != nil || reply.Result.RootPane.PaneID == "" {
		t.Fatalf("workspace create answered no root pane id: %v\n%s", err, created)
	}
	target := reply.Result.RootPane.PaneID

	sessions, err := b.Sessions(ctx)
	if err != nil {
		t.Fatalf("Sessions: %v", err)
	}
	var listed bool
	for _, s := range sessions {
		if !backend.IndexedPaneID(s.Name) {
			t.Errorf("an unlabelled pane is named %q, want its pane id", s.Name)
			continue
		}
		if s.ID != s.Name {
			t.Errorf("session %q reports id %q; an unlabelled pane's name IS its id", s.Name, s.ID)
		}
		if s.Name == target {
			listed = true
		}
	}
	if !listed {
		t.Fatalf("the unlabelled pane %s is not listed among %d sessions", target, len(sessions))
	}

	if got := b.Probe(ctx, target); got != backend.StatePresent {
		t.Errorf("probing an unlabelled pane by id is %q, want %q", got, backend.StatePresent)
	}
	panes, err := b.Panes(ctx, target)
	if err != nil {
		t.Fatalf("Panes(%s): %v", target, err)
	}
	if len(panes) != 1 || panes[0].SessionName != target {
		t.Errorf("a pane listing for %s names session %+v", target, panes)
	}

	// Driven, not merely listed.
	warmShell(t, b, target)
	if err := b.SendAtomic(ctx, target, `printf 'unlabelled-%d\n' 4`); err != nil {
		t.Fatalf("SendAtomic: %v", err)
	}
	waitForScreen(t, b, target, "unlabelled-4")

	// And attached, which is the operation that has to turn a pane id into the
	// server-owned terminal behind it.
	attachAndType(t, b, target, "attached-by-id")

	if err := b.Kill(ctx, target); err != nil {
		t.Fatalf("Kill: %v", err)
	}
	if got := b.Probe(ctx, target); got != backend.StateAbsent {
		t.Errorf("a killed unlabelled pane probes %q, want %q", got, backend.StateAbsent)
	}
}

// §2.9.1 A server this handle did not start is driven, never started and never
// stopped.
//
// The whole reason this backend can be pointed at an existing server is that
// something else owns it — a box's headless herdr with a fleet of agents in it,
// or the operator's own. Reading, driving and attaching must all work there;
// stopping must not, because it would take every pane on it down including
// every one the caller never mentioned.
func TestAServerThisHandleDidNotStartIsDrivenButNotStopped(t *testing.T) {
	owner := liveBackend(t)
	ctx := context.Background()

	// A second handle on the same socket, which is exactly what a consumer
	// pointed at a box's own herdr holds.
	foreign := New(WithSocketPath(owner.Scope()))
	if foreign.startedTheServer() {
		t.Fatal("a handle that started nothing claims it started the server")
	}
	// Give the server a pane the way something that is not Olympus would;
	// a headless server does not necessarily boot with one.
	raw(t, owner, "workspace", "create", "--no-focus")

	sessions, err := foreign.Sessions(ctx)
	if err != nil {
		t.Fatalf("listing a foreign server: %v", err)
	}
	if len(sessions) == 0 {
		t.Fatal("a foreign server with a pane on it listed nothing")
	}
	target := sessions[0].Name

	if _, err := foreign.Screen(ctx, target, backend.ScreenOpts{}); err != nil {
		t.Fatalf("capturing on a foreign server: %v", err)
	}
	warmShell(t, foreign, target)
	if err := foreign.SendAtomic(ctx, target, `printf 'foreign-%d\n' 9`); err != nil {
		t.Fatalf("driving a foreign server: %v", err)
	}
	waitForScreen(t, foreign, target, "foreign-9")
	attachAndType(t, foreign, target, "attached-foreign")

	// Creation must not start a second server, and must not pin anything.
	//
	// The pins are checked by MODIFICATION TIME rather than by absence: both
	// handles address one socket and therefore derive one state directory, so
	// the file the owner wrote when it booted the server is legitimately
	// there. What must not happen is this handle writing it again — which is
	// what would happen for a real foreign server, whose directory holds no
	// such file and never read one.
	pinned, err := os.Stat(foreign.managedConfigPath())
	if err != nil {
		t.Fatalf("the owner's own server was started without its pins: %v", err)
	}
	if _, err := foreign.Create(ctx, backend.CreateSpec{Name: "oly-foreign-created"}); err == nil {
		t.Cleanup(func() { _ = foreign.Kill(context.Background(), "oly-foreign-created") })
	} else {
		t.Fatalf("creating a session on a foreign server: %v", err)
	}
	if foreign.startedTheServer() {
		t.Error("creating a session on a server that was already answering recorded it as ours")
	}
	if after, err := os.Stat(foreign.managedConfigPath()); err != nil || !after.ModTime().Equal(pinned.ModTime()) {
		t.Errorf("a handle that started nothing rewrote the managed configuration")
	}

	// And the refusal this whole mode rests on.
	err = foreign.Stop(ctx)
	if err == nil {
		t.Fatal("a handle stopped a server it did not start, taking every pane on it down")
	}
	if backend.CodeOf(err) != backend.CodeConflict {
		t.Errorf("stopping a foreign server is %q, want %q", backend.CodeOf(err), backend.CodeConflict)
	}
	if owner.Probe(ctx, target) == backend.StateAbsent {
		t.Error("the refused stop still took the server down")
	}

	// The attach client is the one invocation whose configuration directory
	// decides behaviour, so a foreign server's must not be overridden.
	attachment, err := foreign.Attach(ctx, target, backend.AttachSpec{Role: backend.RoleController})
	if err != nil {
		t.Fatalf("Attach: %v", err)
	}
	for _, kv := range attachment.Cmd.Env {
		if strings.HasPrefix(kv, "XDG_CONFIG_HOME=") && strings.Contains(kv, foreign.StateHome()) {
			t.Errorf("an attach onto a foreign server imposes Olympus's own configuration directory: %s", kv)
		}
	}
}

// warmShell blocks until the pane's shell has provably executed something.
//
// Expansion-based on purpose: PTY echo paints typed bytes onto the screen, so a
// literal string appearing there proves only that it was typed (§16).
func warmShell(t *testing.T, b *Herdr, target string) {
	t.Helper()
	ctx := context.Background()
	deadline := time.Now().Add(40 * time.Second)
	for time.Now().Before(deadline) {
		if err := b.SendAtomic(ctx, target, `printf 'ready-%d\n' 7`); err != nil {
			t.Fatalf("warming %s: %v", target, err)
		}
		if screenHas(t, b, target, "ready-7", 2*time.Second) {
			return
		}
	}
	t.Fatalf("warming %s: the shell never executed a command", target)
}

func waitForScreen(t *testing.T, b *Herdr, target, want string) {
	t.Helper()
	if !screenHas(t, b, target, want, 20*time.Second) {
		capture, _ := b.Screen(context.Background(), target, backend.ScreenOpts{})
		t.Fatalf("waiting for %q on %s: never appeared. Screen was:\n%s", want, target, capture.Text)
	}
}

func screenHas(t *testing.T, b *Herdr, target, want string, budget time.Duration) bool {
	t.Helper()
	deadline := time.Now().Add(budget)
	for {
		capture, err := b.Screen(context.Background(), target, backend.ScreenOpts{})
		if err == nil && strings.Contains(capture.Text, want) {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// attachAndType runs a real attach client in a PTY and types through it.
//
// Asserting on the returned command's argv would prove only that a pane id was
// translated into a terminal id. What has to hold is that the client reaches
// the pane and that keystrokes arrive there, which is only observable by
// running it — and it is the operation a caller pointed at somebody else's
// server most wants to work.
func attachAndType(t *testing.T, b *Herdr, target, marker string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	attachment, err := b.Attach(ctx, target, backend.AttachSpec{Role: backend.RoleController, Supersede: true})
	if err != nil {
		t.Fatalf("Attach(%s): %v", target, err)
	}
	defer func() { _ = attachment.Close() }()

	file, err := pty.StartWithSize(attachment.Cmd, &pty.Winsize{Cols: 100, Rows: 30})
	if err != nil {
		t.Fatalf("running the attach client: %v", err)
	}
	defer func() {
		_ = file.Close()
		if attachment.Cmd.Process != nil {
			_ = attachment.Cmd.Process.Kill()
		}
		_ = attachment.Cmd.Wait()
	}()
	// The client has to finish its handshake and paint before a keystroke
	// means anything; a write into a client that is not reading yet is lost.
	go func() { _, _ = io.Copy(io.Discard, file) }()
	time.Sleep(2 * time.Second)

	if _, err := file.Write([]byte("printf '" + marker + "-%d\\n' 3\r")); err != nil {
		t.Fatalf("typing through the attach client: %v", err)
	}
	waitForScreen(t, b, target, marker+"-3")
}
