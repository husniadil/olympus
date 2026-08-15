package olympus

import (
	"context"
	"regexp"
	"time"

	"github.com/husniadil/olympus/backend"
	"github.com/husniadil/olympus/internal/engine"
)

// A Session is a handle to one terminal session.
type Session struct {
	ol   *Olympus
	name string
	row  backend.Session
}

// A SessionOption configures Session.
type SessionOption func(*backend.CreateSpec)

// In sets the session's working directory.
func In(dir string) SessionOption {
	return func(s *backend.CreateSpec) { s.Dir = dir }
}

// Size sets the session's initial size. It is ignored by backends with no
// spawn-time sizing concept, which is documented rather than papered over
// (behavior §2.1).
func Size(cols, rows int) SessionOption {
	return func(s *backend.CreateSpec) { s.Cols, s.Rows = cols, rows }
}

// Command spawns the session directly onto an argv rather than a login shell.
// It is executed, never typed (behavior §2.3).
func Command(argv ...string) SessionOption {
	return func(s *backend.CreateSpec) { s.Command = argv }
}

// KeepCorpse leaves a dead session to inspect after its command exits. Backends
// with no corpse concept reject it as unsupported, before doing anything.
func KeepCorpse() SessionOption {
	return func(s *backend.CreateSpec) { s.RemainOnExit = true }
}

// Session makes a named session exist and be alive, and returns a handle.
//
// This is ensure-semantics, matching the `start` verb: create, reuse, or
// replace-if-dead. There is deliberately no separate create-versus-open
// decision for a caller to get wrong.
func (o *Olympus) Session(ctx context.Context, name string, opts ...SessionOption) (*Session, error) {
	spec := backend.CreateSpec{Name: name, Cols: DefaultCols, Rows: DefaultRows}
	for _, opt := range opts {
		opt(&spec)
	}

	var row backend.Session
	// The whole check-then-create decision is one critical section. That is
	// what turns two concurrent ensures of one name into a deterministic
	// outcome instead of a race (behavior §2.6, §11.1).
	err := engine.WithLock(ctx, o.locks, o.lockKey(name), o.lockWait, func() error {
		created, err := engine.Ensure(ctx, o.backend, spec)
		row = created
		return err
	})
	if err != nil {
		return nil, err
	}
	return &Session{ol: o, name: name, row: row}, nil
}

// Attach returns a handle to an existing session without creating anything.
//
// Unlike Session it does not ensure: it is for a caller that already knows the
// session exists and does not want to bring one into being by asking about it.
func (o *Olympus) Open(ctx context.Context, target string) (*Session, error) {
	resolved, err := o.resolveTarget(ctx, target)
	if err != nil {
		return nil, err
	}
	if state := o.backend.Probe(ctx, resolved); state != backend.StatePresent {
		if state == backend.StateAbsent {
			return nil, backend.Errorf(backend.CodeSessionNotFound, "no session %s", target)
		}
		return nil, backend.Errorf(backend.CodeBackendUnavailable, "cannot reach the backend to address %s", target)
	}
	return &Session{ol: o, name: resolved}, nil
}

// Name reports the session's name.
func (s *Session) Name() string { return s.name }

// Outcome reports what Session did: created, reused, or reaped. It is empty for
// a handle that did not create anything.
func (s *Session) Outcome() backend.Outcome { return s.row.Outcome }

// Row reports the session's listing row as of the handle's creation.
func (s *Session) Row() backend.Session { return s.row }

func (s *Session) key() engine.LockKey { return s.ol.lockKey(s.name) }

// Type places literal text in the input line WITHOUT submitting it.
//
// Placing text and submitting it are separate operations on purpose: it keeps
// injection symmetric across backends and composable, and it means the retry
// discipline for a failed terminator belongs to whoever issues it (behavior
// §4.3).
func (s *Session) Type(ctx context.Context, text string) error {
	return engine.WithLock(ctx, s.ol.locks, s.key(), s.ol.lockWait, func() error {
		return s.ol.backend.Type(ctx, s.name, text)
	})
}

// Press sends named keys.
func (s *Session) Press(ctx context.Context, keys ...backend.Key) error {
	return engine.WithLock(ctx, s.ol.locks, s.key(), s.ol.lockWait, func() error {
		return s.ol.backend.Press(ctx, s.name, keys...)
	})
}

// Paste places multi-line text in the input line without submitting the final
// line.
//
// Whether INTERMEDIATE lines execute is consumer-dependent and genuinely
// differs between backends (behavior §4.6). The cross-backend guarantee is only
// about the last line.
func (s *Session) Paste(ctx context.Context, text string) error {
	return engine.WithLock(ctx, s.ol.locks, s.key(), s.ol.lockWait, func() error {
		return s.ol.backend.Paste(ctx, s.name, text)
	})
}

// Submit sends the terminator alone.
func (s *Session) Submit(ctx context.Context) error {
	return engine.WithLock(ctx, s.ol.locks, s.key(), s.ol.lockWait, func() error {
		return s.ol.backend.Submit(ctx, s.name)
	})
}

