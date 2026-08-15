package olympus_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/husniadil/olympus"
	"github.com/husniadil/olympus/backend"
)

// These run the documented surface against real backends, isolated per §2.9.
// The point is that the layer's own decisions — defaults, locking, resolution,
// disclosure — hold when something real is underneath them.

var counter atomic.Int64

type leg struct {
	name string
	open func(t *testing.T) *olympus.Olympus
}

func legs(t *testing.T) []leg {
	t.Helper()
	var out []leg
	if _, err := exec.LookPath("tmux"); err == nil {
		out = append(out, leg{"tmux", openTmux})
	}
	if _, err := exec.LookPath("zmx"); err == nil {
		out = append(out, leg{"zmx", openZmx})
	}
	// Checked by RUNNING it, not by finding it: a dangling shim on PATH
	// satisfies a lookup and fails every call, which would report as a wall of
	// broken cases rather than an absent backend.
	if err := exec.Command("meja", "version").Run(); err == nil {
		out = append(out, leg{"meja", openMeja})
	}
	if len(out) == 0 {
		t.Skip("no backend is installed")
	}
	return out
}

func openTmux(t *testing.T) *olympus.Olympus {
	t.Helper()
	socket := fmt.Sprintf("olyo-%d-%d", os.Getpid(), counter.Add(1))
	ol, err := olympus.Open(olympus.WithBackend("tmux"), olympus.WithSocket(socket))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() {
		_ = ol.Close()
		_ = exec.Command("tmux", "-L", socket, "kill-server").Run()
		dir := os.Getenv("TMUX_TMPDIR")
		if dir == "" {
			dir = "/tmp"
		}
		_ = os.Remove(filepath.Join(dir, fmt.Sprintf("tmux-%d", os.Getuid()), socket))
	})
	return ol
}

// openMeja opens a handle on a meja server nobody else uses.
//
// A socket PATH, never a profile name: meja keeps session recovery files beside
// the socket, so a named profile would leave persisted sessions in the
// operator's own store to come back on their next restore (§2.9).
func openMeja(t *testing.T) *olympus.Olympus {
	t.Helper()
	dir, err := os.MkdirTemp(os.TempDir(), "olyo")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	socket := filepath.Join(dir, "m.sock")
	ol, err := olympus.Open(olympus.WithBackend("meja"), olympus.WithSocketPath(socket))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() {
		_ = ol.Close()
		_ = exec.Command("meja", "-S", socket, "kill-server").Run()
		_ = os.RemoveAll(dir)
	})
	return ol
}

func openZmx(t *testing.T) *olympus.Olympus {
	t.Helper()
	// Short, because a session's socket path carries a hard byte budget.
	dir, err := os.MkdirTemp(os.TempDir(), "olyo")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	ol, err := olympus.Open(olympus.WithBackend("zmx"), olympus.WithZmxDir(dir))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() {
		_ = ol.Close()
		_ = os.RemoveAll(dir)
	})
	return ol
}

func session(t *testing.T, ol *olympus.Olympus) *olympus.Session {
	t.Helper()
	name := fmt.Sprintf("oly-o%d", counter.Add(1))
	s, err := ol.Session(context.Background(), name, olympus.In(t.TempDir()))
	if err != nil {
		t.Fatalf("Session: %v", err)
	}
	t.Cleanup(func() { _, _ = s.Stop(context.Background(), olympus.Force()) })
	warmUp(t, s)
	return s
}

// warmUp blocks until the shell has provably executed something (§16).
func warmUp(t *testing.T, s *olympus.Session) {
	t.Helper()
	ctx := context.Background()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if err := s.Type(ctx, `printf 'ready-%d\n' 7`); err != nil {
			t.Fatalf("warming: %v", err)
		}
		if err := s.Submit(ctx); err != nil {
			t.Fatalf("warming: %v", err)
		}
		end := time.Now().Add(2 * time.Second)
		for time.Now().Before(end) {
			screen, err := s.Screen(ctx)
			if err == nil && strings.Contains(screen.Text, "ready-7") {
				return
			}
			time.Sleep(50 * time.Millisecond)
		}
	}
	t.Fatal("the shell never executed a command")
}

