package tmux_test

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/husniadil/olympus/backend"
	"github.com/husniadil/olympus/backend/tmux"
)

// §13.2 A tmux server is a socket NAME in tmux's per-user directory, and the
// directory scanned is the one tmux itself resolves: TMUX_TMPDIR when set.
// Scanned against a private directory holding sockets nothing listens on, so
// every row is a known server that is not running — and the operator's own
// directory is never read.
func TestServersScansTheSocketDirectory(t *testing.T) {
	requireTmuxBinary(t)
	base := shortTempDir(t)
	t.Setenv("TMUX_TMPDIR", base)
	dir := filepath.Join(base, fmt.Sprintf("tmux-%d", os.Getuid()))
	if got := tmux.SocketDir(); got != dir {
		t.Fatalf("SocketDir is %q, want %q", got, dir)
	}

	b := tmux.New()
	empty, err := b.Servers(context.Background())
	if err != nil || len(empty) != 0 {
		t.Fatalf("a directory tmux has never made lists %v (%v), want none and no error", empty, err)
	}

	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"default", "work"} {
		l, err := net.Listen("unix", filepath.Join(dir, name))
		if err != nil {
			t.Fatalf("placing a socket file: %v", err)
		}
		// Closed at once, keeping the file: nothing answers on it, which is
		// exactly what a socket left behind by a killed server looks like.
		l.(*net.UnixListener).SetUnlinkOnClose(false)
		_ = l.Close()
	}
	if err := os.WriteFile(filepath.Join(dir, "not-a-socket"), nil, 0o600); err != nil {
		t.Fatal(err)
	}

	servers, err := b.Servers(context.Background())
	if err != nil {
		t.Fatalf("listing: %v", err)
	}
	want := []backend.Server{
		{Name: "default", SocketPath: filepath.Join(dir, "default"), Running: false, Default: true, Dir: dir},
		{Name: "work", SocketPath: filepath.Join(dir, "work"), Running: false, Default: false, Dir: dir},
	}
	if len(servers) != len(want) {
		t.Fatalf("listed %+v, want %+v", servers, want)
	}
	for i := range want {
		if servers[i] != want[i] {
			t.Errorf("row %d is %+v, want %+v", i, servers[i], want[i])
		}
	}

	if err := b.StopServer(context.Background(), "nonesuch"); backend.CodeOf(err) != backend.CodeSessionNotFound {
		t.Errorf("stopping an unknown server is %q (%v), want %q", backend.CodeOf(err), err, backend.CodeSessionNotFound)
	}
	if err := b.StopServer(context.Background(), "../escape"); backend.CodeOf(err) != backend.CodeUsage {
		t.Errorf("a path as a server name is %q (%v), want %q", backend.CodeOf(err), err, backend.CodeUsage)
	}
	// A socket with nothing behind it is already in the desired state.
	if err := b.StopServer(context.Background(), "work"); err != nil {
		t.Errorf("stopping a server that is not running: %v", err)
	}
}

// §13.2 A running server is reported running, and StopServer takes it down.
// The server is private: its socket is a PATH inside a directory this test
// owns, which is also the directory the listing scans.
func TestServersSeesARunningServerAndStopsIt(t *testing.T) {
	requireTmux(t)
	base := shortTempDir(t)
	t.Setenv("TMUX_TMPDIR", base)
	dir := tmux.SocketDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	socket := filepath.Join(dir, "live")
	t.Cleanup(func() { _ = exec.Command("tmux", "-S", socket, "kill-server").Run() })
	if out, err := exec.Command("tmux", "-S", socket, "new-session", "-d", "-x", "80", "-y", "24").CombinedOutput(); err != nil {
		t.Fatalf("starting a private server: %v\n%s", err, out)
	}

	b := tmux.New()
	servers, err := b.Servers(context.Background())
	if err != nil {
		t.Fatalf("listing: %v", err)
	}
	if len(servers) != 1 || servers[0].Name != "live" || !servers[0].Running {
		t.Fatalf("listed %+v, want one running server named live", servers)
	}

	if err := b.StopServer(context.Background(), "live"); err != nil {
		t.Fatalf("stopping: %v", err)
	}
	if err := exec.Command("tmux", "-S", socket, "list-sessions").Run(); err == nil {
		t.Error("the server still answers after StopServer")
	}
	servers, err = b.Servers(context.Background())
	if err != nil {
		t.Fatalf("listing after the stop: %v", err)
	}
	if len(servers) != 1 || servers[0].Running {
		t.Errorf("after the stop the listing is %+v, want the same server, not running", servers)
	}
}

// requireTmuxBinary skips without the full-gate gate: a scan of a private
// directory runs tmux only to ask a dead socket whether it answers.
func requireTmuxBinary(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is not installed")
	}
}

// shortTempDir is a private directory short enough to hold a socket path.
func shortTempDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp(os.TempDir(), "olyt")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}
