// Package tmux drives tmux.
//
// The rules it implements are specified in docs/terminal-behavior.md, and it is
// proved against backend/backendtest. Where a comment here names a section, the
// obvious implementation is the wrong one and the section says why.
package tmux

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/husniadil/olympus/backend"
)

// DefaultSocket is the socket name used when none is chosen (behavior §17.1).
const DefaultSocket = "olympus"

// A Tmux is a backend driving one tmux server, identified by its socket.
//
// tmux addresses a server two ways and they are not interchangeable: a NAME
// resolves to a socket inside a per-user directory tmux chooses, while a PATH
// is used verbatim. The name is the familiar form; the path is what lets a
// caller put the socket somewhere they control — a project directory, a mounted
// volume, a directory with tighter permissions than the shared one.
type Tmux struct {
	socket     string
	socketPath string
	buffers    atomic.Int64
}

// An Option configures a backend.
type Option func(*Tmux)

// WithSocket selects the tmux socket by NAME, which tmux resolves inside its
// own per-user directory. Tests MUST use a private one (§2.9).
func WithSocket(name string) Option {
	return func(t *Tmux) { t.socket = name; t.socketPath = "" }
}

// WithSocketPath selects the tmux socket by PATH, used verbatim.
//
// This is what makes tmux's isolation posture as controllable as a
// directory-based backend's: the socket can live wherever the caller wants
// rather than in a directory shared with every other tmux server that user
// runs. It overrides any name.
func WithSocketPath(path string) Option {
	return func(t *Tmux) { t.socketPath = path }
}

// New builds a tmux backend.
func New(opts ...Option) *Tmux {
	t := &Tmux{socket: DefaultSocket}
	for _, opt := range opts {
		opt(t)
	}
	return t
}

// Capabilities reports static facts, with no subprocess (behavior §13).
func (t *Tmux) Capabilities() backend.Capabilities {
	return backend.Capabilities{
		Backend: backend.Tmux,
		// Scrollback is reachable, but only by asking for a capture window
		// rather than by the backend handing back full history natively.
		NativeScrollback: false,
		Views:            true,
		RemainOnExit:     true,
		ServerEnv:        true,
		// send-keys delivers the control range, so a full-screen program can
		// be driven and not merely watched.
		ControlKeys: true,
		SpawnSizing: true,
		// new-session takes the argv and execs it as the pane's process.
		SpawnCommand:    true,
		SessionStatus:   true,
		TracksAltScreen: true,
		// Named sockets in tmux's per-user directory are its servers, and the
		// directory can be read (§13.2).
		Servers: true,
	}
}

func (t *Tmux) Version(ctx context.Context) (string, error) {
	out, err := t.run(ctx, nil, "-V")
	if err != nil {
		return "", err
	}
	// "tmux 3.7b"
	return strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(out), "tmux")), nil
}

// Target scopes.
//
// tmux resolves a target differently depending on what the command operates on,
// and the exact-match "=" prefix does not paper over that: a session target is
// "=name", a window target "=name:", a pane target "=name:.". send-keys and
// capture-pane reject a bare session target outright, and set-option -w rejects
// it with "no such window" — while any command that already ran keeps its
// effect, which is why §2.2 requires the create chain to clean up after itself.
func sessionTarget(name string) string { return "=" + name }
func windowTarget(name string) string  { return "=" + name + ":" }
func paneTarget(name string) string    { return "=" + name + ":." }

