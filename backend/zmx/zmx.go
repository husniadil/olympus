// Package zmx drives zmx.
//
// The rules it implements are specified in docs/terminal-behavior.md, and it is
// proved against backend/backendtest. Where a comment here names a section, the
// obvious implementation is the wrong one and the section says why.
package zmx

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/husniadil/olympus/backend"
)

// sunPathMax is the platform's limit on a unix socket path, NUL included. The
// daemon places a session's socket at <dir>/<name> — a bare name, no suffix —
// so a name is only usable if the whole path fits (behavior §2.5).
const sunPathMax = 104

// registrationTimeout bounds the wait for a spawned session to appear in the
// listing (behavior §2.4). Three seconds is too short: on a loaded host the
// daemon registers the session after a tight deadline, so the caller reports
// failure and races its own cleanup against the daemon's creation, producing a
// live but completely untracked orphan.
const (
	registrationTimeout = 15 * time.Second
	registrationEnv     = "OLYMPUS_ZMX_REGISTRATION_TIMEOUT"
	registrationPoll    = 100 * time.Millisecond
)

// A Zmx is a backend driving one zmx daemon, identified by its socket
// directory.
type Zmx struct {
	dir string
}

// An Option configures a backend.
type Option func(*Zmx)

// WithDir selects the daemon's socket directory, exported as ZMX_DIR to every
// invocation.
//
// Tests MUST set this. zmx has no equivalent of a private socket flag: sessions
// are global to one daemon per user, so session-name namespacing alone does not
// protect the operator. However carefully named, every test session would still
// land on the one shared daemon and destabilize real live attach clients
// (behavior §2.9).
func WithDir(dir string) Option {
	return func(z *Zmx) { z.dir = dir }
}

// New builds a zmx backend.
func New(opts ...Option) *Zmx {
	z := &Zmx{}
	for _, opt := range opts {
		opt(z)
	}
	return z
}

func (z *Zmx) Capabilities() backend.Capabilities {
	return backend.Capabilities{
		Backend: backend.Zmx,
		// zmx's history command already returns full scrollback, with no
		// separate viewport mode to opt into (§5.2).
		NativeScrollback: true,
		Views:            false,
		RemainOnExit:     false,
		ServerEnv:        false,
		// The send path does not reliably deliver control keys: Ctrl-X and
		// friends are dropped, so an editor can be opened and read but never
		// exited (§4.9). Capture is unaffected — a repaint IS reflected.
		ControlKeys: false,
		// No per-session metadata of any kind: zmx has no option, label or
		// annotation command, so there is nowhere a status could outlive the
		// process that set it.
		// zmx sizes a session from the client that attaches it.
		SpawnSizing: false,
		// `zmx attach <name> <argv>` execs argv as the session process.
		SpawnCommand:    true,
		SessionStatus:   false,
		TracksAltScreen: false,
		// One directory, one daemon: a single row, but an answerable one
		// (§13.2).
		Servers: true,
		// One client and no views: nothing to hide and nothing to attach bare.
		SessionClient: false,
		Bare:          false,
		// One pane per session: nothing to bring to the front.
		Focus:  false,
		Rename: false,
		// No agent detection of its own, but a session can be captured, so
		// the layer above reads an agent's status off its screen (§3.7).
		AgentStatus: true,
	}
}

func (z *Zmx) Version(ctx context.Context) (string, error) {
	out, err := z.run(ctx, "version")
	if err != nil {
		return "", err
	}
	// "zmx\t0.6.0\n…" — the first line's last field is the version.
	line := strings.TrimSpace(strings.SplitN(strings.TrimSpace(out), "\n", 2)[0])
	fields := strings.Fields(line)
	if len(fields) == 0 {
		return "", backend.Errorf(backend.CodeUnexpected, "zmx reported no version")
	}
	return fields[len(fields)-1], nil
}

// Dir reports the socket directory this backend drives, so a test can hold its
// raw verification calls to the same isolation rule as the backend (§2.9).
func (z *Zmx) Dir() string { return z.dir }

// SocketDir is the directory this backend's daemon keeps its sockets in, by
// zmx's own order: the one chosen, then ZMX_DIR, then $XDG_RUNTIME_DIR/zmx,
// then $TMPDIR/zmx-<uid>. It names the daemon whichever of those spellings
// reached it, which is what a lock key needs (§11.1).
func (z *Zmx) SocketDir() string { return z.validationDir() }

