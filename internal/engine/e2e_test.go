package engine_test

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/husniadil/olympus/backend"
	"github.com/husniadil/olympus/backend/tmux"
	"github.com/husniadil/olympus/backend/zmx"
	"github.com/husniadil/olympus/internal/engine"
)

// The engines are unit-tested against a scripted fake, which proves the rules.
// These run the same code against real multiplexers, which proves the rules
// were the right ones — a fake that agrees with a wrong assumption agrees
// perfectly.
//
// Isolation is the same hard requirement as everywhere else (§2.9): a private
// socket for tmux, a private and SHORT directory for zmx.

var e2eCounter atomic.Int64

type e2eBackend struct {
	name string
	make func(t *testing.T) backend.Backend
}

func e2eBackends(t *testing.T) []e2eBackend {
	t.Helper()
	skipUnlessFull(t)
	var out []e2eBackend
	if _, err := exec.LookPath("tmux"); err == nil {
		out = append(out, e2eBackend{"tmux", newE2ETmux})
	} else {
		t.Log("tmux is not installed, skipping its end-to-end leg")
	}
	if _, err := exec.LookPath("zmx"); err == nil {
		out = append(out, e2eBackend{"zmx", newE2EZmx})
	} else {
		t.Log("zmx is not installed, skipping its end-to-end leg")
	}
	if len(out) == 0 {
		t.Skip("no backend is installed")
	}
	return out
}

func newE2ETmux(t *testing.T) backend.Backend {
	t.Helper()
	socket := fmt.Sprintf("olye-%d-%d", os.Getpid(), e2eCounter.Add(1))
	t.Cleanup(func() {
		_ = exec.Command("tmux", "-L", socket, "kill-server").Run()
		dir := os.Getenv("TMUX_TMPDIR")
		if dir == "" {
			dir = "/tmp"
		}
		_ = os.Remove(filepath.Join(dir, fmt.Sprintf("tmux-%d", os.Getuid()), socket))
	})
	return tmux.New(tmux.WithSocket(socket))
}