func (t *Tmux) Create(ctx context.Context, spec backend.CreateSpec) (backend.Session, error) {
	if spec.Name == "" {
		return backend.Session{}, backend.Errorf(backend.CodeUsage, "a session needs a name")
	}

	// Olympus configures only servers it STARTS (§17.5). Pinning reaches every
	// session on the server, so on one the operator already runs it would
	// change sessions nobody asked us about — an effect well outside the target
	// we were given (§0.4). A server that is not up yet is one this very
	// invocation brings up, and there is nobody else on it to disturb.
	//
	// No marker is needed to remember the decision: our own second Create finds
	// the server already up and skips the pins, which is correct because the
	// first Create's pins are server-global and still in force.
	var args []string
	if !t.ServerRunning(ctx) {
		// Ahead of new-session, in the SAME invocation: a pane reads these when
		// it spawns, so applying them afterwards would configure the next
		// session and leave this one misconfigured (§17.5).
		args = pinManagedOptions()
	}
	args = append(args, "new-session", "-d", "-s", spec.Name)
	if spec.Dir != "" {
		args = append(args, "-c", spec.Dir)
	}
	if spec.Cols > 0 {
		args = append(args, "-x", strconv.Itoa(spec.Cols))
	}
	if spec.Rows > 0 {
		args = append(args, "-y", strconv.Itoa(spec.Rows))
	}
	// Per-session environment, not only the client's. A new session's
	// environment is seeded from the SERVER's global environment, fixed when
	// the server booted — so a server another process started hands our
	// sessions its dirty environment no matter what we set on our own exec
	// (§1.2). The empty-valued entries are set-to-empty rather than omitted,
	// which is what overrides a poisoned server global.
	for _, kv := range sessionEnv() {
		args = append(args, "-e", kv)
	}
	if len(spec.Command) > 0 {
		args = append(args, spec.Command...)
	}

	// Chained into the SAME invocation, never a second call. A fast-exiting
	// command tears its window down before a second tmux invocation can run,
	// which then fails with "no such window" — so remain-on-exit would do
	// nothing for exactly the fastest-failing commands a caller most wants a
	// corpse to inspect, and allow-passthrough would turn a successful spawn
	// into a backend error. Order matters too: pin the corpse first, or an
	// instantly-exiting pane vanishes between the two chained commands before
	// the corpse flag lands (§2.2).
	if spec.RemainOnExit {
		args = append(args, ";", "set-option", "-w", "-t", windowTarget(spec.Name), "remain-on-exit", "on")
	}
	args = append(args, ";", "set-option", "-p", "-t", paneTarget(spec.Name), "allow-passthrough", "on")

	if _, err := t.run(ctx, nil, args...); err != nil {
		// new-session may well have succeeded before a later link of the chain
		// failed, leaving a half-configured session. Killing it best-effort is
		// what stops that leaking (§2.2).
		_, _ = t.run(context.WithoutCancel(ctx), nil, "kill-session", "-t", sessionTarget(spec.Name))
		return backend.Session{}, err
	}

	sessions, err := t.Sessions(ctx)
	if err != nil {
		return backend.Session{}, err
	}
	for _, s := range sessions {
		if s.Name == spec.Name {
			s.Outcome = backend.OutcomeCreated
			return s, nil
		}
	}

	// Created, and already finished. Without a corpse flag a session whose
	// command exits takes the session with it, so a fast-exiting command is
	// routinely gone before this listing runs. That is the documented
	// behaviour of the option, not an infrastructure failure — reporting it as
	// one would make an ordinary short command look like Olympus broke.
	//
	// The row is synthesized rather than omitted so the caller still learns
	// what happened: it was created, and it is gone.
	return backend.Session{
		Name:     spec.Name,
		Liveness: backend.LivenessGone,
		CWD:      spec.Dir,
		Outcome:  backend.OutcomeCreated,
	}, nil
}

const sessionFormat = "#{session_name}\x1f#{session_id}\x1f#{session_attached}\x1f#{pane_dead}\x1f#{session_path}"

func (t *Tmux) Sessions(ctx context.Context) ([]backend.Session, error) {
	out, err := t.run(ctx, nil, "list-sessions", "-F", sessionFormat)
	if err != nil {
		if isNoServer(err) || isNotFound(err) {
			// Nothing to find here; nothing went wrong asking (§3.3).
			return nil, nil
		}
		return nil, err
	}

	var sessions []backend.Session
	for _, line := range splitLines(out) {
		f := SplitFields(line)
		if len(f) < 5 {
			continue
		}
		sessions = append(sessions, backend.Session{
			Name:     f[0],
			ID:       f[1],
			Attached: f[2] == "1",
			Dead:     f[3] == "1",
			// Every row tmux lists is one tmux vouches for. A corpse stays
			// present with the dead flag set: liveness and deadness are
			// different questions (§3.2).
			Liveness: backend.LivenessPresent,
			CWD:      f[4],
		})
	}
	return sessions, nil
}