// validationDir resolves the socket directory for the purpose of checking a
// name against the budget.
//
// This deliberately differs from resolving it for daemon selection, which
// returns a bare TMPDIR and would under-count the budget by the whole
// "zmx-<uid>" component. The order is zmx's own: ZMX_DIR, then
// $XDG_RUNTIME_DIR/zmx, then $TMPDIR/zmx-<uid> (§2.5).
func (z *Zmx) validationDir() string {
	if z.dir != "" {
		return z.dir
	}
	if v := os.Getenv("ZMX_DIR"); v != "" {
		return v
	}
	if v := os.Getenv("XDG_RUNTIME_DIR"); v != "" {
		return filepath.Join(v, "zmx")
	}
	return filepath.Join(os.TempDir(), fmt.Sprintf("zmx-%d", os.Getuid()))
}

// validateName rejects a name whose socket path would not fit, before any zmx
// invocation.
//
// Without this the failure is misleading rather than silent: zmx errors loudly,
// but the spawn path deliberately ignores the client's exit code, falls through
// to the registration poll, and times out into an error that never mentions the
// real cause (§2.5).
func (z *Zmx) validateName(name string) error {
	if name == "" {
		return backend.Errorf(backend.CodeUsage, "a session needs a name")
	}
	// The name is the socket's own file name, so it has to be one path
	// component. zmx does not refuse the others, and creation then polls for a
	// session that never registers and blames the backend (§2.5).
	if strings.ContainsAny(name, "/\x00") || name == "." || name == ".." {
		return backend.Errorf(backend.CodeUsage,
			"session name %q cannot be a file name in the socket directory; a session cannot be named that", name)
	}
	dir := z.validationDir()
	budget := sunPathMax - 1
	if length := len(dir) + 1 + len(name); length > budget {
		return backend.Errorf(backend.CodeUsage,
			"session name %q makes the socket path %s %d bytes long, over the %d-byte budget",
			name, filepath.Join(dir, name), length, budget)
	}
	return nil
}

func (z *Zmx) Create(ctx context.Context, spec backend.CreateSpec) (backend.Session, error) {
	// Rejected before branching on anything and before any invocation: zmx has
	// no corpse concept, since the daemon reaps finished sessions itself
	// (§2.7).
	if spec.RemainOnExit {
		return backend.Session{}, backend.Errorf(backend.CodeUnsupported,
			"zmx has no remain-on-exit: the daemon reaps finished sessions itself")
	}
	if err := z.validateName(spec.Name); err != nil {
		return backend.Session{}, err
	}

	// exec, never type. "zmx run <name> <cmd>" TYPES the command into a login
	// shell, echoing the command text into scrollback — and zmx's native
	// scrollback shows it, putting the spawn command line into the session's
	// own output (§2.3).
	//
	// Initial size is accepted for interface conformance and ignored: zmx has
	// no spawn-time sizing concept, and the PTY is sized entirely by whatever
	// client attaches later (§2.1). Papering over that would be a lie.
	args := append([]string{"attach", spec.Name}, spec.Command...)
	cmd := z.command(ctx, args...)
	cmd.Dir = spec.Dir
	// The client's exit is not a failure signal. With stdin ignored it hits
	// EOF and exits as soon as it has forked the daemon — routinely before the
	// session appears in the listing. The daemon is a separate, longer-lived
	// process, so the only correct check is polling the listing (§2.4).
	_ = cmd.Run()

	deadline := time.Now().Add(registrationBudget())
	for {
		sessions, err := z.Sessions(ctx)
		if err != nil {
			return backend.Session{}, err
		}
		for _, s := range sessions {
			if s.Name == spec.Name {
				s.Outcome = backend.OutcomeCreated
				return s, nil
			}
		}
		if time.Now().After(deadline) {
			return backend.Session{}, backend.Errorf(backend.CodeBackendUnavailable,
				"session %s did not register with the zmx daemon within %s", spec.Name, registrationBudget())
		}
		select {
		case <-ctx.Done():
			return backend.Session{}, backend.Wrapf(backend.CodeTimeout, ctx.Err(), "waiting for session %s to register", spec.Name)
		case <-time.After(registrationPoll):
		}
	}
}

// registrationBudget is read at call time, never cached at process start, so an
// operator can raise it for a loaded host without restarting anything (§2.4).
func registrationBudget() time.Duration {
	if v := os.Getenv(registrationEnv); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			return d
		}
	}
	return registrationTimeout
}

