// Package backendtest is the executable definition of a correct backend.
//
// It is exported on purpose: a third-party backend should be able to prove
// itself against the same suite the shipped backends run, rather than against
// prose. Every case names the rule of docs/terminal-behavior.md it enforces, so
// a failure points at a specification section and not only at a line number.
//
// Isolation is not optional. The suite never constructs a backend itself,
// because keeping tests off the operator's live server is backend-specific and
// a hard requirement (behavior §2.9): tmux needs a private -L socket, zmx needs
// a private ZMX_DIR for the backend instance and for every raw verification
// call. Config.New is where a backend proves it has done that.
package backendtest

import (
	"context"
	"testing"
	"time"

	"github.com/husniadil/olympus/backend"
)

// A Config describes the backend under test.
type Config struct {
	// New returns a backend already isolated per behavior §2.9, together with
	// the raw environment a case needs for out-of-band verification. It is
	// called once per case, so a case cannot leak state into the next.
	New func(t Reporter) backend.Backend

	// Expect declares the per-backend, per-session-shape outcomes the spec
	// deliberately allows to differ. It is not a licence to opt out of a rule:
	// only the outcomes named here vary, and each one is required to be
	// declared rather than defaulted, so a backend cannot inherit another
	// backend's answer by silence.
	Expect Expectations

	// Budgets tunes how long the suite waits. Zero fields take the defaults,
	// which suit a real backend on a loaded machine. They exist so a slow CI
	// can raise them, and so the suite itself can be run against an inert
	// backend quickly enough to be worth doing.
	Budgets Budgets
}

// Budgets are the suite's waiting periods.
type Budgets struct {
	// Warm is the total time to spend proving a shell has started executing.
	Warm time.Duration
	// Screen is how long to wait for expected output to appear.
	Screen time.Duration
	// Settle is how long to allow for something to take effect before
	// asserting, and how long to watch for something that must NOT happen.
	Settle time.Duration
	// Poll is the interval between screen captures while waiting.
	Poll time.Duration
}

func (b Budgets) withDefaults() Budgets {
	if b.Warm == 0 {
		b.Warm = 20 * time.Second
	}
	if b.Screen == 0 {
		b.Screen = 10 * time.Second
	}
	if b.Settle == 0 {
		b.Settle = 2 * time.Second
	}
	if b.Poll == 0 {
		b.Poll = 50 * time.Millisecond
	}
	return b
}

// An InterruptOutcome is what interrupting a session's foreground process
// achieves on this backend, for one session shape (behavior §2.8.1).
type InterruptOutcome string

const (
	// InterruptStops means the foreground process actually stops.
	InterruptStops InterruptOutcome = "stops"
	// InterruptIneffective means the mechanism cannot work for this session
	// shape and the caller must fall through to a forced kill. This is a real
	// declared outcome, not a skipped test: a backend claiming it is asserted
	// to survive the interrupt, so a backend that quietly gains the ability
	// fails here and gets to update its declaration.
	InterruptIneffective InterruptOutcome = "ineffective"
)

// Expectations are the declared per-backend answers.
type Expectations struct {
	// InterruptShellBacked is the outcome for a session running a plain shell
	// — the default and common shape.
	InterruptShellBacked InterruptOutcome
	// InterruptExecSpawned is the outcome for a session spawned directly onto
	// an argv. A process that inherits an ignored interrupt disposition can
	// never be interrupted, however the signal is delivered.
	InterruptExecSpawned InterruptOutcome
}

// Run executes the whole suite against a backend.
func Run(t *testing.T, cfg Config) {
	if testing.Short() {
		t.Skip("the conformance suite drives a real multiplexer; run `make test-full` for it")
	}
	t.Helper()
	for _, c := range Cases() {
		t.Run(c.Name, func(t *testing.T) {
			// Cases run concurrently. Each one builds its own backend through
			// cfg.New, which §2.9 already requires to be a server nobody else
			// addresses — so no two cases can see each other's sessions, and the
			// isolation that keeps tests off the operator's servers is the same
			// property that makes this safe.
			//
			// A case cannot break it by reaching for the environment either: it
			// is handed a Reporter rather than a *testing.T, so t.Setenv — the
			// one thing Go forbids alongside parallelism — is not available to
			// it.
			t.Parallel()
			c.run(t, cfg)
		})
	}
}

// A Case is one rule of the specification, executable.
type Case struct {
	// Name identifies the case, opening with the specification section it
	// enforces.
	Name string
	// Fn is the body. It receives an Env already holding an isolated backend.
	Fn func(env *Env)
}

func (c Case) run(t Reporter, cfg Config) {
	t.Helper()
	env := &Env{
		T:       t,
		Backend: cfg.New(t),
		Expect:  cfg.Expect,
		budgets: cfg.Budgets.withDefaults(),
		ctx:     context.Background(),
	}
	c.Fn(env)
}

// Cases returns every case in the suite, in specification order. It is exported
// so a backend can enumerate or filter the suite, and so the suite itself can
// be verified against a deliberately inert backend.
func Cases() []Case {
	var all []Case
	all = append(all, lifecycleCases()...)
	all = append(all, listingCases()...)
	all = append(all, injectionCases()...)
	all = append(all, captureCases()...)
	all = append(all, viewCases()...)
	all = append(all, statusCases()...)
	return all
}