func newE2EZmx(t *testing.T) backend.Backend {
	t.Helper()
	// Short, because a session's socket path carries a hard byte budget and
	// the testing package's temp dir embeds the test's name (§2.5).
	dir, err := os.MkdirTemp(os.TempDir(), "olye")
	if err != nil {
		t.Fatalf("creating a private socket directory: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return zmx.New(zmx.WithDir(dir))
}

func e2eSession(t *testing.T, b backend.Backend) string {
	t.Helper()
	name := fmt.Sprintf("oly-e%d", e2eCounter.Add(1))
	if _, err := b.Create(context.Background(), backend.CreateSpec{Name: name, Dir: t.TempDir(), Cols: 80, Rows: 24}); err != nil {
		t.Fatalf("creating %s: %v", name, err)
	}
	t.Cleanup(func() { _ = b.Kill(context.Background(), name) })
	e2eWarm(t, b, name)
	return name
}

// e2eWarm blocks until the shell has provably executed something. Without it a
// run injects into a shell that is not reading yet, the line is lost, and the
// failure surfaces as a timeout somewhere else entirely (§16).
func e2eWarm(t *testing.T, b backend.Backend, target string) {
	t.Helper()
	ctx := context.Background()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		// One atomic submit, the way backendtest.Env.Warm does it (§16): a
		// retried probe must never leave a typed line behind to double.
		if err := b.SendAtomic(ctx, target, `printf 'ready-%d\n' 7`); err != nil {
			t.Fatalf("warming: %v", err)
		}
		end := time.Now().Add(2 * time.Second)
		for time.Now().Before(end) {
			capture, err := b.Screen(ctx, target, backend.ScreenOpts{})
			if err == nil && strings.Contains(capture.Text, "ready-7") {
				return
			}
			time.Sleep(50 * time.Millisecond)
		}
	}
	t.Fatalf("the shell never executed a command")
}

func e2eRunner(t *testing.T, b backend.Backend, target string) engine.Runner {
	t.Helper()
	locks, err := engine.NewLocksIn(filepath.Join(t.TempDir(), "locks"))
	if err != nil {
		t.Fatalf("NewLocksIn: %v", err)
	}
	return engine.Runner{
		Backend:  b,
		Locks:    locks,
		Key:      engine.LockKey{Backend: b.Capabilities().Backend, Scope: "e2e", Session: target},
		LockWait: 10 * time.Second,
		Timeout:  30 * time.Second,
		Poll:     150 * time.Millisecond,
	}
}

func TestEndToEndRunCapturesOutputAndExitCode(t *testing.T) {
	for _, be := range e2eBackends(t) {
		t.Run(be.name, func(t *testing.T) {
			b := be.make(t)
			target := e2eSession(t, b)

			got, err := e2eRunner(t, b, target).Exec(context.Background(), target, `printf 'e2e-%d\n' 5`)
			if err != nil {
				t.Fatalf("Exec: %v", err)
			}
			if !strings.Contains(got.Output, "e2e-5") {
				t.Errorf("output %q does not contain the command's own output", got.Output)
			}
			// The markers themselves must not leak into what the caller gets.
			if strings.Contains(got.Output, "OLY_S_") || strings.Contains(got.Output, "OLY_D_") {
				t.Errorf("the sentinel markers leaked into the output: %q", got.Output)
			}
			if got.ExitCode != 0 {
				t.Errorf("exit code %d, want 0", got.ExitCode)
			}
		})
	}
}

// A failing command is a normal result carried in the payload, not an
// infrastructure failure.
func TestEndToEndRunReportsTheCommandsOwnExitCode(t *testing.T) {
	for _, be := range e2eBackends(t) {
		t.Run(be.name, func(t *testing.T) {
			b := be.make(t)
			target := e2eSession(t, b)

			// A subshell, deliberately: a bare "exit 3" would exit the
			// session's OWN shell and take the session down with it, which is a
			// different thing from a command that failed.
			got, err := e2eRunner(t, b, target).Exec(context.Background(), target, "sh -c 'exit 3'")
			if err != nil {
				t.Fatalf("Exec: %v", err)
			}
			if got.ExitCode != 3 {
				t.Errorf("exit code %d, want 3", got.ExitCode)
			}
		})
	}
}

// The detached path start-to-finish, including that the state lives entirely in
// the scrollback: nothing is written down, and the id is the whole handle.
func TestEndToEndDetachedRunPollsToCompletion(t *testing.T) {
	for _, be := range e2eBackends(t) {
		t.Run(be.name, func(t *testing.T) {
			b := be.make(t)
			target := e2eSession(t, b)
			r := e2eRunner(t, b, target)
			ctx := context.Background()

			id, err := r.Start(ctx, target, `sleep 1; printf 'detached-%d\n' 6`)
			if err != nil {
				t.Fatalf("Start: %v", err)
			}

			deadline := time.Now().Add(30 * time.Second)
			for {
				got, err := r.PollRun(ctx, target, id)
				if err != nil {
					t.Fatalf("PollRun: %v", err)
				}
				if got.Status == engine.PollCompleted {
					if got.ExitCode == nil || *got.ExitCode != 0 {
						t.Errorf("exit code %v, want 0", got.ExitCode)
					}
					if !strings.Contains(got.Output, "detached-6") {
						t.Errorf("output %q does not contain the command's output", got.Output)
					}
					return
				}
				if got.Status == engine.PollDied {
					t.Fatalf("the run reported died: %s", got.Reason)
				}
				if time.Now().After(deadline) {
					t.Fatal("the detached run never completed")
				}
				time.Sleep(150 * time.Millisecond)
			}
		})
	}
}

// Killing the session under a detached run is the died path, and it must not
// carry an exit code.
func TestEndToEndPollingAKilledSessionReportsDied(t *testing.T) {
	for _, be := range e2eBackends(t) {
		t.Run(be.name, func(t *testing.T) {
			b := be.make(t)
			target := e2eSession(t, b)
			r := e2eRunner(t, b, target)
			ctx := context.Background()

			id, err := r.Start(ctx, target, "sleep 60")
			if err != nil {
				t.Fatalf("Start: %v", err)
			}
			if err := b.Kill(ctx, target); err != nil {
				t.Fatalf("killing: %v", err)
			}

			deadline := time.Now().Add(10 * time.Second)
			for {
				got, err := r.PollRun(ctx, target, id)
				if err != nil {
					t.Fatalf("PollRun: %v", err)
				}
				if got.Status == engine.PollDied {
					if got.ExitCode != nil {
						t.Errorf("a died run carries exit code %d, want none", *got.ExitCode)
					}
					return
				}
				if time.Now().After(deadline) {
					t.Fatalf("polling a killed session still reports %q", got.Status)
				}
				time.Sleep(100 * time.Millisecond)
			}
		})
	}
}

func TestEndToEndVerifiedDeliveryLandsAndSubmits(t *testing.T) {
	for _, be := range e2eBackends(t) {
		t.Run(be.name, func(t *testing.T) {
			b := be.make(t)
			target := e2eSession(t, b)
			locks, err := engine.NewLocksIn(filepath.Join(t.TempDir(), "locks"))
			if err != nil {
				t.Fatalf("NewLocksIn: %v", err)
			}

			d := engine.Delivery{
				Backend:  b,
				Locks:    locks,
				Key:      engine.LockKey{Backend: b.Capabilities().Backend, Scope: "e2e", Session: target},
				LockWait: 10 * time.Second,
				Budget:   10 * time.Second,
				Poll:     100 * time.Millisecond,
			}
			if err := d.VerifiedSubmit(context.Background(), target, `printf 'verified-%d\n' 4`); err != nil {
				t.Fatalf("VerifiedSubmit: %v", err)
			}

			// The expanded output, never the typed line, proves it was
			// submitted rather than merely typed (§16).
			deadline := time.Now().Add(10 * time.Second)
			for {
				capture, err := b.Screen(context.Background(), target, backend.ScreenOpts{})
				if err == nil && strings.Contains(capture.Text, "verified-4") {
					return
				}
				if time.Now().After(deadline) {
					t.Fatal("the verified text was never executed, so it was typed but not submitted")
				}
				time.Sleep(100 * time.Millisecond)
			}
		})
	}
}

func TestEndToEndEnsureCreatesThenReuses(t *testing.T) {
	for _, be := range e2eBackends(t) {
		t.Run(be.name, func(t *testing.T) {
			b := be.make(t)
			ctx := context.Background()
			name := fmt.Sprintf("oly-e%d", e2eCounter.Add(1))
			spec := backend.CreateSpec{Name: name, Dir: t.TempDir(), Cols: 80, Rows: 24}
			t.Cleanup(func() { _ = b.Kill(ctx, name) })

			first, err := engine.Ensure(ctx, b, spec)
			if err != nil {
				t.Fatalf("first Ensure: %v", err)
			}
			if first.Outcome != backend.OutcomeCreated {
				t.Errorf("first outcome %q, want %q", first.Outcome, backend.OutcomeCreated)
			}

			second, err := engine.Ensure(ctx, b, spec)
			if err != nil {
				t.Fatalf("second Ensure: %v", err)
			}
			if second.Outcome != backend.OutcomeReused {
				t.Errorf("second outcome %q, want %q", second.Outcome, backend.OutcomeReused)
			}
		})
	}
}

// §2.6 requires this asserted explicitly: the reaped branch is unreachable on
// both shipped backends, because a finished session is indistinguishable from
// an absent one. Stating it as a test means a future backend that starts
// leaving dead rows behind surfaces here instead of silently changing
// behaviour.
func TestEndToEndAFinishedSessionEnsuresAsCreatedNotReaped(t *testing.T) {
	for _, be := range e2eBackends(t) {
		t.Run(be.name, func(t *testing.T) {
			b := be.make(t)
			ctx := context.Background()
			name := fmt.Sprintf("oly-e%d", e2eCounter.Add(1))
			t.Cleanup(func() { _ = b.Kill(ctx, name) })

			// A session that has finished. Reached by killing it rather than
			// by racing a fast-exiting command: the point under test is what
			// ensure does with a session that is no longer there, and how it
			// got that way is not part of it.
			if _, err := b.Create(ctx, backend.CreateSpec{Name: name, Dir: t.TempDir(), Cols: 80, Rows: 24}); err != nil {
				t.Fatalf("creating: %v", err)
			}
			if err := b.Kill(ctx, name); err != nil {
				t.Fatalf("killing: %v", err)
			}
			deadline := time.Now().Add(10 * time.Second)
			for b.Probe(ctx, name) == backend.StatePresent {
				if time.Now().After(deadline) {
					t.Fatal("the killed session never became absent")
				}
				time.Sleep(100 * time.Millisecond)
			}

			got, err := engine.Ensure(ctx, b, backend.CreateSpec{Name: name, Dir: t.TempDir(), Cols: 80, Rows: 24})
			if err != nil {
				t.Fatalf("Ensure: %v", err)
			}
			if got.Outcome != backend.OutcomeCreated {
				t.Errorf("outcome %q, want %q — a finished session is indistinguishable from an absent one on this backend", got.Outcome, backend.OutcomeCreated)
			}
		})
	}
}

// skipUnlessFull skips work that drives a real multiplexer when the gate is
// running in short mode.
//
// `make test` is the loop a change is iterated against, and it has to be fast
// enough to run on every edit; `make test-full` is the gate before a commit.
// Splitting them is NOT a reduction in coverage — nothing is deleted, and the
// full gate still runs everything. It is a split between the two questions being
// asked: "did I just break the logic" is answerable in seconds, and paying a
// minute and a half for it means the answer gets asked less often, which is how
// coverage is really lost.
func skipUnlessFull(t *testing.T) {
	t.Helper()
	if testing.Short() {
		t.Skip("driving a real multiplexer; run `make test-full` for this")
	}
}