func (z *Zmx) Sessions(ctx context.Context) ([]backend.Session, error) {
	rows, err := z.listRows(ctx)
	if err != nil {
		return nil, err
	}
	sessions := make([]backend.Session, 0, len(rows))
	for _, row := range rows {
		sessions = append(sessions, backend.Session{
			Name: row["name"],
			// zmx has no session id distinct from the name.
			ID:       row["name"],
			Attached: row["clients"] != "" && row["clients"] != "0",
			Dead:     false,
			Liveness: classifyLiveness(row),
			CWD:      row["start_dir"],
		})
	}
	return sessions, nil
}

// classifyLiveness maps a listing row's err field to the backend-owned
// tri-state (§3.2).
//
// err=ConnectionRefused is the only definitive death signal: zmx itself already
// deleted the stale socket this pass. Any other err — a Timeout under load, an
// Unexpected during a kill purge — is indeterminate, and a consumer must never
// finalize on it. Leaving this classification consumer-side is how it gets
// lost, and dead rows then survive reconciliation forever.
func classifyLiveness(row map[string]string) backend.Liveness {
	switch row["err"] {
	case "":
		return backend.LivenessPresent
	case "ConnectionRefused":
		return backend.LivenessGone
	default:
		return backend.LivenessUnknown
	}
}

// listRows parses the LONG listing form.
//
// The short form is not a smaller version of the same answer: it omits the err
// field entirely, and without that field every row classifies as present — the
// gone signal disappears and nothing can ever be reaped (§3.1).
func (z *Zmx) listRows(ctx context.Context) ([]map[string]string, error) {
	out, err := z.run(ctx, "list")
	if err != nil {
		return nil, err
	}

	var rows []map[string]string
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || !strings.Contains(line, "name=") {
			// "no sessions found in <dir>" is an empty list, not an error:
			// there is nothing to find, and nothing went wrong asking (§3.3).
			continue
		}
		row := map[string]string{}
		for _, field := range strings.Split(line, "\t") {
			key, value, found := strings.Cut(strings.TrimSpace(field), "=")
			if found {
				row[key] = value
			}
		}
		if row["name"] != "" {
			rows = append(rows, row)
		}
	}
	return rows, nil
}

func (z *Zmx) Panes(ctx context.Context, target string) ([]backend.Pane, error) {
	rows, err := z.listRows(ctx)
	if err != nil {
		return nil, err
	}
	panes := []backend.Pane{}
	for _, row := range rows {
		if target != "" && row["name"] != target {
			continue
		}
		created, _ := strconv.ParseInt(row["created"], 10, 64)
		// The session's process, which is the root of the tree the agent
		// listing walks (§3.7): the one live fact this row carries.
		pid, _ := strconv.Atoi(row["pid"])
		panes = append(panes, backend.Pane{
			// zmx has no pane concept, so the row is synthesized from the
			// session and keyed on its name. There is nothing else stable to
			// key it on, and an empty id would break every caller that
			// addresses panes.
			ID:          row["name"],
			SessionName: row["name"],
			SessionID:   row["name"],
			WindowIndex: 0,
			Dead:        false,
			CreatedAt:   created,
			// Static, not live: start_dir is captured at session creation and
			// never updated, and cmd is the spawn argv. Reading either as a
			// live tracker reports the original value forever (§3.4).
			CurrentPath:    row["start_dir"],
			CurrentCommand: row["cmd"],
			Liveness:       classifyLiveness(row),
			PID:            pid,
		})
	}
	if target != "" && len(panes) == 0 {
		return nil, backend.Errorf(backend.CodeSessionNotFound, "no session %s", target)
	}
	return panes, nil
}

// Probe answers presence, never a transport error (§3.5).
func (z *Zmx) Probe(ctx context.Context, target string) backend.State {
	sessions, err := z.Sessions(ctx)
	if err != nil {
		// A daemon hiccup must never read as absent: a caller polling across a
		// flaky backend needs "definitely gone" and "could not ask" to be
		// different answers.
		return backend.StateError
	}
	for _, s := range sessions {
		// A row zmx reported dead this pass (liveness gone) is the socket it
		// has just deleted: absent, or a write would go on to a session that
		// is not there and zmx send would still exit 0.
		if s.Name == target && s.Liveness != backend.LivenessGone {
			return backend.StatePresent
		}
	}
	// A name that never existed is absent even with no daemon running: zmx
	// reports a clean not-found rather than a connection failure, and the
	// error arm is reserved for a genuinely unreachable backend.
	return backend.StateAbsent
}