// Session is ensure-semantics: create, reuse, or replace-if-dead, with no
// separate create-versus-open decision for a caller to get wrong.
func TestSessionCreatesThenReuses(t *testing.T) {
	for _, l := range legs(t) {
		t.Run(l.name, func(t *testing.T) {
			ol := l.open(t)
			ctx := context.Background()
			name := fmt.Sprintf("oly-o%d", counter.Add(1))

			first, err := ol.Session(ctx, name, olympus.In(t.TempDir()))
			if err != nil {
				t.Fatalf("first Session: %v", err)
			}
			t.Cleanup(func() { _, _ = first.Stop(ctx, olympus.Force()) })
			if first.Outcome() != backend.OutcomeCreated {
				t.Errorf("first outcome %q, want %q", first.Outcome(), backend.OutcomeCreated)
			}

			second, err := ol.Session(ctx, name)
			if err != nil {
				t.Fatalf("second Session: %v", err)
			}
			if second.Outcome() != backend.OutcomeReused {
				t.Errorf("second outcome %q, want %q", second.Outcome(), backend.OutcomeReused)
			}
		})
	}
}

func TestExecReturnsOutputAndTheCommandsOwnExitCode(t *testing.T) {
	for _, l := range legs(t) {
		t.Run(l.name, func(t *testing.T) {
			s := session(t, l.open(t))
			ctx := context.Background()

			got, err := s.Exec(ctx, `printf 'lib-%d\n' 8`)
			if err != nil {
				t.Fatalf("Exec: %v", err)
			}
			if !strings.Contains(got.Output, "lib-8") {
				t.Errorf("output %q does not contain the command's output", got.Output)
			}
			if got.ExitCode != 0 {
				t.Errorf("exit code %d, want 0", got.ExitCode)
			}

			// A non-zero exit is a RESULT, not an error. Conflating them makes
			// an ordinary failing command look like Olympus broke.
			failed, err := s.Exec(ctx, "sh -c 'exit 4'")
			if err != nil {
				t.Fatalf("a failing command was reported as an error: %v", err)
			}
			if failed.ExitCode != 4 {
				t.Errorf("exit code %d, want 4", failed.ExitCode)
			}
		})
	}
}

func TestSendVerifiesBeforeSubmitting(t *testing.T) {
	for _, l := range legs(t) {
		t.Run(l.name, func(t *testing.T) {
			s := session(t, l.open(t))
			ctx := context.Background()

			if err := s.Send(ctx, `printf 'sent-%d\n' 9`); err != nil {
				t.Fatalf("Send: %v", err)
			}
			if _, err := s.WaitFor(ctx, `sent-9`, olympus.WaitTimeout(10*time.Second)); err != nil {
				t.Errorf("the sent text was never executed: %v", err)
			}
		})
	}
}

// Info is the only door onto the presence tri-state, so it must not error on an
// absent target — collapsing absent into an error would destroy the distinction
// between "definitely gone" and "could not ask".
func TestInfoAnswersAbsentWithoutErroring(t *testing.T) {
	for _, l := range legs(t) {
		t.Run(l.name, func(t *testing.T) {
			ol := l.open(t)
			got, err := ol.Info(context.Background(), "oly-never-existed")
			if err != nil {
				t.Fatalf("Info on an absent target errored: %v", err)
			}
			if got.State != backend.StateAbsent {
				t.Errorf("state %q, want %q", got.State, backend.StateAbsent)
			}
			if got.Session != nil || len(got.Panes) != 0 {
				t.Error("an absent target came back with a session or panes")
			}
			if got.Capabilities.Backend == "" {
				t.Error("capabilities are missing, so a caller cannot feature-probe from here")
			}
		})
	}
}

