package backendtest

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync/atomic"
	"time"

	"github.com/husniadil/olympus/backend"
)

// A Reporter is the subset of testing.TB the suite uses. It is an interface
// rather than *testing.T so the suite can be run against a recorder and
// verified to fail for the right reasons — a suite nobody has watched fail is
// a suite nobody has tested.
type Reporter interface {
	Helper()
	Errorf(format string, args ...any)
	Fatalf(format string, args ...any)
	Cleanup(func())
	TempDir() string
}

// An Env is one case's working environment.
type Env struct {
	// T reports failures.
	T Reporter
	// Backend is the isolated backend under test.
	Backend backend.Backend
	// Expect holds the declared per-backend outcomes.
	Expect Expectations

	budgets Budgets
	ctx     context.Context
}

// Ctx is the context for backend calls.
func (e *Env) Ctx() context.Context { return e.ctx }

// nameCounter keeps generated session names unique within a process.
var nameCounter atomic.Int64

// Name returns a fresh session name. Names are kept short deliberately: a
// session name has a socket-path budget on some backends (behavior §2.5), and
// a suite that generates long names would fail there for a reason that has
// nothing to do with the rule under test. The prefix is outside the reserved
// namespace of §17.1, so a stray test session can never be mistaken for one
// Olympus created for a caller.
func (e *Env) Name() string {
	return fmt.Sprintf("oly-t%d-%d", os.Getpid(), nameCounter.Add(1))
}

// StartShell creates a session running a plain shell and arranges for it to be
// killed when the case ends.
func (e *Env) StartShell() string {
	e.T.Helper()
	return e.start(nil)
}

// StartCommand creates a session spawned directly onto an argv. This is the
// second session shape the spec distinguishes (behavior §2.8.1), not a variant
// of the first.
//
// It is only callable where Capabilities.SpawnCommand is true. Cases that need
// a PROGRAM on the screen rather than the exec-versus-typed distinction itself
// use StartProgram, which reaches the same shape on a backend that cannot spawn
// one.
func (e *Env) StartCommand(argv ...string) string {
	e.T.Helper()
	return e.start(argv)
}

// StartProgram creates a session whose process is argv rather than a shell, by
// whichever mechanism the backend has.
//
// Where a backend can spawn an argv this is StartCommand. Where it cannot
// (behavior §2.3, Capabilities.SpawnCommand false) it starts a shell and hands
// the shell over to the program with `exec`, which reaches the same end state —
// the session's process IS the program — at the cost of the command line being
// echoed into the session's own output first. That echo is exactly what §2.3
// rules out for a spawn, which is why the two are separate helpers and why the
// case about the distinction calls Create itself.
func (e *Env) StartProgram(argv ...string) string {
	e.T.Helper()
	if e.Backend.Capabilities().SpawnCommand {
		return e.start(argv)
	}

	target := e.StartShell()
	e.Warm(target)
	if err := e.Backend.SendAtomic(e.ctx, target, "exec "+shellCommand(argv)); err != nil {
		e.T.Fatalf("handing session %s over to %v: %v", target, argv, err)
	}
	return target
}

// shellCommand quotes an argv into one POSIX shell command line.
//
// Single quotes rather than any escaping scheme: inside them every byte but the
// quote itself is literal, so the payloads these cases use — escape sequences,
// backslashes, semicolons, `$?` — survive without a per-character rule to get
// wrong.
func shellCommand(argv []string) string {
	quoted := make([]string, 0, len(argv))
	for _, arg := range argv {
		quoted = append(quoted, "'"+strings.ReplaceAll(arg, "'", `'\''`)+"'")
	}
	return strings.Join(quoted, " ")
}

func (e *Env) start(argv []string) string {
	e.T.Helper()
	name := e.Name()
	spec := backend.CreateSpec{
		Name:    name,
		Dir:     e.T.TempDir(),
		Command: argv,
		Cols:    80,
		Rows:    24,
	}
	if _, err := e.Backend.Create(e.ctx, spec); err != nil {
		e.T.Fatalf("creating session %s: %v", name, err)
	}
	e.T.Cleanup(func() {
		// Best effort: a case that already killed its session must not fail in
		// cleanup for having succeeded.
		_ = e.Backend.Kill(context.Background(), name)
	})
	return name
}