const paneFormat = "#{pane_id}\x1f#{session_name}\x1f#{session_id}\x1f#{window_index}\x1f#{pane_dead}\x1f#{session_created}\x1f#{pane_current_path}\x1f#{pane_current_command}"

func (t *Tmux) Panes(ctx context.Context, target string) ([]backend.Pane, error) {
	args := []string{"list-panes", "-F", paneFormat}
	if target == "" {
		args = append(args, "-a")
	} else {
		args = append(args, "-s", "-t", sessionTarget(target))
	}

	out, err := t.run(ctx, nil, args...)
	if err != nil {
		// Only the UNTARGETED listing collapses an absent server into an empty
		// result. Asked about one named session, absence is that session's
		// absence and must be reported as not-found naming it (§12.3) — an
		// empty list there says "that session has no panes", which is a
		// different and false answer, and a caller branching on success takes
		// the wrong path with no error to notice.
		if target == "" && isNoServer(err) {
			return nil, nil
		}
		return nil, named(target, err)
	}

	var panes []backend.Pane
	for _, line := range splitLines(out) {
		f := SplitFields(line)
		if len(f) < 8 {
			continue
		}
		panes = append(panes, backend.Pane{
			ID:          f[0],
			SessionName: f[1],
			SessionID:   f[2],
			WindowIndex: atoi(f[3]),
			Dead:        f[4] == "1",
			// Session-granular on purpose: tmux has no per-pane birth time.
			// #{pane_start_time} and #{pane_created} do not exist and expand
			// to the empty string WITH exit 0, so trusting one yields a
			// silently zeroed column rather than an error (§3.4).
			CreatedAt:      int64(atoi(f[5])),
			CurrentPath:    f[6],
			CurrentCommand: f[7],
			Liveness:       backend.LivenessPresent,
		})
	}
	return panes, nil
}

// Probe answers presence, never a transport error (§3.5).
func (t *Tmux) Probe(ctx context.Context, target string) backend.State {
	_, err := t.run(ctx, nil, "has-session", "-t", sessionTarget(target))
	switch {
	case err == nil:
		return backend.StatePresent
	case isNotFound(err), isNoServer(err):
		// A name that never existed is absent even with no server running.
		// The error arm is reserved for a genuinely unreachable backend: a
		// caller polling across a flaky backend needs "definitely gone" and
		// "could not ask" to be different answers, so it neither wrongly
		// recreates nor wrongly gives up (§3.5).
		return backend.StateAbsent
	default:
		return backend.StateError
	}
}

func (t *Tmux) Kill(ctx context.Context, target string) error {
	_, err := t.run(ctx, nil, "kill-session", "-t", sessionTarget(target))
	if err != nil && (isNotFound(err) || isNoServer(err)) {
		// The desired state already holds.
		return nil
	}
	return err
}

// Interrupt delivers the interrupt as a keypress into the pane's tty, so the
// kernel raises it on the foreground process group.
func (t *Tmux) Interrupt(ctx context.Context, target string) error {
	return t.Press(ctx, target, backend.KeyCtrlC)
}

// Type injects literal text without submitting it (§4.3).
func (t *Tmux) Type(ctx context.Context, target, text string) error {
	return t.inject(ctx, target, text, false)
}

// Paste injects multi-line text without submitting it (§4.6). It is literal
// injection with bracketed-paste framing added, so a paste-aware consumer
// receives the whole text as one un-executed paste event.
func (t *Tmux) Paste(ctx context.Context, target, text string) error {
	return t.inject(ctx, target, text, true)
}