func (z *Zmx) Kill(ctx context.Context, target string) error {
	// zmx exits 0 for an absent name, which is already the desired state.
	_, err := z.run(ctx, "kill", target, "--force")
	return err
}

func (z *Zmx) Type(ctx context.Context, target, text string) error {
	if err := fitsInput(len(text)); err != nil {
		return err
	}
	if err := z.requirePresent(ctx, target); err != nil {
		return err
	}
	return z.send(ctx, target, text)
}

// Paste injects text with no terminator, framed as a bracketed paste where
// the session's program asked for one (§4.6).
//
// zmx has no framing of its own, so Olympus frames the text itself. Whether
// the program asked is read off `history --vt`, which serializes the
// terminal's current modes ahead of the screen's content: measured on zmx
// 0.6.0, the mode shows while it is set, is gone once the program resets it,
// and outlasts output that pushed the set out of the history. The mode is read
// and the text written in two calls, so a program that switches the mode in
// between gets the other form.
func (z *Zmx) Paste(ctx context.Context, target, text string) error {
	if text == "" {
		return z.Type(ctx, target, text)
	}
	// Counted with the markers, whether or not the mode turns out to want
	// them, so the refusal comes before anything is run.
	if err := fitsInput(len(pasteStart) + len(text) + len(pasteEnd)); err != nil {
		return err
	}
	if err := z.requirePresent(ctx, target); err != nil {
		return err
	}
	state, err := z.run(ctx, "history", target, "--vt")
	if err != nil {
		return err
	}
	if bracketedPaste(state) {
		text = pasteStart + text + pasteEnd
	}
	return z.send(ctx, target, text)
}

const (
	pasteMode  = "\x1b[?2004h"
	pasteStart = "\x1b[200~"
	pasteEnd   = "\x1b[201~"
)

// bracketedPaste reports whether a serialized terminal state has bracketed
// paste on. A cell never holds an ESC, so the sequence can only be the mode.
func bracketedPaste(vt string) bool {
	return strings.Contains(vt, pasteMode)
}

func (z *Zmx) Press(ctx context.Context, target string, keys ...backend.Key) error {
	var payload strings.Builder
	for _, k := range keys {
		seq, ok := keySequence(k)
		if !ok {
			return backend.Errorf(backend.CodeUsage, "unknown key %q", string(k))
		}
		payload.WriteString(seq)
	}
	if err := z.requirePresent(ctx, target); err != nil {
		return err
	}
	return z.send(ctx, target, payload.String())
}

func (z *Zmx) Submit(ctx context.Context, target string) error {
	return z.Press(ctx, target, backend.KeyEnter)
}

// SendAtomic delivers text and its terminator as one caller-visible unit.
//
// zmx has no subcommand chaining, so backend-level single-operation atomicity
// is unachievable here: the settle between the two writes is what makes the
// terminator register as a keypress rather than becoming a literal newline
// inside an input box (§4.5). Caller-visible atomicity comes from the
// per-session write lock held across both writes, one layer up (§4.7).
func (z *Zmx) SendAtomic(ctx context.Context, target, text string) error {
	// The terminator is counted: written while the text may still be queued,
	// it is dropped with nothing said if the two pass zmx's cap.
	if err := fitsInput(len(text) + 1); err != nil {
		return err
	}
	if err := z.requirePresent(ctx, target); err != nil {
		return err
	}
	if err := z.send(ctx, target, text); err != nil {
		return err
	}
	select {
	case <-ctx.Done():
		return backend.Unsubmitted(ctx.Err(), target)
	case <-time.After(backend.SubmitSettle):
	}
	if err := z.send(ctx, target, "\r"); err != nil {
		// Never silent success: the text is sitting unsubmitted in the input
		// line (§4.7).
		return backend.Unsubmitted(err, target)
	}
	return nil
}

// The two sizes zmx drops input past, with nothing said and exit status 0.
// Measured on 0.6.0 and 0.7.0, and read in their source, which 0.8.1 keeps.
const (
	// maxInput is the daemon's cap on input queued for the session's pty
	// (PTY_WRITE_BUF_MAX). A send that would take the queue past it is dropped
	// whole.
	maxInput = 256 << 10
	// sendChunk is the most text one `zmx send` delivers for certain. The
	// daemon reads a client's socket 4096 bytes per poll pass and closes the
	// client on the pass that sees it hang up, discarding what it has not
	// read. A send whose message, an 8-byte header and the text, fits in one
	// read always arrives. One larger than that is lost more often than not.
	sendChunk = 4096 - 8
)