// Warm blocks until the session's shell has provably executed a command
// (behavior §16).
//
// The probe is expansion-based on purpose. PTY echo paints typed bytes onto the
// screen, so a literal string appearing there proves only that it was typed —
// the shell may not be reading input yet. Only substituted output proves
// execution. Without this, a one-shot send lands before the shell is ready, the
// deadline expires, and the flake rotates between cases rather than reproducing
// in one.
//
// The probe is delivered with SendAtomic rather than composed out of Type and
// Submit. No production caller composes those two: every inject-then-submit
// path above this interface goes through one door verb that owns its terminator
// and retries it (§4.4), so a harness composing them itself would prove a path
// nothing ships. Atomic delivery is the shape this loop needs anyway — it
// retries the WHOLE probe until the expansion appears, and §4.7 is precisely
// the guarantee that a retried invocation cannot leave a typed-but-unsubmitted
// line behind for the next attempt to concatenate onto.
func (e *Env) Warm(target string) {
	e.T.Helper()
	const probe = `printf 'ready-%d\n' 7`
	const want = "ready-7"

	deadline := time.Now().Add(e.budgets.Warm)
	for time.Now().Before(deadline) {
		if err := e.Backend.SendAtomic(e.ctx, target, probe); err != nil {
			e.T.Fatalf("warming %s: sending the probe: %v", target, err)
		}
		if _, ok := e.screenContains(target, want, e.budgets.Settle); ok {
			return
		}
	}
	e.T.Fatalf("warming %s: the shell never executed a command within the budget", target)
}

// WaitFor blocks until the target's screen contains want, and returns the
// screen that satisfied it.
func (e *Env) WaitFor(target, want string) string {
	e.T.Helper()
	screen, ok := e.screenContains(target, want, e.budgets.Screen)
	if !ok {
		e.T.Fatalf("waiting for %q on %s: never appeared. Screen was:\n%s", want, target, screen)
	}
	return screen
}

// Never asserts that want does not appear within a short settling period. It
// exists for the rules that are about something NOT happening — a literal
// injection that must not submit, for instance — where a single immediate check
// would pass simply by being too early.
func (e *Env) Never(target, want string) {
	e.T.Helper()
	if screen, ok := e.screenContains(target, want, e.budgets.Settle); ok {
		e.T.Errorf("%q appeared on %s but should not have. Screen was:\n%s", want, target, screen)
	}
}

func (e *Env) screenContains(target, want string, budget time.Duration) (string, bool) {
	deadline := time.Now().Add(budget)
	var last string
	for {
		capture, err := e.Backend.Screen(e.ctx, target, backend.ScreenOpts{})
		if err == nil {
			last = capture.Text
			if strings.Contains(last, want) {
				return last, true
			}
		}
		if time.Now().After(deadline) {
			return last, false
		}
		time.Sleep(e.budgets.Poll)
	}
}

// Quiesce waits until the screen stops changing, and returns the settled text.
//
// A live session is a moving target: a shell redrawing its prompt, output still
// arriving, a clock in a status line. Any case that compares two captures has
// to settle first, or it is asserting that nothing happened between them —
// which is not what it means to test, and fails intermittently for a reason
// unrelated to the rule.
func (e *Env) Quiesce(target string) string {
	e.T.Helper()
	deadline := time.Now().Add(e.budgets.Screen)
	previous := ""
	for {
		capture, err := e.Backend.Screen(e.ctx, target, backend.ScreenOpts{})
		if err == nil {
			if capture.Text == previous && capture.Text != "" {
				return capture.Text
			}
			previous = capture.Text
		}
		if time.Now().After(deadline) {
			e.T.Fatalf("the screen of %s never settled", target)
		}
		time.Sleep(e.budgets.Poll)
	}
}

// Screen captures a target or fails the case.
func (e *Env) Screen(target string) backend.Capture {
	e.T.Helper()
	capture, err := e.Backend.Screen(e.ctx, target, backend.ScreenOpts{})
	if err != nil {
		e.T.Fatalf("capturing %s: %v", target, err)
	}
	return capture
}

// SessionNamed returns the listed row for a session, or fails.
func (e *Env) SessionNamed(name string) backend.Session {
	e.T.Helper()
	sessions, err := e.Backend.Sessions(e.ctx)
	if err != nil {
		e.T.Fatalf("listing sessions: %v", err)
	}
	for _, s := range sessions {
		if s.Name == name {
			return s
		}
	}
	e.T.Fatalf("session %s is not in the listing", name)
	return backend.Session{}
}

// PlausibleEpoch reports whether a timestamp is a believable creation time for
// something this test just made.
//
// Plausibility rather than an exact value is deliberate (behavior §16): a wrong
// tmux format variable expands to the empty string with exit 0, so a caller
// that gated its assertion behind "if non-zero" would silently assert nothing
// at all. The window is checked unconditionally instead.
func PlausibleEpoch(ts int64) bool {
	now := time.Now().Unix()
	return ts > now-3600 && ts < now+60
}
