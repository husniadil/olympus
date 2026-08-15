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
	// Addressed by PATH rather than by name, so the socket lives in a directory
	// this test owns and disappears with it. A named socket lands in the
	// directory tmux shares with every server the operator runs, and killing a
	// server does not unlink its socket — which left hundreds of dead files
	// there before this changed.
	//
	// The directory is short and made outside the testing package's own temp
	// dir: that one embeds the test's name, and a socket path carries a hard
	// byte budget.
	dir, err := os.MkdirTemp(os.TempDir(), "olyt")
	if err != nil {
		t.Fatalf("creating a private socket directory: %v", err)
	}
	path := filepath.Join(dir, fmt.Sprintf("s%d.sock", socketCounter.Add(1)))

	t.Cleanup(func() {
		// Best effort: a case that never started a server has nothing to kill.
		_ = exec.Command("tmux", "-S", path, "kill-server").Run()
		_ = os.RemoveAll(dir)
	})
	return tmux.New(tmux.WithSocketPath(path))
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