// fitsInput refuses, as the caller's input, a write zmx would drop (§4.3).
func fitsInput(n int) error {
	if n > maxInput {
		return backend.Errorf(backend.CodeUsage,
			"zmx takes at most %d bytes of input at once, and this is %d", maxInput, n)
	}
	return nil
}

// send writes text to the session's input.
//
// The text goes on zmx's stdin, never on its argv, where every local user reads
// it in the process list and Linux refuses one argument past 128 KiB (§4.3).
// Given no text argument, `zmx send` reads stdin to its end when stdin is not a
// terminal (main.zig, send, from 0.6.0).
//
// A text longer than sendChunk goes as several sends, in order, each one a
// message the daemon reads whole. Each send finishes before the next starts,
// and the daemon handles a closed client's message before it answers the next
// client's probe, so the pieces reach the pty in the order sent. A piece that
// fails after an earlier one landed leaves that part of the text in the input
// line, and the failure says so (partlySent).
//
// Empty text is not sent: zmx refuses an empty send. Every caller has checked
// the target's presence first, which is all that is left of the operation
// (§4.1).
func (z *Zmx) send(ctx context.Context, target, text string) error {
	for sent := 0; sent < len(text); {
		n := min(len(text)-sent, sendChunk)
		if err := z.sendOnce(ctx, target, text[sent:sent+n]); err != nil {
			if sent == 0 {
				return err
			}
			return partlySent(target, sent, len(text), err)
		}
		sent += n
	}
	return nil
}

// partlySent is a send that failed after part of its text landed. It is marked
// typed, since a second send would type that part again (§4.3). A failure that
// carries its own code keeps it, as SubmitOnce's does in the engine, and an
// unexpected one becomes a timeout: the text is half delivered, and the caller
// has to look before acting.
func partlySent(target string, sent, total int, err error) error {
	code := backend.CodeOf(err)
	if code == backend.CodeUnexpected {
		code = backend.CodeTimeout
	}
	partial := backend.Wrapf(code, err,
		"%d of %d bytes reached %s before the write failed, and are still in its input line; read the screen before sending again",
		sent, total, target)
	partial.Typed = true
	return partial
}

// sendOnce is one `zmx send` of a piece of text. zmx drops one trailing newline
// from what it read on stdin, so a piece that ends in a newline gets one more
// for zmx to take back.
func (z *Zmx) sendOnce(ctx context.Context, target, piece string) error {
	if strings.HasSuffix(piece, "\n") {
		piece += "\n"
	}
	_, err := z.runInput(ctx, strings.NewReader(piece), "send", target)
	return err
}

// requirePresent turns an absent target into not-found before the write.
//
// zmx's send exits 0 for a session that does not exist, so without this every
// injection into a dead or mistyped target silently succeeds — and a caller
// waiting for output that can never arrive has no idea why.
func (z *Zmx) requirePresent(ctx context.Context, target string) error {
	switch z.Probe(ctx, target) {
	case backend.StatePresent:
		return nil
	case backend.StateAbsent:
		return backend.Errorf(backend.CodeSessionNotFound, "no session %s", target)
	default:
		return backend.Errorf(backend.CodeBackendUnavailable, "cannot reach zmx to address %s", target)
	}
}

func (z *Zmx) Screen(ctx context.Context, target string, opts backend.ScreenOpts) (backend.Capture, error) {
	if err := z.requirePresent(ctx, target); err != nil {
		return backend.Capture{}, err
	}

	args := []string{"history", target}
	if opts.Colors {
		// Not a no-op on zmx: default output has every ANSI escape byte
		// stripped, and this preserves them byte-for-byte (§5.2).
		args = append(args, "--vt")
	}
	// HistoryLines is deliberately ignored. zmx's history command already
	// returns full scrollback with no separate viewport mode to opt into, so
	// both states MUST return byte-identical output (§5.2). There is also no
	// -J equivalent: a line hitting the PTY's width comes back split by a
	// literal newline indistinguishable from a real one, and that is inherited
	// as-is rather than papered over.
	out, err := z.run(ctx, args...)
	if err != nil {
		return backend.Capture{}, err
	}
	// Metadata is always the zero value, with no subprocess run to check. That
	// is an honest answer ("not tracked"), not an unsupported-class error, so
	// the call succeeds with zeroes (§5.3).
	return backend.Capture{Text: out}, nil
}

