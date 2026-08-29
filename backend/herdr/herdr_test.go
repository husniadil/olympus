package herdr_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/husniadil/olympus/backend"
	"github.com/husniadil/olympus/backend/backendtest"
	"github.com/husniadil/olympus/backend/herdr"
)

// newIsolated builds a backend on a server nobody else addresses.
//
// A private SOCKET is necessary and, on this backend, not sufficient. herdr
// keeps the unnamed session's persisted layout in its CONFIGURATION directory
// rather than beside its socket (src/session.rs:157-165), so a second server on
// a private socket still writes the operator's own `session.json` and their
// saved workspaces come back changed. WithSocketPath moves the configuration
// and state directories with the socket for exactly that reason, and this test
// only has to put the socket somewhere it owns (§2.9).
//
// The server is stopped in cleanup on every path, including a failing case: a
// leaked one holds a login shell open for as long as the machine runs.
func newIsolated(t backendtest.Reporter) backend.Backend {
	// Deliberately NOT the testing package's temp dir. That one embeds the
	// test's name in the path, and a socket path carries a hard budget the
	// server enforces by refusing to bind — so a case with a descriptive name
	// would fail for its name rather than be tested. Measured: the failure is
	// `local socket name length exceeds capacity of sun_path of sockaddr_un`,
	// which names neither Olympus nor the path it chose.
	dir, err := os.MkdirTemp(os.TempDir(), "olyh")
	if err != nil {
		t.Fatalf("creating a private socket directory: %v", err)
	}

	b := herdr.New(herdr.WithSocketPath(filepath.Join(dir, "h.sock")))
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := b.Stop(ctx); err != nil {
			t.Errorf("stopping the test server at %s: %v", b.Scope(), err)
		}
		_ = os.RemoveAll(dir)
	})
	return b
}

func requireHerdr(t *testing.T) {
	t.Helper()
	skipUnlessFull(t)
	// Runnable, not merely present: a version-manager shim left behind by an
	// uninstalled tool satisfies a lookup and fails every call, which produces
	// a wall of broken cases instead of one honest skip.
	if err := exec.Command("herdr", "--version").Run(); err != nil {
		t.Skip("herdr is not installed or not runnable, so the herdr conformance leg is not being run")
	}
}

func TestConformance(t *testing.T) {
	requireHerdr(t)
	backendtest.Run(t, backendtest.Config{
		New: newIsolated,
		Expect: backendtest.Expectations{
			// A pane is a real PTY with its line discipline intact, so writing
			// 0x03 into it reaches the foreground process group as SIGINT.
			// This is the contrast with zmx, where the same write generates no
			// signal at all (§2.8.1, cause 1).
			InterruptShellBacked: backendtest.InterruptStops,
			// herdr cannot spawn a session onto an argv (§2.3.1), so the suite
			// reaches the second session shape by handing the shell over with
			// `exec`. A program a shell execs inherits an ordinary SIGINT
			// disposition rather than the SIG_IGN a zmx spawn leaves it with,
			// so it stops — the two shapes converge here rather than diverging.
			InterruptExecSpawned: backendtest.InterruptStops,
		},
		// Raised over the defaults because every case boots its own server,
		// and a cold one has a login shell to start behind it.
		Budgets: backendtest.Budgets{Warm: 40 * time.Second},
	})
}

// skipUnlessFull skips work that drives a real multiplexer when the gate is
// running in short mode. See the Makefile: `make test` is the fast loop, and
// `make test-full` still runs everything.
func skipUnlessFull(t *testing.T) {
	t.Helper()
	if testing.Short() {
		t.Skip("driving a real multiplexer; run `make test-full` for this")
	}
}
