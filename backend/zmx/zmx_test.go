package zmx_test

import (
	"os"
	"os/exec"
	"testing"

	"github.com/husniadil/olympus/backend"
	"github.com/husniadil/olympus/backend/backendtest"
	"github.com/husniadil/olympus/backend/zmx"
)

// newIsolated builds a backend on a daemon nobody else uses.
//
// zmx has no equivalent of tmux's -L: sessions are global to one daemon per
// user, and the daemon's socket directory resolves from the environment. Naming
// test sessions carefully is NOT sufficient protection — however they are
// named, they still land on the operator's one shared daemon, and test churn
// there destabilizes real live attach clients. A private ZMX_DIR is the only
// isolation available, and it must cover raw verification calls too (§2.9).
func newIsolated(t backendtest.Reporter) backend.Backend {
	// Deliberately NOT the testing package's temp dir. That one embeds the
	// test's name in the path, and a socket path carries a hard 103-byte
	// budget (§2.5) — so a case with a descriptive name would be rejected for
	// its name rather than tested. A short directory keeps the budget for the
	// session names the cases actually use.
	dir, err := os.MkdirTemp(os.TempDir(), "olyz")
	if err != nil {
		t.Fatalf("creating a private socket directory: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })

	b := zmx.New(zmx.WithDir(dir))
	t.Cleanup(func() {
		// Kill whatever the case left behind, scoped to this directory alone.
		cmd := exec.Command("zmx", "list")
		cmd.Env = append(cmd.Environ(), "ZMX_DIR="+dir)
		out, err := cmd.Output()
		if err != nil {
			return
		}
		for _, name := range sessionNames(string(out)) {
			kill := exec.Command("zmx", "kill", name, "--force")
			kill.Env = append(kill.Environ(), "ZMX_DIR="+dir)
			_ = kill.Run()
		}
	})
	return b
}

func sessionNames(listing string) []string {
	var names []string
	for _, line := range splitLines(listing) {
		for _, field := range splitFields(line) {
			if len(field) > 5 && field[:5] == "name=" {
				names = append(names, field[5:])
			}
		}
	}
	return names
}

func requireZmx(t *testing.T) {
	t.Helper()
	skipUnlessFull(t)
	if _, err := exec.LookPath("zmx"); err != nil {
		// Skipping loudly: the zmx leg is expected to be absent on some
		// machines, and a silent skip reads as a pass.
		t.Skip("zmx is not installed, so the zmx conformance leg is not being run")
	}
}

func TestConformance(t *testing.T) {
	requireZmx(t)
	backendtest.Run(t, backendtest.Config{
		New: newIsolated,
		Expect: backendtest.Expectations{
			// A shell-backed session behaves exactly as on tmux once the
			// interrupt is delivered as a signal to the foreground process
			// group rather than as 0x03 through the terminal.
			InterruptShellBacked: backendtest.InterruptStops,
			// An exec-spawned session inherits SIGINT as SIG_IGN, and a signal
			// ignored on entry can never be trapped or reset. No delivery
			// mechanism can help, so graceful kill must fall through to a
			// forced kill (§2.8.1, cause 2).
			InterruptExecSpawned: backendtest.InterruptIneffective,
		},
	})
}

func splitLines(s string) []string {
	var out []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	if start < len(s) {
		out = append(out, s[start:])
	}
	return out
}

func splitFields(s string) []string {
	var out []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\t' || s[i] == ' ' {
			if i > start {
				out = append(out, s[start:i])
			}
			start = i + 1
		}
	}
	if start < len(s) {
		out = append(out, s[start:])
	}
	return out
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