// ScreenMeta is always the zero value here, with no subprocess run to check.
//
// That is an honest answer — "not tracked" — rather than an unsupported-class
// error: the caller asked a question this backend can answer negatively. The
// tracks_alt_screen capability is what tells a caller the zero is a genuine
// "no" rather than a real observation (behavior §5.3, §13).
func (z *Zmx) ScreenMeta(ctx context.Context, target string) (backend.ScreenMeta, error) {
	if err := z.requirePresent(ctx, target); err != nil {
		return backend.ScreenMeta{}, err
	}
	return backend.ScreenMeta{}, nil
}

func (z *Zmx) ServerEnv(ctx context.Context, key string) (string, bool, error) {
	return "", false, backend.Errorf(backend.CodeUnsupported,
		"zmx has no server environment: there is no shared server whose environment could be read")
}

// SetStatus is unsupported: zmx keeps no per-session metadata (§13.1).
func (z *Zmx) SetStatus(ctx context.Context, target, status string) error {
	return backend.Errorf(backend.CodeUnsupported, "zmx cannot carry a session status")
}

// Status is unsupported for the same reason. It refuses rather than returning
// empty, which would be indistinguishable from a session that has simply not
// reported yet.
func (z *Zmx) Status(ctx context.Context, target string) (string, error) {
	return "", backend.Errorf(backend.CodeUnsupported, "zmx cannot carry a session status")
}

func (z *Zmx) CreateView(ctx context.Context, base string, spec backend.ViewSpec) (backend.View, error) {
	return backend.View{}, backend.Errorf(backend.CodeUnsupported, "zmx has no views")
}

func (z *Zmx) ScrollView(ctx context.Context, view string, lines int) error {
	return backend.Errorf(backend.CodeUnsupported, "zmx has no views")
}

func (z *Zmx) FocusView(ctx context.Context, view string, col, row int) (string, error) {
	return "", backend.Errorf(backend.CodeUnsupported, "zmx has no views")
}

func (z *Zmx) Views(ctx context.Context, base string) ([]backend.View, error) {
	return nil, backend.Errorf(backend.CodeUnsupported, "zmx has no views")
}

// command builds a zmx invocation with the isolation and hygiene rules applied.
// waitDelay bounds how long a cancelled subprocess may keep a call waiting.
//
// Cancelling a context kills the CHILD, which is not enough to unblock the read:
// a grandchild inherits the same output pipe, and the copy waits on the pipe
// rather than on the process. Without this, a cancelled call can hang forever
// past its own deadline. See backend/meja for the case that proved it.
const waitDelay = 2 * time.Second

func (z *Zmx) command(ctx context.Context, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, "zmx", args...)
	cmd.Env = z.env(spawnEnv())
	cmd.WaitDelay = waitDelay
	return cmd
}

func (z *Zmx) env(base []string) []string {
	if z.dir == "" {
		return base
	}
	return append(base, "ZMX_DIR="+z.dir)
}

func (z *Zmx) run(ctx context.Context, args ...string) (string, error) {
	return z.runInput(ctx, nil, args...)
}

// runInput is run with stdin read from in.
func (z *Zmx) runInput(ctx context.Context, in io.Reader, args ...string) (string, error) {
	cmd := z.command(ctx, args...)
	cmd.Stdin = in
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return stdout.String(), classify(err, stderr.String()+stdout.String(), args)
	}
	return stdout.String(), nil
}

func classify(err error, output string, args []string) error {
	msg := strings.TrimSpace(output)
	lower := strings.ToLower(msg)

	var execErr *exec.Error
	if errors.As(err, &execErr) {
		return backend.Wrapf(backend.CodeBackendUnavailable, err, "zmx is not available")
	}
	if msg == "" {
		msg = err.Error()
	}

	switch {
	case strings.Contains(lower, "does not exist"), strings.Contains(lower, "no such session"):
		return backend.Errorf(backend.CodeSessionNotFound, "%s", msg)
	default:
		return backend.Wrapf(backend.CodeUnexpected, errors.New(msg), "zmx %s", args[0])
	}
}
