package olympus_test

import (
	"context"
	"encoding/json"
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
	skipUnlessFull(t)
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
	for _, l := range legs(t) {
		t.Run(l.name, func(t *testing.T) {
			ol := l.open(t)
			s := session(t, ol)

			screen, err := s.Screen(context.Background(), olympus.WithHistory(500))
			if err != nil {
				t.Fatalf("Screen: %v", err)
			}

			// Branched on the CAPABILITY, not on the backend's name. Naming a
			// backend here made "degraded" and "is zmx" the same statement,
			// which held only while zmx was the only degraded one — and broke
			// the moment a third backend shared one of its gaps. What a caller
			// reacts to is the capability, and so is what this asserts.
			caps := ol.Capabilities()

			if caps.NativeScrollback {
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
			}

			// Whatever the backend does honour must not produce a warning, and
			// every warning it does produce must correspond to a capability it
			// declares false.
			for _, w := range screen.Warnings {
				switch {
				case strings.Contains(w.Message, "history") && !caps.NativeScrollback:
					t.Errorf("a backend that honours history warned about it: %s", w.Message)
				case strings.Contains(w.Message, "alt-screen") && caps.TracksAltScreen:
					t.Errorf("a backend that tracks the alternate screen warned about it: %s", w.Message)
				}
			}
		})
	}
}

func TestStopReportsHowTheSessionEnded(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
			// "On PATH" and "runnable" are different things, and the gap is not
			// hypothetical: a version-manager shim left behind by an uninstalled
			// tool satisfies a lookup and fails every call. The diagnostic
			// reporting it as installed with no version is the honest answer —
			// that IS the environment — so the assertion holds only for a
			// backend that actually runs.
			runnable := exec.Command(string(report.Name), "-V").Run() == nil ||
				exec.Command(string(report.Name), "version").Run() == nil
			if report.Version == "" && runnable {
				t.Errorf("%s is installed and runnable but reports no version", report.Name)
			}
			if report.Isolation == "" {
				t.Errorf("%s does not say where its sessions live", report.Name)
			}
		}
	}
}