// Send delivers text, waits until it is observed on screen, and only then
// submits it — holding the lock across all three (behavior §11.2).
func (s *Session) Send(ctx context.Context, text string) error {
	return engine.Delivery{
		Backend:  s.ol.backend,
		Locks:    s.ol.locks,
		Key:      s.key(),
		LockWait: s.ol.lockWait,
		Budget:   DefaultVerifyBudget,
		Poll:     DefaultVerifyPoll,
	}.VerifiedSubmit(ctx, s.name, text)
}

// SendAtomic delivers text and submits it as one caller-visible unit.
//
// It does NOT verify: atomicity trades away the on-screen check. A caller
// needing both properties does not get them from one call — verify-then-submit
// cannot be atomic, because any cross-invocation retry re-types the text before
// checking and doubles it (behavior §4.7).
func (s *Session) SendAtomic(ctx context.Context, text string) error {
	// Single-line only: multi-line text has no unambiguous submit point. The
	// door validates; the backend does not re-check.
	if err := engine.ValidateCommand(text); err != nil {
		return err
	}
	return engine.WithLock(ctx, s.ol.locks, s.key(), s.ol.lockWait, func() error {
		return s.ol.backend.SendAtomic(ctx, s.name, text)
	})
}

// A Screen is a capture and what the text itself could not carry.
type Screen struct {
	Text     string             `json:"text"`
	Meta     backend.ScreenMeta `json:"meta"`
	Warnings []Warning          `json:"-"`
}

// A ScreenOption configures Screen.
type ScreenOption func(*backend.ScreenOpts)

// WithColors keeps ANSI escapes in the captured text.
func WithColors() ScreenOption {
	return func(o *backend.ScreenOpts) { o.Colors = true }
}

// WithHistory asks for scrollback above the visible screen.
func WithHistory(lines int) ScreenOption {
	return func(o *backend.ScreenOpts) { o.HistoryLines = lines }
}

// Screen reads the session's screen.
//
// Reads never take the write lock. A read that blocks on a writer turns
// observation into contention, which is backwards: observing a busy session is
// the case that matters most (behavior §11.1).
func (s *Session) Screen(ctx context.Context, opts ...ScreenOption) (Screen, error) {
	var options backend.ScreenOpts
	for _, opt := range opts {
		opt(&options)
	}

	capture, err := s.ol.backend.Screen(ctx, s.name, options)
	if err != nil {
		return Screen{}, err
	}

	screen := Screen{Text: capture.Text, Meta: capture.Meta}
	screen.Warnings = append(screen.Warnings, warn(s.ol.resolution.Backend, opCapture)...)
	screen.Warnings = append(screen.Warnings, warn(s.ol.resolution.Backend, opCaptureMeta)...)
	if options.HistoryLines > 0 {
		screen.Warnings = append(screen.Warnings, warn(s.ol.resolution.Backend, opCaptureHistory)...)
	}
	return screen, nil
}

// A WaitOption configures WaitFor.
type WaitOption func(*waitConfig)

type waitConfig struct {
	timeout time.Duration
	poll    time.Duration
}

// WaitTimeout bounds how long to wait for a pattern.
func WaitTimeout(d time.Duration) WaitOption {
	return func(c *waitConfig) { c.timeout = d }
}

// WaitFor blocks until the session's screen matches a regular expression, and
// returns the screen that matched.
func (s *Session) WaitFor(ctx context.Context, pattern string, opts ...WaitOption) (Screen, error) {
	expression, err := regexp.Compile(pattern)
	if err != nil {
		// A bad pattern is input the caller could have validated.
		return Screen{}, backend.Wrapf(backend.CodeUsage, err, "the pattern %q is not a valid regular expression", pattern)
	}

	cfg := waitConfig{timeout: DefaultWaitTimeout, poll: DefaultWaitPoll}
	for _, opt := range opts {
		opt(&cfg)
	}

	deadline := time.Now().Add(cfg.timeout)
	var last Screen
	for {
		screen, err := s.Screen(ctx)
		if err == nil {
			last = screen
			if expression.MatchString(screen.Text) {
				return screen, nil
			}
		}
		if time.Now().After(deadline) {
			return last, backend.Errorf(backend.CodeTimeout,
				"the pattern %q did not appear on %s within %s", pattern, s.name, cfg.timeout)
		}
		select {
		case <-ctx.Done():
			return last, backend.Wrapf(backend.CodeTimeout, ctx.Err(), "waiting on %s", s.name)
		case <-time.After(cfg.poll):
		}
	}
}

// A Result is a completed command.
type Result struct {
	ExitCode int    `json:"exit_code"`
	Output   string `json:"output"`
}

// Exec runs a command and waits for it to finish.
//
// A non-zero exit code is a RESULT, not an error: whether the protocol worked
// and what the command's own status was are two independent outcomes, and
// conflating them makes an ordinary failing command look like Olympus broke
// (behavior §12.1).
func (s *Session) Exec(ctx context.Context, command string) (Result, error) {
	result, err := s.runner().Exec(ctx, s.name, command)
	if err != nil {
		return Result{}, err
	}
	return Result{ExitCode: result.ExitCode, Output: result.Output}, nil
}

