//go:build darwin || linux

package olympus_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/creack/pty"
)

// §8.3: an attach keeps the session's size in step with the caller's, by two
// separate routes — SIGWINCH for a caller with a real window, and an in-band
// control line for one without.
//
// ParseResizeControl was unit-tested; nothing checked that a parsed size ever
// reached a pane. A parser can be perfectly correct while the size is dropped on
// the floor, and the symptom is a session that renders at 80x24 forever — which
// looks like a backend limitation rather than a bug in the door.
//
// Both cases ask the SESSION how big it thinks it is, via `tput`, rather than
// asking Olympus what it believes it set. Only the session's answer is evidence.

// A tmux client reserves one row for its status line, so a session inside a
// 40-row client reports 39. Measured, not assumed: the request and the result
// legitimately differ by one, and a test demanding equality would fail forever
// against correct behaviour.
const statusRowAllowance = 1

func TestAnInBandResizeReachesThePane(t *testing.T) {
	binary, where, session := attachFixture(t, "inband")

	if cols, rows := paneSize(t, binary, where, session); cols != 80 {
		t.Fatalf("the session started at %dx%d, so a change to 100 columns would prove nothing", cols, rows)
	}

	// A caller whose stdin is a pipe has no window and therefore no SIGWINCH.
	// The control line in the stream is the only way it can say how big to be.
	client := exec.Command(binary, append(where, "attach", session)...)
	client.Stdin = strings.NewReader("\x1b]olympus;resize;100;40\x07")
	// Opened, not fabricated. `os.NewFile(0, os.DevNull)` builds a File around
	// fd 0 and merely NAMES it /dev/null — the client's stdout then points at
	// whatever fd 0 happens to be, which on Linux is not writable here and kills
	// the client before it reads the control line. It survived on macOS, so the
	// test passed there while proving nothing.
	discard, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		t.Fatalf("opening %s: %v", os.DevNull, err)
	}
	t.Cleanup(func() { _ = discard.Close() })
	client.Stdout, client.Stderr = discard, discard
	// tmux refuses to attach to a terminal it cannot identify, and a bare
	// container has no TERM at all. Without this the client exits before it ever
	// reads the control line, and the test reports a resize that did not
	// propagate — blaming the product for the harness's missing environment.
	client.Env = append(os.Environ(), "TERM=xterm-256color")
	if err := client.Start(); err != nil {
		t.Fatalf("starting the attach client: %v", err)
	}
	t.Cleanup(func() { _ = client.Process.Kill(); _, _ = client.Process.Wait() })

	waitForSize(t, binary, where, session, 100, 40)
}

func TestAWindowChangeReachesThePane(t *testing.T) {
	binary, where, session := attachFixture(t, "winch")

	ptmx, tty, err := pty.Open()
	if err != nil {
		t.Fatalf("opening a terminal to be the outer one: %v", err)
	}
	t.Cleanup(func() { _ = ptmx.Close(); _ = tty.Close() })
	if err := pty.Setsize(ptmx, &pty.Winsize{Cols: 80, Rows: 24}); err != nil {
		t.Fatalf("sizing the outer terminal: %v", err)
	}

	client := exec.Command(binary, append(where, "attach", session)...)
	client.Stdin, client.Stdout, client.Stderr = tty, tty, tty
	client.Env = append(os.Environ(), "TERM=xterm-256color")
	if err := client.Start(); err != nil {
		t.Fatalf("starting the attach client: %v", err)
	}
	t.Cleanup(func() { _ = client.Process.Kill(); _, _ = client.Process.Wait() })

	// The first sync happens at attach, before any signal: without it a session
	// keeps whatever size it was created with until the operator happens to
	// resize their window.
	waitForSize(t, binary, where, session, 80, 24)

	// The operator drags the window edge. A kernel delivers SIGWINCH to the
	// terminal's foreground process group; this client is deliberately not a
	// session leader — making it one revokes the test's own terminal when it
	// dies — so the signal is delivered here instead. What is under test is what
	// the client does with it, not who sent it.
	if err := pty.Setsize(ptmx, &pty.Winsize{Cols: 120, Rows: 45}); err != nil {
		t.Fatalf("resizing the outer terminal: %v", err)
	}
	if err := client.Process.Signal(syscall.SIGWINCH); err != nil {
		t.Fatalf("signalling the window change: %v", err)
	}

	waitForSize(t, binary, where, session, 120, 45)
}

// attachFixture builds the binary and starts a session on a private tmux server.
func attachFixture(t *testing.T, name string) (binary string, where []string, session string) {
	t.Helper()
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is not installed")
	}
	binary = buildOlympus(t)

	// Deliberately NOT t.TempDir(). A unix socket's path is capped at ~104
	// bytes and the test's temp directory is longer than that on this harness,
	// so the server would fail to bind with "File name too long" — an error
	// about the test's own plumbing that reads like a product failure.
	dir, err := os.MkdirTemp("/tmp", "olyz")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })

	socket := filepath.Join(dir, "s")
	t.Cleanup(func() { _ = exec.Command("tmux", "-S", socket, "kill-server").Run() })

	where = []string{"--backend", "tmux", "--socket-path", socket}
	session = name
	if out, err := exec.Command(binary, append(where, "start", session)...).CombinedOutput(); err != nil {
		t.Fatalf("start: %v\n%s", err, out)
	}
	return binary, where, session
}

// paneSize asks the session how big it thinks it is.
func paneSize(t *testing.T, binary string, where []string, session string) (cols, rows int) {
	t.Helper()
	out, err := exec.Command(binary, append(where, "run", session, "tput cols; tput lines")...).CombinedOutput()
	if err != nil {
		t.Fatalf("asking the session its size: %v\n%s", err, out)
	}
	fields := strings.Fields(string(out))
	if len(fields) < 2 {
		t.Fatalf("the session reported %q, which is not a size", out)
	}
	cols, _ = strconv.Atoi(fields[len(fields)-2])
	rows, _ = strconv.Atoi(fields[len(fields)-1])
	return cols, rows
}

// waitForSize polls until the pane reports the requested size, because a resize
// travels through a client and a server before it lands.
func waitForSize(t *testing.T, binary string, where []string, session string, wantCols, wantRows int) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	var cols, rows int
	for {
		cols, rows = paneSize(t, binary, where, session)
		if cols == wantCols && wantRows-rows <= statusRowAllowance && wantRows-rows >= 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("the pane is %dx%d, want %dx%d (rows may be up to %d short for a status line)",
				cols, rows, wantCols, wantRows, statusRowAllowance)
		}
		time.Sleep(200 * time.Millisecond)
	}
}