func TestDiagnoseNamesTheResolvedBackendAndWhy(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
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
	skipUnlessFull(t)
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
	skipUnlessFull(t)
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
	t.Parallel()
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

// §10: a pane id addresses the session that owns it, on every backend, in that
// backend's own spelling.
//
// Written as one rule across all three rather than per backend, because the
// spellings are what differ and the rule is what must not: tmux writes "%0",
// meja writes "1", and zmx has no panes at all so its synthesized id is the
// session's own name. A caller that reads an id out of `panes` must be able to
// hand it straight back as a target without knowing which backend it came from.
func TestAPaneIDAddressesItsSession(t *testing.T) {
	t.Parallel()
	for _, l := range legs(t) {
		t.Run(l.name, func(t *testing.T) {
			ol := l.open(t)
			s := session(t, ol)
			ctx := context.Background()

			panes, err := ol.Panes(ctx, s.Name())
			if err != nil {
				t.Fatalf("Panes: %v", err)
			}
			if len(panes) == 0 {
				t.Fatalf("the session reports no panes at all")
			}
			id := panes[0].ID

			info, err := ol.Info(ctx, id)
			if err != nil {
				t.Fatalf("addressing pane %q: %v", id, err)
			}
			if info.State != backend.StatePresent {
				t.Fatalf("pane %q reports %q, want present — an id read from `panes` must work as a target",
					id, info.State)
			}
			if info.Session == nil || info.Session.Name != s.Name() {
				t.Errorf("pane %q resolved to %v, want session %q", id, info.Session, s.Name())
			}
		})
	}
}

// Why these tests may run in parallel at all: every leg opens its own server —
// a socket name, socket path or directory keyed on an atomic counter — so no
// two tests address the same multiplexer, and nothing they create is visible to
// each other. That isolation is required anyway by §2.9, which exists to keep
// tests off the operator's live servers; parallelism is what it also buys.
//
// The exceptions are the tests that call t.Setenv, which Go forbids alongside
// t.Parallel because the process environment is shared. Those stay sequential.

// §0.8: a backend that cannot honour something a caller asked for MUST say so
// AT THE CALL, not only in a flag's help text.
//
// Two gaps this pins. Every backend whose capabilities say alt-screen is not
// tracked must warn on a capture, not just zmx — the metadata is equally
// meaningless on both, and only one was saying so. And a spawn size that a
// backend drops must be disclosed: measured, a 120x40 request became 80x23 on
// meja with nothing anywhere reporting it.
func TestABackendThatDropsARequestSaysSo(t *testing.T) {
	for _, l := range legs(t) {
		t.Run(l.name, func(t *testing.T) {
			ol := l.open(t)
			caps := ol.Capabilities()
			ctx := context.Background()

			s, err := ol.Create(ctx, fmt.Sprintf("oly-warn%d", counter.Add(1)),
				olympus.In(t.TempDir()), olympus.Size(120, 40))
			if err != nil {
				t.Fatalf("Create: %v", err)
			}
			t.Cleanup(func() { _, _ = s.Stop(context.Background(), olympus.Force()) })

			if !caps.SpawnSizing {
				if len(ol.SizeWarnings()) == 0 {
					t.Errorf("%s cannot size a session at spawn and says nothing about it", l.name)
				}
			} else if len(ol.SizeWarnings()) != 0 {
				t.Errorf("%s sizes at spawn but warned anyway: %v", l.name, ol.SizeWarnings())
			}

			screen, err := s.Screen(ctx)
			if err != nil {
				t.Fatalf("Screen: %v", err)
			}
			if !caps.TracksAltScreen && len(screen.Warnings) == 0 {
				t.Errorf("%s does not track the alternate screen and its capture says nothing, "+
					"so a caller cannot tell an untracked flag from a false one", l.name)
			}
		})
	}
}

// §12.3: a target-addressed operation resolves an absent SERVER into an absent
// SESSION, naming the target. An untargeted listing resolves it into an empty
// list, because "nothing to find here" is the honest answer to "what is there".
//
// The two halves must not be confused, and one backend confused them: asked for
// one named session's panes on a server that was not running, tmux answered
// ok:true with an empty list while meja and zmx answered not-found. A caller
// checking `if olympus panes X` was told that session has no panes rather than
// that it does not exist, and took the success branch.
func TestAColdServerIsEmptyUntargetedAndNotFoundTargeted(t *testing.T) {
	for _, l := range legs(t) {
		t.Run(l.name, func(t *testing.T) {
			// Deliberately nothing created: the handle points at a scope whose
			// server has never been started.
			ol := l.open(t)
			ctx := context.Background()

			all, err := ol.Panes(ctx, "")
			if err != nil {
				t.Errorf("listing every pane on a cold server errored: %v", err)
			}
			if len(all) != 0 {
				t.Errorf("a cold server reported %d panes", len(all))
			}

			if _, err := ol.Panes(ctx, "ghost"); olympus.CodeOf(err) != backend.CodeSessionNotFound {
				t.Errorf("panes of a named session on a cold server reports %v, want SESSION_NOT_FOUND",
					olympus.CodeOf(err))
			}

			sessions, err := ol.Sessions(ctx)
			if err != nil {
				t.Errorf("listing sessions on a cold server errored: %v", err)
			}
			if len(sessions) != 0 {
				t.Errorf("a cold server reported %d sessions", len(sessions))
			}
		})
	}
}

// §5.6: a follow is a TAP on the session, and an interrupted follow must turn
// it off. Ctrl-C is how a stream is normally ended, so it is the path that
// matters, not an edge case.
//
// Left on, tmux keeps piping that pane's output into a file nobody will ever
// read, growing on the operator's disk for as long as the session lives — and
// the operator has no way to know, because the command they interrupted is
// gone. Measured before this: pane_pipe stayed 1 after SIGINT.
func TestAnInterruptedWatchTurnsItsTapOff(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is not installed")
	}
	binary := buildOlympus(t)

	dir, err := os.MkdirTemp(os.TempDir(), "olyw")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	socket := filepath.Join(dir, "s.sock")
	t.Cleanup(func() { _ = exec.Command("tmux", "-S", socket, "kill-server").Run() })

	where := []string{"--backend", "tmux", "--socket-path", socket}
	if out, err := exec.Command(binary, append(where, "start", "tap")...).CombinedOutput(); err != nil {
		t.Fatalf("start: %v\n%s", err, out)
	}

	piped := func() string {
		out, _ := exec.Command("tmux", "-S", socket, "list-panes", "-t", "=tap", "-F", "#{pane_pipe}").Output()
		return strings.TrimSpace(string(out))
	}

	watch := exec.Command(binary, append(where, "watch", "tap")...)
	if err := watch.Start(); err != nil {
		t.Fatalf("starting watch: %v", err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for piped() != "1" {
		if time.Now().After(deadline) {
			_ = watch.Process.Kill()
			t.Fatal("the tap never came on, so there is nothing to test turning off")
		}
		time.Sleep(50 * time.Millisecond)
	}

	if err := watch.Process.Signal(os.Interrupt); err != nil {
		t.Fatalf("interrupting watch: %v", err)
	}
	done := make(chan error, 1)
	go func() { done <- watch.Wait() }()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		_ = watch.Process.Kill()
		t.Fatal("watch did not exit on SIGINT")
	}

	deadline = time.Now().Add(5 * time.Second)
	for piped() == "1" {
		if time.Now().After(deadline) {
			t.Fatal("the tap is still on after the watch was interrupted")
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// api §2: an empty collection serializes as [], never null. A consumer that has
// to handle both shapes for one field is being asked to write two parsers.
func TestEmptyCollectionsAreNeverNull(t *testing.T) {
	t.Parallel()
	// A diagnosis with every backend installed carries no install hints, which
	// is exactly the case that produced null.
	raw, err := json.Marshal(olympus.Diagnose(context.Background()))
	if err != nil {
		t.Fatalf("marshalling a diagnosis: %v", err)
	}
	var round map[string]any
	if err := json.Unmarshal(raw, &round); err != nil {
		t.Fatalf("unmarshalling it back: %v", err)
	}
	for _, field := range []string{"install_hints", "backends"} {
		if round[field] == nil {
			t.Errorf("%s serialized as null rather than []: %s", field, raw)
		}
	}
}

// The tunables were all correct and none of them was guarded: twelve option
// constructors that no test referenced, so a wrong field assignment would ship
// silently and only show up as a budget that does nothing or a colour flag that
// never arrives.
//
// Asserted through observable EFFECT, not by reading the config back. Checking
// that WithColors sets a Colors field only proves the assignment; checking that
// escape bytes arrive proves the option reaches the backend.
func TestTheTunablesReachTheBackend(t *testing.T) {
	t.Parallel()
	for _, l := range legs(t) {
		t.Run(l.name, func(t *testing.T) {
			ol := l.open(t)
			caps := ol.Capabilities()
			ctx := context.Background()
			s := session(t, ol)

			// WithColors: the default capture strips escapes, the opt-in keeps
			// them. Both directions, because an option that is always on looks
			// identical to one that works.
			if err := s.Send(ctx, `printf '\033[31mRED\033[0m\n'`); err != nil {
				t.Fatalf("Send: %v", err)
			}
			if _, err := s.WaitFor(ctx, "RED", olympus.WaitTimeout(15*time.Second)); err != nil {
				t.Fatalf("the coloured line never appeared: %v", err)
			}
			plain, err := s.Screen(ctx)
			if err != nil {
				t.Fatalf("Screen: %v", err)
			}
			if strings.Contains(plain.Text, "\x1b[") {
				t.Errorf("the default capture kept escape sequences")
			}
			coloured, err := s.Screen(ctx, olympus.WithColors())
			if err != nil {
				t.Fatalf("Screen with colours: %v", err)
			}
			if !strings.Contains(coloured.Text, "\x1b[") {
				t.Errorf("WithColors did not reach the backend: no escape sequences in the capture")
			}

			// VerifyBudget: a budget too small to observe anything must fail as
			// a TIMEOUT rather than hang or silently succeed.
			err = s.Send(ctx, "echo budget", olympus.VerifyBudget(time.Millisecond))
			if err != nil && olympus.CodeOf(err) != backend.CodeTimeout {
				t.Errorf("an impossible verify budget failed as %v, want TIMEOUT", olympus.CodeOf(err))
			}
			// That probe leaves whatever it managed to type on the input line,
			// and a verified send resends once — so the line can hold the text
			// twice over. Clearing it is not tidiness: the next assertion counts
			// occurrences of ITS marker, and a dirty line makes that count a
			// measurement of the previous step.
			if err := s.Press(ctx, backend.KeyCtrlC); err != nil {
				t.Fatalf("clearing the line after the budget probe: %v", err)
			}

			// WithoutSubmit: the text lands and is NOT executed.
			marker := "unsubmitted-marker"
			if err := s.Send(ctx, "echo "+marker, olympus.WithoutSubmit()); err != nil {
				t.Fatalf("Send without submit: %v", err)
			}
			screen, err := s.Screen(ctx)
			if err != nil {
				t.Fatalf("Screen: %v", err)
			}
			// The command is on the line; its OUTPUT would be a second
			// occurrence on its own line, which is what submitting would add.
			if strings.Count(screen.Text, marker) > 1 {
				t.Errorf("WithoutSubmit submitted anyway:\n%s", screen.Text)
			}
			if err := s.Press(ctx, backend.KeyCtrlC); err != nil {
				t.Fatalf("clearing the unsubmitted line: %v", err)
			}

			// KeepCorpse, where a backend has corpses at all.
			if caps.RemainOnExit {
				name := fmt.Sprintf("oly-corpse%d", counter.Add(1))
				corpse, err := ol.Create(ctx, name, olympus.In(t.TempDir()),
					olympus.Command("sh", "-c", "exit 0"), olympus.KeepCorpse())
				if err != nil {
					t.Fatalf("creating a session with a corpse: %v", err)
				}
				t.Cleanup(func() { _, _ = corpse.Stop(context.Background(), olympus.Force()) })
				deadline := time.Now().Add(10 * time.Second)
				for {
					info, err := ol.Info(ctx, name)
					if err == nil && info.State == backend.StatePresent {
						break
					}
					if time.Now().After(deadline) {
						t.Errorf("KeepCorpse did not keep the session after its command exited")
						break
					}
					time.Sleep(50 * time.Millisecond)
				}
			}

			// The timing tunables have no observable effect beyond being
			// honoured, so the assertion is that they are accepted and change
			// nothing else — a wrong field would surface as an error here.
			if _, err := s.Exec(ctx, "echo tuned",
				olympus.RunTimeout(30*time.Second), olympus.RunInterval(50*time.Millisecond)); err != nil {
				t.Errorf("the run tunables broke a run: %v", err)
			}
			if _, err := s.WaitFor(ctx, "tuned",
				olympus.WaitTimeout(10*time.Second), olympus.WaitInterval(20*time.Millisecond)); err != nil {
				t.Errorf("the wait tunables broke a wait: %v", err)
			}
		})
	}
}

// §14: an exit marker is read off the SCREEN, so everything that can be on a
// screen is an input to it — including text that looks like a marker but is
// not, and an older marker from a previous command.
//
// The failure mode is silent and wrong rather than loud: a stale marker makes a
// finished command report the previous one's status, and a caller acting on an
// exit code has no way to tell.
func TestExitMarkersReadTheRightOne(t *testing.T) {
	t.Parallel()
	for _, l := range legs(t) {
		t.Run(l.name, func(t *testing.T) {
			s := session(t, l.open(t))
			ctx := context.Background()
			// The separator is part of the MARKER, not something Olympus
			// skips — §14 says it has no opinion on the format, and skipping a
			// colon would be one. Written the documented way round here so this
			// test fails if the documentation ever drifts back.
			const marker = "OLYDONE:"

			// Nothing has run: not found, and NOT a zero exit code, which a
			// caller would otherwise read as success.
			if _, found, err := s.ExitStatus(ctx, marker, 200); err != nil {
				t.Fatalf("ExitStatus before anything ran: %v", err)
			} else if found {
				t.Error("a marker was found before any command wrote one")
			}

			if err := s.Send(ctx, "sh -c 'exit 3'; echo "+marker+"$?"); err != nil {
				t.Fatalf("Send: %v", err)
			}
			if _, err := s.WaitFor(ctx, marker+"3", olympus.WaitTimeout(15*time.Second)); err != nil {
				t.Fatalf("the first marker never appeared: %v", err)
			}
			code, found, err := s.ExitStatus(ctx, marker, 200)
			if err != nil || !found || code != 3 {
				t.Fatalf("first marker: code=%d found=%v err=%v, want 3/true/nil", code, found, err)
			}

			// A second command writes a second marker. The LATEST must win —
			// reading the older one reports a status for a command that already
			// finished, which is the whole hazard.
			if err := s.Send(ctx, "sh -c 'exit 7'; echo "+marker+"$?"); err != nil {
				t.Fatalf("Send: %v", err)
			}
			if _, err := s.WaitFor(ctx, marker+"7", olympus.WaitTimeout(15*time.Second)); err != nil {
				t.Fatalf("the second marker never appeared: %v", err)
			}
			code, found, err = s.ExitStatus(ctx, marker, 200)
			if err != nil || !found {
				t.Fatalf("second marker: found=%v err=%v", found, err)
			}
			if code != 7 {
				t.Errorf("exit status %d, want 7 — a stale marker won, so a finished command reports "+
					"the previous command's status", code)
			}
		})
	}
}

// A capture must survive output that is large and shaped awkwardly. These are
// the inputs a caller cannot control: a build log is thousands of lines, and a
// single line of JSON is longer than any terminal is wide.
func TestCaptureSurvivesLargeAndAwkwardOutput(t *testing.T) {
	t.Parallel()
	for _, l := range legs(t) {
		t.Run(l.name, func(t *testing.T) {
			ol := l.open(t)
			s := session(t, ol)
			ctx := context.Background()

			if err := s.Send(ctx, "for i in $(seq 1 400); do echo line-$i; done"); err != nil {
				t.Fatalf("Send: %v", err)
			}
			if _, err := s.WaitFor(ctx, "line-400", olympus.WaitTimeout(30*time.Second)); err != nil {
				t.Fatalf("the bulk output never finished: %v", err)
			}
			deep, err := s.Screen(ctx, olympus.WithHistory(1000))
			if err != nil {
				t.Fatalf("capturing history: %v", err)
			}
			// The tail is on the visible screen; the head is only reachable
			// through scrollback, which is what asking for history means.
			if !strings.Contains(deep.Text, "line-400") {
				t.Errorf("the newest line is missing from a history capture")
			}
			if ol.Capabilities().NativeScrollback || len(deep.Text) > len(mustScreen(t, s).Text) {
				// Either the backend returns full scrollback natively, or the
				// history capture must be strictly larger than the viewport.
			} else {
				t.Errorf("a history capture is no larger than the visible screen, so scrollback was not read")
			}

			// A line far wider than the terminal. The bytes must survive; how
			// the backend wraps them is its own business (§5.1).
			long := strings.Repeat("x", 500)
			if err := s.Send(ctx, "echo start-"+long+"-end"); err != nil {
				t.Fatalf("Send a long line: %v", err)
			}
			if _, err := s.WaitFor(ctx, "-end", olympus.WaitTimeout(20*time.Second)); err != nil {
				t.Fatalf("the long line never appeared: %v", err)
			}
			screen, err := s.Screen(ctx)
			if err != nil {
				t.Fatalf("Screen: %v", err)
			}
			flat := strings.ReplaceAll(screen.Text, "\n", "")
			if !strings.Contains(flat, strings.Repeat("x", 400)) {
				t.Errorf("a 500-character line did not survive capture intact")
			}
		})
	}
}

func mustScreen(t *testing.T, s *olympus.Session) olympus.Screen {
	t.Helper()
	screen, err := s.Screen(context.Background())
	if err != nil {
		t.Fatalf("Screen: %v", err)
	}
	return screen
}

// §9.2: a view is a real, separately-killable session. Killing the base leaves
// the view alive, and sweeping views is the caller's responsibility.
//
// Both halves matter and only one is obvious. That a view survives its base is
// what makes it a session rather than a window onto one; that Olympus does NOT
// sweep it is what a caller has to know, because an unswept view keeps the
// underlying window alive and a caller who believed `stop` was enough leaves
// one behind on every run.
func TestAViewOutlivesItsBase(t *testing.T) {
	t.Parallel()
	for _, l := range legs(t) {
		t.Run(l.name, func(t *testing.T) {
			ol := l.open(t)
			ctx := context.Background()

			base := session(t, ol)
			view, err := ol.CreateView(ctx, base.Name())
			if !ol.Capabilities().Views {
				if backend.CodeOf(err) != backend.CodeUnsupported {
					t.Errorf("a backend without views reports %v, want UNSUPPORTED", backend.CodeOf(err))
				}
				return
			}
			if err != nil {
				t.Fatalf("CreateView: %v", err)
			}
			t.Cleanup(func() {
				if s, err := ol.Open(context.Background(), view.Name); err == nil {
					_, _ = s.Stop(context.Background(), olympus.Force())
				}
			})
			if view.Base != base.Name() {
				t.Errorf("the view names base %q, want %q", view.Base, base.Name())
			}

			listed, err := ol.Views(ctx, base.Name())
			if err != nil {
				t.Fatalf("Views: %v", err)
			}
			if len(listed) != 1 || listed[0].Name != view.Name {
				t.Errorf("the view is not listed against its base: %+v", listed)
			}

			// Kill the BASE. The view is a session in its own right.
			if _, err := base.Stop(ctx, olympus.Force()); err != nil {
				t.Fatalf("stopping the base: %v", err)
			}
			deadline := time.Now().Add(10 * time.Second)
			for {
				info, err := ol.Info(ctx, base.Name())
				if err == nil && info.State == backend.StateAbsent {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("the base never went away")
				}
				time.Sleep(50 * time.Millisecond)
			}

			survived, err := ol.Info(ctx, view.Name)
			if err != nil {
				t.Fatalf("Info on the view: %v", err)
			}
			if survived.State != backend.StatePresent {
				t.Errorf("the view is %q after its base was killed, want present — a view is a "+
					"separately-killable session, and sweeping it is the caller's job", survived.State)
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

// §17.3: one place decides the tunable values, and these are the values it
// decides. A door that invents its own has created a second contract, so the
// numbers are transcribed here from the spec's table by hand rather than read
// back off the constants — reading them off the code would only assert the code
// agrees with itself.
//
// Two of these are per-attempt rather than total, which is the distinction the
// table records and the reason they are listed with that word attached: the
// verified-send budget is spent twice (§7.4).
func TestTheShippedDefaultsAreTheOnesTheSpecPublishes(t *testing.T) {
	t.Parallel()

	for _, c := range []struct {
		rule string
		got  any
		want any
	}{
		{"attach initial size (§8)", [2]int{olympus.DefaultCols, olympus.DefaultRows}, [2]int{80, 24}},
		{"run timeout", olympus.DefaultRunTimeout, 60 * time.Second},
		{"run poll interval", olympus.DefaultRunPoll, 250 * time.Millisecond},
		{"screen-wait timeout (§5)", olympus.DefaultWaitTimeout, 30 * time.Second},
		{"screen-wait interval (§5)", olympus.DefaultWaitPoll, 250 * time.Millisecond},
		{"verified-send per-attempt budget (§7.4)", olympus.DefaultVerifyBudget, 5 * time.Second},
		{"verified-send poll (§7.4)", olympus.DefaultVerifyPoll, 100 * time.Millisecond},
		{"write-lock wait (§11.1)", olympus.DefaultLockWait, 10 * time.Second},
	} {
		if c.got != c.want {
			t.Errorf("%s is %v, want the published %v", c.rule, c.got, c.want)
		}
	}

	// The write-lock wait is one of the env-overridable values, and §17.3 says
	// those MUST be read at call time rather than cached at process start — an
	// operator raising it should not have to restart anything. Naming the
	// variable is what makes that reachable; a name only this package knew
	// could not be set from outside it.
	if olympus.LockWaitEnv != "OLYMPUS_LOCK_WAIT" {
		t.Errorf("the write-lock override is named %q, want OLYMPUS_LOCK_WAIT", olympus.LockWaitEnv)
	}
}