func TestInfoOnALiveSessionCarriesItsRows(t *testing.T) {
	for _, l := range legs(t) {
		t.Run(l.name, func(t *testing.T) {
			ol := l.open(t)
			s := session(t, ol)

			got, err := ol.Info(context.Background(), s.Name())
			if err != nil {
				t.Fatalf("Info: %v", err)
			}
			if got.State != backend.StatePresent {
				t.Fatalf("state %q, want %q", got.State, backend.StatePresent)
			}
			if got.Session == nil {
				t.Error("a present session came back with no row")
			}
			if len(got.Panes) == 0 {
				t.Error("a present session came back with no panes")
			}
		})
	}
}

// §0.8: an operation that means materially less on this backend says so once,
// through the result. It is not an error — the answer is real, just narrower.
func TestDegradedOperationsDiscloseThemselves(t *testing.T) {
	for _, l := range legs(t) {
		t.Run(l.name, func(t *testing.T) {
			ol := l.open(t)
			s := session(t, ol)

			screen, err := s.Screen(context.Background(), olympus.WithHistory(500))
			if err != nil {
				t.Fatalf("Screen: %v", err)
			}

			if ol.Backend() == backend.Zmx {
				if len(screen.Warnings) == 0 {
					t.Error("a history request on a backend that ignores it disclosed nothing")
				}
				var mentioned bool
				for _, w := range screen.Warnings {
					if w.Code != olympus.WarningDegraded {
						t.Errorf("warning code %q, want %q", w.Code, olympus.WarningDegraded)
					}
					if strings.Contains(w.Message, "history") {
						mentioned = true
					}
				}
				if !mentioned {
					t.Errorf("the warnings do not mention the ignored history request: %+v", screen.Warnings)
				}
			} else if len(screen.Warnings) != 0 {
				t.Errorf("a backend that honours the request warned anyway: %+v", screen.Warnings)
			}
		})
	}
}

func TestStopReportsHowTheSessionEnded(t *testing.T) {
	for _, l := range legs(t) {
		t.Run(l.name, func(t *testing.T) {
			ol := l.open(t)
			s := session(t, ol)
			ctx := context.Background()

			got, err := s.Stop(ctx)
			if err != nil {
				t.Fatalf("Stop: %v", err)
			}
			switch got.Outcome {
			case "gone", "graceful", "killed":
			default:
				t.Errorf("outcome %q, want one of gone, graceful or killed", got.Outcome)
			}

			// "gone" means WAS ALREADY GONE, which is a claim about what the
			// initial probe saw. A listing is eventually consistent after a
			// kill (§3.3), so the wait is part of the setup rather than
			// something being tested: without it the second stop legitimately
			// observes the stale row, interrupts, and reports graceful.
			deadline := time.Now().Add(10 * time.Second)
			for {
				info, err := ol.Info(ctx, s.Name())
				if err != nil {
					t.Fatalf("Info: %v", err)
				}
				if info.State == backend.StateAbsent {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("the stopped session never became absent")
				}
				time.Sleep(100 * time.Millisecond)
			}

			// Stopping something already stopped is not a failure: the desired
			// state holds either way, and no interrupt is sent.
			again, err := ol.Stop(ctx, s.Name())
			if err != nil {
				t.Fatalf("stopping an already-stopped session: %v", err)
			}
			if again.Outcome != "gone" {
				t.Errorf("outcome %q for an already-absent session, want gone", again.Outcome)
			}
		})
	}
}

// The resolved backend, never the requested one, is what a caller must be able
// to see — sessions are backend-scoped and never migrate.
func TestTheResolvedBackendIsObservable(t *testing.T) {
	for _, l := range legs(t) {
		t.Run(l.name, func(t *testing.T) {
			ol := l.open(t)
			if string(ol.Backend()) != l.name {
				t.Errorf("resolved %q, want %q", ol.Backend(), l.name)
			}
			if ol.Resolution().Reason != olympus.ReasonFlag {
				t.Errorf("reason %q, want %q", ol.Resolution().Reason, olympus.ReasonFlag)
			}
		})
	}
}

func TestOpeningAnAbsentSessionIsNotFound(t *testing.T) {
	for _, l := range legs(t) {
		t.Run(l.name, func(t *testing.T) {
			ol := l.open(t)
			_, err := ol.Open(context.Background(), "oly-never-existed")
			if !errors.Is(err, olympus.ErrNotFound) {
				t.Errorf("error is %v, want not-found", err)
			}
		})
	}
}