func (t *Tmux) inject(ctx context.Context, target, text string, bracketed bool) error {
	// A buffer name unique per call. Two concurrent injections sharing a name
	// race: one call's load-buffer clobbers the other's text before
	// paste-buffer consumes it (§4.1).
	name := fmt.Sprintf("olympus-%d-%d", os.Getpid(), t.buffers.Add(1))

	// load-buffer from stdin rather than send-keys -l, which mangles special
	// characters and cannot carry arbitrary bytes (§4.1).
	if _, err := t.run(ctx, strings.NewReader(text), "load-buffer", "-b", name, "-"); err != nil {
		return named(target, err)
	}

	args := []string{"paste-buffer", "-d", "-b", name, "-t", paneTarget(target)}
	if bracketed {
		args = append(args, "-p")
	}
	if _, err := t.run(ctx, nil, args...); err != nil {
		// -d is not unconditional cleanup: tmux deletes the buffer only when
		// the paste succeeds. If the pane vanished between the two calls — a
		// real race window — the buffer leaks forever without this. Its own
		// failure is swallowed so it can never mask the real error (§4.2).
		_, _ = t.run(context.WithoutCancel(ctx), nil, "delete-buffer", "-b", name)
		return named(target, err)
	}
	return nil
}

func (t *Tmux) Press(ctx context.Context, target string, keys ...backend.Key) error {
	args := []string{"send-keys", "-t", paneTarget(target)}
	for _, k := range keys {
		name, ok := keyName(k)
		if !ok {
			// Input the caller could have fixed by changing one argument,
			// which §12 makes the definition of a usage error.
			return backend.Errorf(backend.CodeUsage, "unknown key %q", string(k))
		}
		args = append(args, name)
	}
	_, err := t.run(ctx, nil, args...)
	return named(target, err)
}

// Submit writes the terminator alone, as a keypress rather than as part of a
// paste (§4.5).
func (t *Tmux) Submit(ctx context.Context, target string) error {
	return t.Press(ctx, target, backend.KeyEnter)
}

// SendAtomic delivers text and its terminator as one caller-visible unit, so a
// caller retrying a failed invocation can never leave a typed-but-unsubmitted
// line behind to double (§4.7).
func (t *Tmux) SendAtomic(ctx context.Context, target, text string) error {
	pane := paneTarget(target)
	// Two send-keys subcommands chained by a literal ";" argv element into one
	// client invocation: the terminator is still its own write, so it registers
	// as a keypress on a paste-detecting consumer rather than becoming a
	// literal newline inside an input box (§4.5).
	_, err := t.run(ctx, nil,
		"send-keys", "-t", pane, "-l", "--", escapeTrailingSemicolon(text),
		";", "send-keys", "-t", pane, "Enter",
	)
	return named(target, err)
}

// escapeTrailingSemicolon guards the chained send-keys path.
//
// tmux's ";" chaining separator treats an unescaped TRAILING ";" byte in a text
// argv element as a command separator rather than literal text, so "echo A;
// echo B;" lands with the final ";" dropped and text that is just ";" lands
// nothing at all. Interior semicolons are untouched — only a trailing one
// (§4.8).
func escapeTrailingSemicolon(text string) string {
	if strings.HasSuffix(text, ";") && !strings.HasSuffix(text, `\;`) {
		return text[:len(text)-1] + `\;`
	}
	return text
}

func (t *Tmux) Screen(ctx context.Context, target string, opts backend.ScreenOpts) (backend.Capture, error) {
	meta, err := t.screenMeta(ctx, target)
	if err != nil {
		return backend.Capture{}, named(target, err)
	}

	args := []string{"capture-pane", "-p", "-t", paneTarget(target)}
	if opts.Colors {
		args = append(args, "-e")
	}
	if opts.HistoryLines > 0 {
		// -J is dropped whenever history is requested. It is correct on the
		// live viewport, where nothing has been wrapped and re-flowed since;
		// across scrollback it rejoins a long line tmux already wrapped at
		// capture time with its own historical continuation, silently merging
		// two scrollback lines that never appeared as one on screen (§5.1).
		args = append(args, "-S", "-"+strconv.Itoa(opts.HistoryLines))
	} else {
		args = append(args, "-J")
	}

	out, err := t.run(ctx, nil, args...)
	if err != nil {
		return backend.Capture{}, named(target, err)
	}
	return backend.Capture{Text: out, Meta: meta}, nil
}

