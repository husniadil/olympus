package olympus

import (
	"context"
	"regexp"
	"strings"
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

// PasteAndSubmit pastes text and then submits the final line.
//
// The two are one critical section, and the terminator is retried once. Once
// text is sitting in the input line, a failed terminator does not merely fail
// visibly — it leaves that text there, where the NEXT injection silently
// concatenates onto it and corrupts both (behavior §4.4).
func (s *Session) PasteAndSubmit(ctx context.Context, text string) error {
	return engine.WithLock(ctx, s.ol.locks, s.key(), s.ol.lockWait, func() error {
		if err := s.ol.backend.Paste(ctx, s.name, text); err != nil {
			return err
		}
		return engine.SubmitOnce(ctx, s.ol.backend, s.name)
	})
}

// TypeAndSubmit places text and then submits it, as one critical section with
// the terminator retried once (behavior §4.4).
//
// It exists because composing Type and Submit at a door is exactly what §4.4
// forbids: two lock acquisitions with a gap between them, and an unretried
// terminator that leaves the text sitting in the input line for the next
// injection to concatenate onto. Type followed by Submit remains available for
// a caller that genuinely wants the two apart; a door offering "type and press
// Enter" as one verb goes through here.
func (s *Session) TypeAndSubmit(ctx context.Context, text string) error {
	return engine.WithLock(ctx, s.ol.locks, s.key(), s.ol.lockWait, func() error {
		if err := s.ol.backend.Type(ctx, s.name, text); err != nil {
			return err
		}
		return engine.SubmitOnce(ctx, s.ol.backend, s.name)
	})
}

// Submit sends the terminator alone.
func (s *Session) Submit(ctx context.Context) error {
	return engine.WithLock(ctx, s.ol.locks, s.key(), s.ol.lockWait, func() error {
		return s.ol.backend.Submit(ctx, s.name)
	})
}

// A SendOption configures a verified send.
type SendOption func(*sendConfig)

type sendConfig struct {
	delivery engine.Delivery
	submit   bool
}

// WithoutSubmit confirms the text landed but leaves it unsubmitted.
//
// Verifying and submitting are independent: a caller may want the input line
// filled and left for a human, or for a terminator whose timing it controls
// itself.
func WithoutSubmit() SendOption {
	return func(c *sendConfig) { c.submit = false }
}

// VerifyBudget sets ONE attempt's window. A verified send spends it twice, so
// the worst case before failing is double this.
func VerifyBudget(d time.Duration) SendOption {
	return func(c *sendConfig) { c.delivery.Budget = d }
}

// Send delivers text, waits until it is observed on screen, and only then
// submits it — holding the lock across all three (behavior §11.2).
func (s *Session) Send(ctx context.Context, text string, opts ...SendOption) error {
	cfg := sendConfig{
		delivery: engine.Delivery{
			Backend:  s.ol.backend,
			Locks:    s.ol.locks,
			Key:      s.key(),
			LockWait: s.ol.lockWait,
			Budget:   DefaultVerifyBudget,
			Poll:     DefaultVerifyPoll,
		},
		submit: true,
	}
	for _, opt := range opts {
		opt(&cfg)
	}
	return cfg.delivery.Verified(ctx, s.name, text, cfg.submit)
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
	Text string             `json:"text"`
	Meta backend.ScreenMeta `json:"meta"`
	// Line is the first line that matched, on a result from WaitFor. A caller
	// waiting on a pattern almost always wants the line rather than the whole
	// screen, and finding it again themselves means re-implementing the match.
	Line     string    `json:"line,omitempty"`
	Matched  bool      `json:"matched,omitempty"`
	Warnings []Warning `json:"-"`
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

	// Through the shared capture, so a single-target read gathers metadata
	// first and drops a history request on the alternate screen exactly as a
	// multi-target one does (behavior §5.3). WaitFor and ExitStatus both read
	// through here, so this is where they inherit the rule too.
	capture, altScreen, err := s.ol.captureOne(ctx, s.name, options)
	if err != nil {
		return Screen{}, err
	}

	screen := Screen{Text: capture.Text, Meta: capture.Meta}
	screen.Warnings = append(screen.Warnings, warn(s.ol.resolution.Backend, opCapture)...)
	screen.Warnings = append(screen.Warnings, warn(s.ol.resolution.Backend, opCaptureMeta)...)
	if options.HistoryLines > 0 {
		screen.Warnings = append(screen.Warnings, warn(s.ol.resolution.Backend, opCaptureHistory)...)
		screen.Warnings = append(screen.Warnings, warnDepth(s.ol.resolution.Backend, options.HistoryLines)...)
		if altScreen {
			screen.Warnings = append(screen.Warnings, altScreenHistoryDropped)
		}
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

// WaitInterval sets how often the screen is re-read while waiting. A shorter
// interval catches output that is overwritten quickly; a longer one costs the
// backend less on a session nobody is in a hurry about.
func WaitInterval(d time.Duration) WaitOption {
	return func(c *waitConfig) { c.poll = d }
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
			// Matched per LINE, never against the whole screen as one string.
			//
			// A screen is lines, and callers write line-oriented patterns:
			// `^>>> $` for a REPL prompt, `\$ $` for a shell. Go's ^ and $
			// anchor to the whole text by default, so whole-screen matching
			// makes every one of those silently never match — while a plain
			// substring still works, which is what makes the bug so easy to
			// ship.
			if line, ok := firstMatchingLine(expression, screen.Text); ok {
				screen.Matched = true
				screen.Line = line
				return screen, nil
			}
		}
		if time.Now().After(deadline) {
			return last, backend.Errorf(backend.CodeTimeout,
				"the pattern %q did not appear on %s within %s%s",
				pattern, s.name, cfg.timeout, anchorHint(expression, last.Text))
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
	// Warnings never reaches the payload: the run's shape is semver-bound and
	// disclosure travels on the envelope, exactly as it does for a capture.
	Warnings []Warning `json:"-"`
}

// A RunOption configures Exec and Start.
type RunOption func(*engine.Runner)

// RunTimeout bounds how long a run waits for its command.
func RunTimeout(d time.Duration) RunOption {
	return func(r *engine.Runner) { r.Timeout = d }
}

// RunInterval sets how often a run checks for its completion marker.
func RunInterval(d time.Duration) RunOption {
	return func(r *engine.Runner) { r.Poll = d }
}

// PollWindow sets how deep into scrollback a detached poll looks for the
// completion marker. It is ignored where scrollback depth is the backend's own.
func PollWindow(lines int) RunOption {
	return func(r *engine.Runner) { r.Window = lines }
}

// Exec runs a command and waits for it to finish.
//
// A non-zero exit code is a RESULT, not an error: whether the protocol worked
// and what the command's own status was are two independent outcomes, and
// conflating them makes an ordinary failing command look like Olympus broke
// (behavior §12.1).
func (s *Session) Exec(ctx context.Context, command string, opts ...RunOption) (Result, error) {
	result, err := s.runner(opts...).Exec(ctx, s.name, command)
	if err != nil {
		return Result{}, err
	}
	out := Result{ExitCode: result.ExitCode, Output: result.Output}
	if result.Truncated {
		out.Warnings = append(out.Warnings, truncatedRunOutput)
	}
	return out, nil
}

func (s *Session) runner(opts ...RunOption) engine.Runner {
	r := engine.Runner{
		Backend:  s.ol.backend,
		Locks:    s.ol.locks,
		Key:      s.key(),
		LockWait: s.ol.lockWait,
		Timeout:  DefaultRunTimeout,
		Poll:     DefaultRunPoll,
	}
	for _, opt := range opts {
		opt(&r)
	}
	return r
}

// firstMatchingLine returns the first line a pattern matches.
//
// Each line is tried BOTH as captured and with trailing whitespace trimmed,
// because a terminal's padding is invisible to the person writing the pattern
// and either reading can be the intended one:
//
//   - `^42$` is written against a line the caller sees as "42". A terminal may
//     have padded it out to the pane's width, so `$` would anchor past a column
//     of spaces and never match without the trim.
//   - `^>>> $` is written against a REPL prompt whose trailing space is real and
//     is the whole point of the pattern. Trimming would destroy exactly what it
//     is looking for.
//
// Requiring the caller to know which one their terminal produced would make the
// pattern depend on the pane's width, which is not something they control.
func firstMatchingLine(expression *regexp.Regexp, screen string) (string, bool) {
	for _, line := range strings.Split(screen, "\n") {
		trimmed := strings.TrimRight(line, " \t\r")
		if expression.MatchString(line) || expression.MatchString(trimmed) {
			// The trimmed form is returned: the trailing spaces are invisible
			// anyway, and a caller printing this wants the line, not padding.
			return trimmed, true
		}
	}
	return "", false
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
func (s *Session) Start(ctx context.Context, command string, opts ...RunOption) (*Job, error) {
	id, err := s.runner(opts...).Start(ctx, s.name, command)
	if err != nil {
		return nil, err
	}
	return &Job{session: s, id: id}, nil
}

// A Started is what starting a detached run reports: the identifier a caller
// re-presents to poll it.
//
// It lives here rather than in a door because both doors mirror the ergonomic
// layer (§15.6). Each used to spell this shape for itself — the CLI through an
// anonymous map, MCP through a private struct — which is two independent
// definitions of one semver-bound payload, agreeing by coincidence and with
// nothing to notice if either drifted.
type Started struct {
	CommandID string `json:"command_id"`
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
func (j *Job) Poll(ctx context.Context, opts ...RunOption) (PollResult, error) {
	return j.session.Poll(ctx, j.id, opts...)
}

// Poll reports on a detached run by id, for a caller resuming from nothing but
// the pair.
func (s *Session) Poll(ctx context.Context, id string, opts ...RunOption) (PollResult, error) {
	runner := s.runner(opts...)
	got, err := runner.PollRun(ctx, s.name, id)
	if err != nil {
		return PollResult{}, err
	}
	out := PollResult{
		State:    string(got.Status),
		ExitCode: got.ExitCode,
		Output:   got.Output,
		Reason:   got.Reason,
		// Two disclosures, and they are not the same shape. One backend
		// ignores the window entirely and says so on every poll; another
		// honours it up to a ceiling and says so only when the request was
		// above it (§0.8). The ceiling is judged against the EFFECTIVE window,
		// never the raw option: the field is zero until the engine substitutes
		// its default, so reading it raw silenced the disclosure on the one
		// path nobody passes a window on.
		Warnings: append(warn(s.ol.resolution.Backend, opPollWindow),
			warnDepth(s.ol.resolution.Backend, runner.PollWindow())...),
	}
	if got.Truncated {
		out.Warnings = append(out.Warnings, truncatedRunOutput)
	}
	return out, nil
}

// ExitStatus reads a caller-supplied completion marker off the screen.
//
// The marker is always caller-supplied and there is deliberately no default: a
// fixed one would collide with ordinary output or stale scrollback, and weaken
// the caller-controlled uniqueness the design assumes (behavior §14).
func (s *Session) ExitStatus(ctx context.Context, marker string, lines int) (int, bool, error) {
	if lines <= 0 {
		lines = engine.DetachedWindow
	}
	screen, err := s.Screen(ctx, WithHistory(lines))
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

// Presses sets how many interrupts to send before waiting.
func Presses(n int) StopOption {
	return func(p *engine.KillPolicy) { p.Presses = n }
}

// InterruptTimeout bounds the POLL phase only, so total wall time is
// presses*gap plus this.
func InterruptTimeout(d time.Duration) StopOption {
	return func(p *engine.KillPolicy) { p.Timeout = d }
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

// anchorHint explains a timeout whose only cause was a line anchor.
//
// A pattern like `^>>>\s*$` cannot match a prompt a program painted onto the row
// the shell's echo is on — behavior §7.3.1 has the measurement — and the capture
// shows the text plainly, so the failure reads as a lie. This turns that into an
// answer.
//
// Deliberately conservative. It fires only when the SAME pattern with its line
// anchors removed does match, so an ordinary timeout on genuinely absent text
// says nothing extra: a wrong guess would send a caller to edit a pattern that
// was correct.
func anchorHint(expression *regexp.Regexp, screen string) string {
	source := expression.String()
	if !strings.HasPrefix(source, "^") && !strings.HasSuffix(source, "$") {
		return ""
	}
	unanchored := strings.TrimSuffix(strings.TrimPrefix(source, "^"), "$")
	if unanchored == "" {
		return ""
	}
	relaxed, err := regexp.Compile(unanchored)
	if err != nil {
		return ""
	}
	if !relaxed.MatchString(screen) {
		return ""
	}
	return " (it IS on screen, but not at a line boundary: the anchors in the pattern are what rejected it — " +
		"a program that paints by cursor positioning can leave its prompt mid-line, see behavior §7.3.1)"
}
