package herdr

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/husniadil/olympus/backend"
)

// §13.2 A herdr server is a named session, and the rows of
// `herdr session list --json` map onto the shared shape field for field.
func TestServerListingParsesHerdrRows(t *testing.T) {
	t.Parallel()
	const fixture = `{"sessions":[` +
		`{"default":true,"name":"default","running":true,"session_dir":"/home/op/.config/herdr","socket_path":"/home/op/.config/herdr/herdr.sock"},` +
		`{"default":false,"name":"work","running":false,"session_dir":"/home/op/.config/herdr/sessions/work","socket_path":"/home/op/.config/herdr/sessions/work/herdr.sock"}` +
		`]}`

	servers, err := parseServers(fixture)
	if err != nil {
		t.Fatalf("parsing the fixture: %v", err)
	}
	want := []backend.Server{
		{Name: "default", SocketPath: "/home/op/.config/herdr/herdr.sock", Running: true, Default: true, Dir: "/home/op/.config/herdr"},
		{Name: "work", SocketPath: "/home/op/.config/herdr/sessions/work/herdr.sock", Running: false, Default: false, Dir: "/home/op/.config/herdr/sessions/work"},
	}
	if len(servers) != len(want) {
		t.Fatalf("parsed %d rows, want %d: %+v", len(servers), len(want), servers)
	}
	for i := range want {
		if servers[i] != want[i] {
			t.Errorf("row %d is %+v, want %+v", i, servers[i], want[i])
		}
	}

	if _, err := parseServers("not json"); backend.CodeOf(err) != backend.CodeUnexpected {
		t.Errorf("an unparseable listing is %q, want %q", backend.CodeOf(err), backend.CodeUnexpected)
	}
}

// §13.2 A server selected by NAME is addressed by its socket alone: the
// configuration and state redirect that WithSocketPath derives must not run,
// because the socket lives inside the operator's configuration tree and the
// derived state home would be created there.
func TestServerSocketDoesNotRedirectConfigurationOrState(t *testing.T) {
	t.Parallel()
	socket := "/home/op/.config/herdr/sessions/work/herdr.sock"
	b := New(WithServerSocket("work", socket))

	env := b.env(nil)
	for _, kv := range env {
		if strings.HasPrefix(kv, "XDG_CONFIG_HOME=") || strings.HasPrefix(kv, "XDG_STATE_HOME=") {
			t.Errorf("a server-socket backend redirects %s", kv)
		}
	}
	var addressed bool
	for _, kv := range env {
		if kv == "HERDR_SOCKET_PATH="+socket {
			addressed = true
		}
	}
	if !addressed {
		t.Errorf("the environment %v does not address the server's socket", env)
	}
}

// §13.2 The same rule, measured against the real binary: driving a named
// server's socket creates nothing OLYMPUS would put under the configuration
// tree it lives in — no state home, no managed configuration — while a create
// on a server that is not answering starts it, against the operator's own
// configuration, and does not claim it (§2.9.1).
func TestServerSocketStartsTheServerAndLeavesTheTreeToHerdr(t *testing.T) {
	requireHerdrRunnable(t)
	if testing.Short() {
		t.Skip("driving a real multiplexer; run `make test-full` for this")
	}
	configHome := shortDir(t)
	t.Setenv("XDG_CONFIG_HOME", configHome)
	socket := filepath.Join(configHome, "herdr", "sessions", "w", "herdr.sock")
	b := New(WithServerSocket("w", socket))
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	sessions, err := b.Sessions(ctx)
	if err != nil {
		t.Fatalf("listing against a socket with no server: %v", err)
	}
	if len(sessions) != 0 {
		t.Errorf("a socket with no server listed %d sessions", len(sessions))
	}

	if _, err := b.Create(ctx, backend.CreateSpec{Name: "s", Cols: 80, Rows: 24, Dir: t.TempDir()}); err != nil {
		t.Fatalf("creating on a named server that is not running: %v — Olympus must start it", err)
	}
	// Started, never owned: the operator's server stays theirs to stop, and
	// the ownership-scoped Stop still refuses it.
	t.Cleanup(func() {
		if err := b.StopServer(context.Background(), "w"); err != nil {
			t.Errorf("stopping the server this test started: %v", err)
		}
	})
	if err := b.Stop(ctx); backend.CodeOf(err) != backend.CodeConflict {
		t.Errorf("stopping a server Olympus started but does not own is %q (%v), want %q",
			backend.CodeOf(err), err, backend.CodeConflict)
	}

	// What herdr itself writes there is the operator's server doing its own
	// business. What must never appear is Olympus's.
	if _, err := os.Stat(b.StateHome()); !os.IsNotExist(err) {
		t.Errorf("a state home was created at %s inside the configuration tree", b.StateHome())
	}
	if _, err := os.Stat(b.managedConfigPath()); !os.IsNotExist(err) {
		t.Errorf("a managed configuration was written at %s", b.managedConfigPath())
	}
}