// The diagnostic must work when nothing is installed — that is the case it
// exists to explain — so it reports rather than failing.
func TestDiagnoseAlwaysReports(t *testing.T) {
	got := olympus.Diagnose(context.Background())
	if len(got.Backends) == 0 {
		t.Fatal("the diagnostic listed no backends")
	}
	for _, report := range got.Backends {
		if report.Name == "" {
			t.Error("a backend report has no name")
		}
		if report.Floor == "" {
			t.Errorf("%s has no version floor", report.Name)
		}
		if report.Installed {
			if report.Version == "" {
				t.Errorf("%s is installed but reports no version", report.Name)
			}
			if report.Isolation == "" {
				t.Errorf("%s does not say where its sessions live", report.Name)
			}
		}
	}
}

func TestDiagnoseNamesTheResolvedBackendAndWhy(t *testing.T) {
	got := olympus.Diagnose(context.Background())
	if got.Resolved.Problem != "" {
		t.Skipf("no backend resolves on this machine: %s", got.Resolved.Problem)
	}
	if got.Resolved.Backend == "" {
		t.Error("the diagnostic does not name the resolved backend")
	}
	if got.Resolved.Reason == "" {
		t.Error("the diagnostic does not say which rule chose it")
	}
}

// §17.5: pinning options takes something from the operator, so the diagnostic
// has to say what. A tool that quietly overrides a line in somebody's tmux.conf
// and never mentions it turns "my config is being ignored" into an unanswerable
// question — which is the exact failure `doctor` exists to prevent.
func TestDiagnoseDisclosesWhatItPins(t *testing.T) {
	diagnosis := olympus.Diagnose(context.Background())

	for _, report := range diagnosis.Backends {
		if report.Name != backend.Tmux {
			// Nothing is pinned on a backend that takes no configuration file,
			// and inventing an entry there would be a claim, not a disclosure.
			if len(report.Managed) != 0 {
				t.Errorf("%s reports pinned options %v, but nothing is pinned there", report.Name, report.Managed)
			}
			continue
		}
		if !report.Installed {
			continue
		}
		if report.Managed["history-limit"] == "" {
			t.Errorf("doctor does not disclose that Olympus pins history-limit: %v", report.Managed)
		}
		if _, ok := report.Managed["default-command"]; !ok {
			t.Errorf("doctor does not disclose that Olympus pins default-command: %v", report.Managed)
		}
	}
}

// §17.5: Olympus configures only servers it starts, so a caller pointed at one
// somebody else runs is subject to that server's settings — including a
// default-command that can make a run report the wrong exit code. Nothing else
// discloses this: the pins simply do not happen, silently and correctly.
//
// So the diagnostic MUST distinguish the two cases and report what is actually
// in effect, not merely what Olympus would have pinned.
func TestDiagnoseSeparatesAServerItStartedFromOneItFound(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is not installed")
	}

	dir, err := os.MkdirTemp(os.TempDir(), "olyd")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	socket := filepath.Join(dir, "s.sock")
	t.Cleanup(func() { _ = exec.Command("tmux", "-S", socket, "kill-server").Run() })

	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, ".tmux.conf"), []byte("set -g history-limit 7\n"), 0o600); err != nil {
		t.Fatalf("writing the operator's config: %v", err)
	}
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))

	opts := []olympus.Option{olympus.WithBackend("tmux"), olympus.WithSocketPath(socket)}
	ctx := context.Background()

	// Somebody else's server, started with their settings.
	if err := exec.Command("tmux", "-S", socket, "new-session", "-d", "-s", "theirs").Run(); err != nil {
		t.Fatalf("starting the operator's server: %v", err)
	}

	found := olympus.Diagnose(ctx, opts...).Resolved
	if found.Pinned {
		t.Errorf("doctor claims Olympus configured a server it merely found")
	}
	if found.Effective["history-limit"] != "7" {
		t.Errorf("doctor reports history-limit %q, want the operator's 7 that is actually in effect: %v",
			found.Effective["history-limit"], found.Effective)
	}

	// Now one Olympus starts for itself.
	_ = exec.Command("tmux", "-S", socket, "kill-server").Run()
	ol, err := olympus.Open(opts...)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = ol.Close() })
	if _, err := ol.Create(ctx, "ours", olympus.In(t.TempDir())); err != nil {
		t.Fatalf("Create: %v", err)
	}

	started := olympus.Diagnose(ctx, opts...).Resolved
	if !started.Pinned {
		t.Errorf("doctor does not recognise a server Olympus started as configured: %+v", started)
	}
	if started.Effective["history-limit"] == "7" {
		t.Errorf("doctor reports the operator's history-limit on a server Olympus started and pinned: %v", started.Effective)
	}
}

