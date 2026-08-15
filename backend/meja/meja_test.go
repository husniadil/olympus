package meja_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/husniadil/olympus/backend"
	"github.com/husniadil/olympus/backend/backendtest"
	"github.com/husniadil/olympus/backend/meja"
)

// newIsolated builds a backend on a daemon nobody else uses.
//
// meja addresses a server either by profile name (-L, resolved under ~/.meja)
// or by exact socket path (-S). Only the path form is safe here: a profile
// lands in the directory shared with every server the operator runs, and meja
// also keeps its session recovery files beside the socket — so a named profile
// would leave persisted sessions in the operator's own store, which would come
// back on their next restore (§2.9).
func newIsolated(t backendtest.Reporter) backend.Backend {
	// Deliberately not the testing package's temp dir: its path embeds the
	// test's name, and a unix socket path is capped near 104 bytes, so a
	// descriptively named case would be rejected for its name.
	dir, err := os.MkdirTemp(os.TempDir(), "olym")
	if err != nil {
		t.Fatalf("creating a private socket directory: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })

	socket := filepath.Join(dir, "m.sock")
	t.Cleanup(func() { _ = exec.Command("meja", "-S", socket, "kill-server").Run() })
	return meja.New(meja.WithSocketPath(socket))
}

// requireMeja skips loudly, and checks the binary RUNS rather than merely
// existing. A dangling shim on PATH satisfies a lookup and fails every call,
// which would report as a wall of broken cases rather than an absent backend.
func requireMeja(t *testing.T) {
	t.Helper()
	if err := exec.Command("meja", "version").Run(); err != nil {
		t.Skip("meja is not installed or not runnable, so the meja conformance leg is not being run")
	}
}

func TestConformance(t *testing.T) {
	requireMeja(t)
	backendtest.Run(t, backendtest.Config{
		New: newIsolated,
		Expect: backendtest.Expectations{
			InterruptShellBacked: backendtest.InterruptStops,
			InterruptExecSpawned: backendtest.InterruptStops,
		},
	})
}
