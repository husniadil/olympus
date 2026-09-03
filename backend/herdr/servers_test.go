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
	b := New(WithServerSocket(socket))

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
// server's socket creates nothing under the configuration tree it lives in,
// and a server that is not answering is NOT started by Olympus (§2.9.1) —
// starting one would boot it against the operator's configuration.
func TestServerSocketLeavesTheConfigurationTreeAlone(t *testing.T) {
	requireHerdrRunnable(t)
	configHome := shortDir(t)
	t.Setenv("XDG_CONFIG_HOME", configHome)
	socket := filepath.Join(configHome, "herdr", "sessions", "w", "herdr.sock")
	b := New(WithServerSocket(socket))
	ctx := context.Background()

	sessions, err := b.Sessions(ctx)
	if err != nil {
		t.Fatalf("listing against a socket with no server: %v", err)
	}
	if len(sessions) != 0 {
		t.Errorf("a socket with no server listed %d sessions", len(sessions))
	}

	_, err = b.Create(ctx, backend.CreateSpec{Name: "s", Cols: 80, Rows: 24, Dir: t.TempDir()})
	if backend.CodeOf(err) != backend.CodeBackendUnavailable {
		t.Errorf("creating on a named server that is not running is %q (%v), want %q — Olympus must not start it",
			backend.CodeOf(err), err, backend.CodeBackendUnavailable)
	}

	if _, err := os.Stat(b.StateHome()); !os.IsNotExist(err) {
		t.Errorf("a state home was created at %s inside the configuration tree", b.StateHome())
	}
	if entries, _ := os.ReadDir(configHome); len(entries) != 0 {
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Errorf("the configuration tree gained %v", names)
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
	b := New(WithServerSocket(socket))
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