// Ownership cannot be inferred from the values. An operator whose own config
// happens to match what Olympus pins — and 50000 is an ordinary thing to set —
// would have their server reported as one Olympus started and configured,
// which is the single fact this report exists to establish.
//
// So a server Olympus starts is MARKED, in the same chain that starts it. A
// server it merely finds never receives the mark, because that chain never runs
// there (§17.5).
func TestDiagnoseDoesNotMistakeAMatchingConfigForOwnership(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is not installed")
	}

	dir, err := os.MkdirTemp(os.TempDir(), "olym")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	socket := filepath.Join(dir, "s.sock")
	t.Cleanup(func() { _ = exec.Command("tmux", "-S", socket, "kill-server").Run() })

	home := t.TempDir()
	// Exactly what Olympus would pin, set by somebody else.
	conf := "set -g history-limit 50000\nset -g default-command \"\"\n"
	if err := os.WriteFile(filepath.Join(home, ".tmux.conf"), []byte(conf), 0o600); err != nil {
		t.Fatalf("writing the operator's config: %v", err)
	}
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))

	if err := exec.Command("tmux", "-S", socket, "new-session", "-d", "-s", "theirs").Run(); err != nil {
		t.Fatalf("starting the operator's server: %v", err)
	}

	resolved := olympus.Diagnose(context.Background(),
		olympus.WithBackend("tmux"), olympus.WithSocketPath(socket)).Resolved
	if resolved.Pinned {
		t.Error("doctor reports a server Olympus never touched as one it started and configured, because the operator's own settings happened to match")
	}
}

// The story this exists for: a program inside a session says what it is doing,
// and a caller outside blocks until it says the thing they care about.
//
// A screen scrape cannot answer this reliably — a program sitting at a prompt
// and a program halfway through work can render identically — so the answer has
// to come from the program itself.
func TestWaitingForASessionToReportAStatus(t *testing.T) {
	for _, l := range legs(t) {
		t.Run(l.name, func(t *testing.T) {
			ol := l.open(t)
			s := session(t, ol)
			ctx := context.Background()

			if !ol.Capabilities().SessionStatus {
				if err := s.SetStatus(ctx, "working"); olympus.CodeOf(err) != backend.CodeUnsupported {
					t.Errorf("a backend that cannot carry a status reports %v, want UNSUPPORTED", olympus.CodeOf(err))
				}
				return
			}

			// Nobody has reported anything yet, so waiting must time out rather
			// than match an empty status against an empty want.
			if _, err := s.WaitForStatus(ctx, "ready", olympus.WaitTimeout(300*time.Millisecond)); olympus.CodeOf(err) != backend.CodeTimeout {
				t.Errorf("waiting on a status nobody set reports %v, want TIMEOUT", olympus.CodeOf(err))
			}

			go func() {
				time.Sleep(200 * time.Millisecond)
				_ = s.SetStatus(context.Background(), "ready")
			}()

			got, err := s.WaitForStatus(ctx, "ready", olympus.WaitTimeout(5*time.Second))
			if err != nil {
				t.Fatalf("WaitForStatus: %v", err)
			}
			if got != "ready" {
				t.Errorf("WaitForStatus returned %q, want %q", got, "ready")
			}
		})
	}
}