func (s *Session) runner() engine.Runner {
	return engine.Runner{
		Backend:  s.ol.backend,
		Locks:    s.ol.locks,
		Key:      s.key(),
		LockWait: s.ol.lockWait,
		Timeout:  DefaultRunTimeout,
		Poll:     DefaultRunPoll,
	}
}

// A Job is a detached run.
//
// It holds no state of its own beyond the pair that identifies it. Nothing
// durable is written: the id is baked into the sentinel markers, and polling
// re-scans the scrollback for them. The scrollback IS the state (behavior
// §6.7).
type Job struct {
	session *Session
	id      string
}

// ID reports the run identifier. A caller resumes solely by re-presenting it
// with the target.
func (j *Job) ID() string { return j.id }

// Start injects a command and returns without waiting.
func (s *Session) Start(ctx context.Context, command string) (*Job, error) {
	id, err := s.runner().Start(ctx, s.name, command)
	if err != nil {
		return nil, err
	}
	return &Job{session: s, id: id}, nil
}

// A PollResult is one poll of a detached run.
type PollResult struct {
	Status backend.Liveness `json:"-"`
	// State is pending, completed, or died.
	State string `json:"status"`
	// ExitCode is populated ONLY when completed, never a fake zero a naive
	// consumer could read as success. Branch on State first (behavior §6.7).
	ExitCode *int      `json:"exit_code,omitempty"`
	Output   string    `json:"output,omitempty"`
	Reason   string    `json:"reason,omitempty"`
	Warnings []Warning `json:"-"`
}

// Poll reports whether a detached run has finished.
//
// It never takes the write lock, and it answers about the COMMAND rather than
// about the backend: a target that never existed and one that vanished are
// indistinguishable from a read-only vantage point, so both answer died
// (behavior §6.8).
func (j *Job) Poll(ctx context.Context) (PollResult, error) {
	return j.session.Poll(ctx, j.id)
}

// Poll reports on a detached run by id, for a caller resuming from nothing but
// the pair.
func (s *Session) Poll(ctx context.Context, id string) (PollResult, error) {
	got, err := s.runner().PollRun(ctx, s.name, id)
	if err != nil {
		return PollResult{}, err
	}
	return PollResult{
		State:    string(got.Status),
		ExitCode: got.ExitCode,
		Output:   got.Output,
		Reason:   got.Reason,
		Warnings: warn(s.ol.resolution.Backend, opPollWindow),
	}, nil
}

// ExitStatus reads a caller-supplied completion marker off the screen.
//
// The marker is always caller-supplied and there is deliberately no default: a
// fixed one would collide with ordinary output or stale scrollback, and weaken
// the caller-controlled uniqueness the design assumes (behavior §14).
func (s *Session) ExitStatus(ctx context.Context, marker string) (int, bool, error) {
	screen, err := s.Screen(ctx, WithHistory(engine.DetachedWindow))
	if err != nil {
		return 0, false, err
	}
	return engine.ExitMarker(screen.Text, marker)
}

// A Stopped reports how a session ended.
type Stopped struct {
	// Outcome is gone, graceful, or killed. All three are successes.
	Outcome  string    `json:"outcome"`
	Warnings []Warning `json:"-"`
}

// A StopOption configures Stop.
type StopOption func(*engine.KillPolicy)

// Force skips the graceful attempt entirely.
func Force() StopOption {
	return func(p *engine.KillPolicy) { p.Presses = 0; p.Timeout = 0 }
}

// Stop ends a session, trying to interrupt it before forcing.
func (s *Session) Stop(ctx context.Context, opts ...StopOption) (Stopped, error) {
	policy := engine.DefaultKillPolicy
	for _, opt := range opts {
		opt(&policy)
	}

	ops := engine.KillOps{
		Interrupt: func(ctx context.Context) error { return s.ol.backend.Interrupt(ctx, s.name) },
		Probe:     func(ctx context.Context) backend.State { return s.ol.backend.Probe(ctx, s.name) },
		Kill:      func(ctx context.Context) error { return s.ol.backend.Kill(ctx, s.name) },
		Sleep: func(ctx context.Context, d time.Duration) error {
			select {
			case <-ctx.Done():
				return backend.Wrapf(backend.CodeTimeout, ctx.Err(), "stopping %s", s.name)
			case <-time.After(d):
				return nil
			}
		},
	}

	outcome, err := engine.GracefulKill(ctx, ops, policy)
	if err != nil {
		return Stopped{}, err
	}
	return Stopped{
		Outcome:  string(outcome),
		Warnings: warn(s.ol.resolution.Backend, opGracefulKill),
	}, nil
}

// Stop ends a session by name, for a caller that does not hold a handle.
func (o *Olympus) Stop(ctx context.Context, target string, opts ...StopOption) (Stopped, error) {
	resolved, err := o.resolveTarget(ctx, target)
	if err != nil {
		return Stopped{}, err
	}
	return (&Session{ol: o, name: resolved}).Stop(ctx, opts...)
}