// ScreenMeta reports capture metadata without capturing (behavior §5.3).
func (t *Tmux) ScreenMeta(ctx context.Context, target string) (backend.ScreenMeta, error) {
	meta, err := t.screenMeta(ctx, target)
	return meta, named(target, err)
}

func (t *Tmux) screenMeta(ctx context.Context, target string) (backend.ScreenMeta, error) {
	out, err := t.run(ctx, nil, "list-panes", "-t", paneTarget(target), "-F", "#{alternate_on}\x1f#{scroll_position}")
	if err != nil {
		return backend.ScreenMeta{}, err
	}
	lines := splitLines(out)
	if len(lines) == 0 {
		return backend.ScreenMeta{}, nil
	}
	f := SplitFields(lines[0])
	meta := backend.ScreenMeta{AltScreen: f[0] == "1"}
	if len(f) > 1 {
		// #{scroll_position} expands to the empty string outside copy mode,
		// with exit 0. Parsing that as zero is the answer §5.5 wants; treating
		// it as an error would make every ordinary capture fail.
		meta.ScrollPosition = atoi(f[1])
	}
	return meta, nil
}

func (t *Tmux) ServerEnv(ctx context.Context, key string) (string, bool, error) {
	out, err := t.run(ctx, nil, "show-environment", "-g", key)
	if err != nil {
		if isNoServer(err) || isNotFound(err) || strings.Contains(errText(err), "unknown variable") {
			// Asked, and got a real negative answer. This is not the same as a
			// backend with no such concept, which is unsupported (§12).
			return "", false, nil
		}
		return "", false, err
	}
	line := strings.TrimSpace(out)
	if strings.HasPrefix(line, "-") {
		// tmux spells "explicitly unset" as a leading dash.
		return "", false, nil
	}
	_, value, found := strings.Cut(line, "=")
	if !found {
		return "", false, nil
	}
	return value, true, nil
}

func splitLines(s string) []string {
	s = strings.TrimRight(s, "\n")
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}

func atoi(s string) int {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil {
		return 0
	}
	return n
}

// addressing is how every tmux invocation names the server it means.
//
// One helper, used by run() and by the attach path alike: attach builds its own
// argv, so a second copy of this rule is a second place to get it wrong, and
// getting it wrong there hands the operator's terminal to a session on a
// different server (§17.2).
//
// -S takes a path verbatim; -L takes a name tmux resolves itself. Passing both
// would let tmux pick, which is not a decision to leave to it.
func (t *Tmux) addressing() []string {
	if t.socketPath != "" {
		return []string{"-S", t.socketPath}
	}
	return []string{"-L", t.socket}
}

// waitDelay bounds how long a cancelled subprocess may keep a call waiting.
//
// Cancelling a context kills the CHILD, which is not enough to unblock the read:
// a grandchild inherits the same output pipe, and the copy waits on the pipe
// rather than on the process. Without this, a cancelled call can hang forever
// past its own deadline. See backend/meja for the case that proved it.
const waitDelay = 2 * time.Second

// run invokes the tmux client and maps its failure into the error vocabulary.
func (t *Tmux) run(ctx context.Context, stdin io.Reader, args ...string) (string, error) {
	full := append(t.addressing(), args...)
	cmd := exec.CommandContext(ctx, "tmux", full...)
	cmd.Stdin = stdin
	cmd.Env = clientEnv()
	cmd.WaitDelay = waitDelay

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return stdout.String(), classify(err, stderr.String(), args)
	}
	return stdout.String(), nil
}

// exitError carries tmux's own message so the classifiers can read it back.
type exitError struct {
	stderr string
	err    error
	code   backend.Code
}

func (e *exitError) Error() string { return e.stderr }
func (e *exitError) Unwrap() error { return e.err }

