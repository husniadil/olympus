package tmux_test

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/husniadil/olympus/backend"
	"github.com/husniadil/olympus/backend/backendtest"
	"github.com/husniadil/olympus/backend/tmux"
)

var socketCounter atomic.Int64

// newIsolated builds a backend on a socket nobody else uses.
//
// Behavior §2.9 makes this non-negotiable: a shared socket addressed by a bare
// literal name is a real collision surface, and two processes pointing at one
// can crash the same server out from under each other. The operator's live
// server must never be touched, so every backend here gets a private -L socket
// that is torn down with the case.
func newIsolated(t backendtest.Reporter) backend.Backend {
	socket := fmt.Sprintf("olyt-%d-%d", os.Getpid(), socketCounter.Add(1))
	t.Cleanup(func() {
		// Best effort: a case that never started a server has nothing to kill.
		_ = exec.Command("tmux", "-L", socket, "kill-server").Run()
		// Killing the server does not unlink its socket file, so a suite that
		// stops here leaves one behind per case — hundreds of them across a
		// few runs, in a directory the operator shares with their own servers.
		_ = os.Remove(socketPath(socket))
	})
	return tmux.New(tmux.WithSocket(socket))
}

// socketPath is where tmux places a named socket: TMUX_TMPDIR or /tmp, then a
// per-uid directory, then the bare name.
func socketPath(socket string) string {
	dir := os.Getenv("TMUX_TMPDIR")
	if dir == "" {
		dir = "/tmp"
	}
	return filepath.Join(dir, fmt.Sprintf("tmux-%d", os.Getuid()), socket)
}

func requireTmux(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is not installed")
	}
}

func TestConformance(t *testing.T) {
	requireTmux(t)
	backendtest.Run(t, backendtest.Config{
		New: newIsolated,
		Expect: backendtest.Expectations{
			// tmux delivers the interrupt as a keypress into the pane's tty,
			// so the kernel raises it on the foreground process group. Neither
			// session shape can ignore it on entry, so both stop.
			InterruptShellBacked: backendtest.InterruptStops,
			InterruptExecSpawned: backendtest.InterruptStops,
		},
	})
}