// §13.2 An unknown server name is not-found, and a name whose socket would
// not fit herdr's derived client socket is refused by name rather than left
// to fail inside the server (validateSocketPath).
func TestLookupServerReportsUnknownAndOverlongNames(t *testing.T) {
	requireHerdrRunnable(t)
	ctx := context.Background()

	t.Run("unknown", func(t *testing.T) {
		t.Setenv("XDG_CONFIG_HOME", shortDir(t))
		_, err := LookupServer(ctx, "nonesuch")
		if backend.CodeOf(err) != backend.CodeSessionNotFound {
			t.Errorf("an unknown server is %q (%v), want %q", backend.CodeOf(err), err, backend.CodeSessionNotFound)
		}
	})

	t.Run("overlong", func(t *testing.T) {
		deep := filepath.Join(shortDir(t), strings.Repeat("d", 100))
		t.Setenv("XDG_CONFIG_HOME", deep)
		_, err := LookupServer(ctx, "default")
		if backend.CodeOf(err) != backend.CodeUsage {
			t.Errorf("an over-long socket is %q (%v), want %q", backend.CodeOf(err), err, backend.CodeUsage)
		}
	})
}

// §13.2 Servers sees a named server that is running, and StopServer stops it.
//
// The named server is brought up under a PRIVATE configuration tree, never the
// operator's: `herdr session list` resolves under XDG_CONFIG_HOME, and a server
// whose socket sits at `<config>/herdr/sessions/<name>/herdr.sock` is what that
// listing calls a running named session (measured).
func TestServersSeesANamedServerAndStopsIt(t *testing.T) {
	if testing.Short() {
		t.Skip("driving a real multiplexer; run `make test-full` for this")
	}
	requireHerdrRunnable(t)
	configHome := shortDir(t)
	t.Setenv("XDG_CONFIG_HOME", configHome)
	t.Setenv("XDG_STATE_HOME", filepath.Join(configHome, "state"))
	const name = "n"
	dir := filepath.Join(configHome, "herdr", "sessions", name)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("preparing %s: %v", dir, err)
	}
	socket := filepath.Join(dir, "herdr.sock")

	server := exec.Command("herdr", "server")
	server.Env = append(invocationEnv(), "HERDR_SOCKET_PATH="+socket)
	server.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := server.Start(); err != nil {
		t.Fatalf("starting a private named server: %v", err)
	}
	go func() { _ = server.Wait() }()
	t.Cleanup(func() {
		// Belt and braces: the test stops it through the backend, and this
		// catches the run where that assertion failed.
		_ = server.Process.Signal(syscall.SIGTERM)
	})

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	b := New(WithServerSocket(name, socket))
	var listed []backend.Server
	deadline := time.Now().Add(serverStartBudget())
	for {
		var err error
		listed, err = b.Servers(ctx)
		if err != nil {
			t.Fatalf("listing servers: %v", err)
		}
		if running(listed, name) || time.Now().After(deadline) {
			break
		}
		time.Sleep(serverStartPoll)
	}
	if !running(listed, name) {
		t.Fatalf("the named server never appeared as running: %+v", listed)
	}

	stopped, err := LookupServer(ctx, name)
	if err != nil || stopped.SocketPath != socket {
		t.Errorf("looking the server up gave %+v (%v), want socket %s", stopped, err, socket)
	}
	if err := b.StopServer(ctx, name); err != nil {
		t.Fatalf("stopping the named server: %v", err)
	}
	listed, err = b.Servers(ctx)
	if err != nil {
		t.Fatalf("listing after the stop: %v", err)
	}
	if running(listed, name) {
		t.Errorf("the named server is still running after StopServer: %+v", listed)
	}
	if err := b.StopServer(ctx, "nonesuch"); backend.CodeOf(err) != backend.CodeSessionNotFound {
		t.Errorf("stopping an unknown server is %q (%v), want %q", backend.CodeOf(err), err, backend.CodeSessionNotFound)
	}
}