func errText(err error) string {
	var e *backend.Error
	if errors.As(err, &e) {
		return e.Error()
	}
	return err.Error()
}

func classify(err error, stderr string, args []string) error {
	msg := strings.TrimSpace(stderr)
	lower := strings.ToLower(msg)

	var execErr *exec.Error
	if errors.As(err, &execErr) {
		return backend.Wrapf(backend.CodeBackendUnavailable, err, "tmux is not available")
	}
	if msg == "" {
		msg = err.Error()
	}

	switch {
	case strings.Contains(lower, "no server running"), strings.Contains(lower, "error connecting to"):
		// Nothing is listening on the socket. That is absence, not a backend
		// that cannot be reached: §12.3 makes "no server running" collapse into
		// the negative answer for every question where the negative answer is
		// meaningful. The verbs that can say so — listing, probe,
		// server-environment — recognise it and answer empty, absent or
		// not-present; every target-addressed verb turns it into not-found
		// naming its own target.
		return backend.Errorf(backend.CodeSessionNotFound, "%s", msg)
	case strings.Contains(lower, "can't find session"),
		strings.Contains(lower, "session not found"),
		strings.Contains(lower, "no such session"),
		strings.Contains(lower, "can't find pane"),
		strings.Contains(lower, "no such pane"),
		strings.Contains(lower, "no such window"),
		strings.Contains(lower, "can't find window"):
		return backend.Errorf(backend.CodeSessionNotFound, "%s", msg)
	default:
		return backend.Wrapf(backend.CodeUnexpected, errors.New(msg), "tmux %s", args[0])
	}
}

func isNotFound(err error) bool {
	return backend.CodeOf(err) == backend.CodeSessionNotFound
}

// named rewrites a not-found so it names the target the caller asked about.
//
// tmux reports its own vocabulary — a pane id, a socket path, or nothing at all
// when the server is simply absent — and a caller holding a session name cannot
// match any of those against what it asked for.
func named(target string, err error) error {
	if err == nil || !isNotFound(err) {
		return err
	}
	if strings.Contains(err.Error(), target) {
		return err
	}
	return backend.Errorf(backend.CodeSessionNotFound, "no session %s", target)
}

// isNoServer reports whether tmux failed because nothing is listening.
//
// tmux spells this two different ways depending on the subcommand: list-sessions
// says "no server running on <socket>", while most others fail at connect time
// with "error connecting to <socket> (No such file or directory)". Matching only
// the first turns every no-server case on every other verb into an UNEXPECTED
// error, which is the opposite of §12.3's rule that absence is a real answer.
func isNoServer(err error) bool {
	msg := strings.ToLower(errText(err))
	return strings.Contains(msg, "no server running") ||
		strings.Contains(msg, "error connecting to")
}

// Scope reports how this backend addresses its server: the socket path when one
// was given, otherwise the socket name. It is what a lock key and a diagnostic
// identify the server by, so the two forms must never collapse to the same
// string — they are different servers.
func (t *Tmux) Scope() string {
	if t.socketPath != "" {
		return t.socketPath
	}
	return t.socket
}

// Socket reports the tmux socket this backend drives. It exists so a test can
// hold its raw verification calls to the same isolation rule as the backend
// itself (behavior §2.9), rather than reaching for the operator's default
// server to check what happened.
func (t *Tmux) Socket() string { return t.socket }

// SessionOf reports which session owns a pane.
//
// It exists for a process asking which session it is ITSELF running in: tmux
// tells a pane its own id through the environment but not its session's name,
// so the name has to be asked for. Ordinary target resolution goes the same
// way (§10) but answers for a caller outside; this answers for one inside.
func (t *Tmux) SessionOf(ctx context.Context, pane string) (string, error) {
	panes, err := t.Panes(ctx, "")
	if err != nil {
		return "", err
	}
	for _, p := range panes {
		if p.ID == pane {
			return p.SessionName, nil
		}
	}
	return "", backend.Errorf(backend.CodeSessionNotFound, "no pane %s on this server", pane)
}
