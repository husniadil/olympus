package herdr

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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