func running(servers []backend.Server, name string) bool {
	for _, s := range servers {
		if s.Name == name && s.Running {
			return true
		}
	}
	return false
}

// requireHerdrRunnable is requireHerdr without the full-gate skip: the cases
// using it run the binary against an empty configuration tree, which starts
// nothing and costs a subprocess, not a server.
func requireHerdrRunnable(t *testing.T) {
	t.Helper()
	if err := exec.Command("herdr", "--version").Run(); err != nil {
		t.Skip("herdr is not installed or not runnable")
	}
}

// §13.4 Starting a server creates nothing on it, and what comes up with it is
// what herdr restores: the panes that named session was running when it
// stopped. That restore is the whole reason the verb exists, so it is measured
// against the real binary rather than argued about.
func TestStartServerBringsBackWhatTheServerWasRunning(t *testing.T) {
	requireHerdrRunnable(t)
	if testing.Short() {
		t.Skip("driving a real multiplexer; run `make test-full` for this")
	}
	configHome := shortDir(t)
	t.Setenv("XDG_CONFIG_HOME", configHome)
	socket := filepath.Join(configHome, "herdr", "sessions", "w", "herdr.sock")
	b := New(WithServerSocket("w", socket))
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	row := backend.Server{Name: "w", SocketPath: socket}
	if err := b.StartServer(ctx, row); err != nil {
		t.Fatalf("starting a named server that has never run: %v", err)
	}
	t.Cleanup(func() { _ = b.StopServer(context.Background(), "w") })
	// Nothing was created by starting it. The session below is the caller's,
	// and it is what has to come back.
	if sessions, err := b.Sessions(ctx); err != nil {
		t.Fatalf("listing a server that was just started: %v", err)
	} else if len(sessions) != 0 {
		t.Fatalf("starting a server made %d sessions on it: %+v", len(sessions), sessions)
	}
	if _, err := b.Create(ctx, backend.CreateSpec{Name: "restored", Cols: 80, Rows: 24, Dir: t.TempDir()}); err != nil {
		t.Fatalf("creating on the server: %v", err)
	}
	if err := b.StopServer(ctx, "w"); err != nil {
		t.Fatalf("stopping the server: %v", err)
	}

	if err := b.StartServer(ctx, row); err != nil {
		t.Fatalf("starting the server again: %v", err)
	}
	var err error
	// The verb waits for the server to ANSWER, which is what it promises;
	// herdr's restore lands shortly after, so the listing is polled rather
	// than read once. A restore that never lands fails here on the deadline.
	var sessions []backend.Session
	var found bool
	for deadline := time.Now().Add(20 * time.Second); !found && time.Now().Before(deadline); {
		sessions, err = b.Sessions(ctx)
		if err != nil {
			t.Fatalf("listing after the restart: %v", err)
		}
		for _, s := range sessions {
			if s.Name == "restored" {
				found = true
			}
		}
		if !found {
			time.Sleep(250 * time.Millisecond)
		}
	}
	if !found {
		t.Errorf("the session the server was running did not come back: %+v", sessions)
	}
}

// §2.9 A named server Olympus boots is booted AS that session. Without
// `--session`, herdr resolves its data directory to the default session's, so
// the named socket would come up on the default session's saved layout and
// write it back over the operator's own.
func TestStartingANamedServerNamesItsSession(t *testing.T) {
	t.Parallel()
	cases := []struct {
		backend *Herdr
		want    []string
	}{
		{New(WithServerSocket("work", "/home/op/.config/herdr/sessions/work/herdr.sock")), []string{"--session", "work", "server"}},
		{New(WithServerSocket("default", "/home/op/.config/herdr/herdr.sock")), []string{"server"}},
		{New(WithSocketPath("/tmp/o/herdr.sock")), []string{"server"}},
	}
	for _, c := range cases {
		if got := c.backend.serverArgs(); strings.Join(got, " ") != strings.Join(c.want, " ") {
			t.Errorf("server %q boots with %v, want %v", c.backend.serverName, got, c.want)
		}
	}
}
