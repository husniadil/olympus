// Package meja drives the meja multiplexer.
//
// meja differs from the other backends in how driving reaches a pane, and that
// is where everything unusual here comes from. Through 0.0.25 every INPUT
// command is routed through an attached client and refuses outright when a
// session has none; from 0.0.26 ordinary input goes straight to the pane and
// only copy mode still refuses. Observation — listing, capture — works
// headlessly on both. Both versions are supported, so the injection path
// attempts the operation and attaches a client only if it is refused. See
// §2.10.
package meja

import (
	"context"
	"errors"
	"io"
	"os/exec"
	"strings"
	"time"

	"github.com/husniadil/olympus/backend"
)

// A Meja drives one meja server.
type Meja struct {
	socketPath string
}

// An Option configures New.
type Option func(*Meja)

// WithSocketPath selects the server socket by path, used verbatim.
//
// Only the path form is offered, deliberately. meja's other form, -L <profile>,
// resolves under ~/.meja and keeps session RECOVERY FILES beside the socket, so
// a profile Olympus drove would leave persisted sessions in the operator's own
// store to come back on their next restore. A path Olympus is given keeps both
// the socket and that store where the caller put them.
func WithSocketPath(path string) Option {
	return func(m *Meja) { m.socketPath = path }
}

// New builds a backend. With no socket path it addresses meja's default
// profile, which is the operator's own.
func New(opts ...Option) *Meja {
	m := &Meja{}
	for _, opt := range opts {
		opt(m)
	}
	return m
}

// Scope reports how this backend addresses its server.
func (m *Meja) Scope() string {
	if m.socketPath != "" {
		return m.socketPath
	}
	return "default"
}

func (m *Meja) addressing() []string {
	if m.socketPath != "" {
		return []string{"-S", m.socketPath}
	}
	return nil
}

// waitDelay bounds how long a cancelled subprocess may keep this call waiting.
//
// Cancelling a context kills the CHILD, and that is not enough to unblock the
// read: any grandchild it left behind inherited the same output pipe, and the
// copy goroutine waits on the pipe rather than on the process. A `version` probe
// was observed hanging indefinitely past its own 10s deadline for exactly this
// reason — the binary on PATH was a version-manager shim which, when HOME moved
// underneath it, spawned git subprocesses that outlived the kill.
//
// WaitDelay closes the descriptors once the deadline has passed, converting an
// unbounded hang into a normal error. Long enough that a healthy child which is
// merely slow to flush still gets to.
const waitDelay = 2 * time.Second

// command builds a meja invocation with the addressing and hygiene rules
// applied (§1.1).
func (m *Meja) command(ctx context.Context, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, "meja", append(m.addressing(), args...)...)
	cmd.Env = spawnEnv()
	cmd.WaitDelay = waitDelay
	return cmd
}

// run invokes the meja client and maps its failure into the error vocabulary.
func (m *Meja) run(ctx context.Context, stdin io.Reader, args ...string) (string, error) {
	cmd := m.command(ctx, args...)
	cmd.Stdin = stdin
	out, err := cmd.CombinedOutput()
	text := string(out)
	if err != nil {
		return text, classify(text, err)
	}
	return text, nil
}

// classify maps meja's messages onto the shared vocabulary.
//
// meja spells each condition exactly one way, on every subcommand — unlike
// tmux, which says "no server running" on one verb and "error connecting to" on
// the rest. So there is one string to match per condition rather than a family.
func classify(text string, err error) error {
	// The binary itself missing or not executable. Reached only if it vanishes
	// between the preflight and the call, which is why it is defence rather
	// than the main path — but the classification still has to be right, since
	// UNEXPECTED tells a caller retrying will not help when in fact fixing
	// their PATH will (§12).
	var execErr *exec.Error
	if errors.As(err, &execErr) {
		return backend.Wrapf(backend.CodeBackendUnavailable, err, "meja is not available")
	}

	lower := strings.ToLower(text)
	switch {
	case strings.Contains(lower, "server unavailable"):
		return backend.Wrapf(backend.CodeBackendUnavailable, err, "%s", trim(text))
	case strings.Contains(lower, "unknown session"):
		return backend.Wrapf(backend.CodeSessionNotFound, err, "%s", trim(text))
	case strings.Contains(lower, "requires an attached client"):
		// Not a caller error and not a backend outage: it means this operation
		// needed a client and the transient one failed to arrive. §2.10.
		return backend.Wrapf(backend.CodeBackendUnavailable, err, "%s", trim(text))
	case strings.Contains(lower, "unknown command"), strings.Contains(lower, "unsupported"):
		return backend.Wrapf(backend.CodeUnsupported, err, "%s", trim(text))
	case strings.Contains(lower, " must be "), strings.Contains(lower, " must not "),
		strings.Contains(lower, "requires a "):
		// meja's input-validation family, and it is one family: every rejection
		// it spells this way is a caller's argument it will not take — a
		// session name that is entirely numeric, a resize amount that is not
		// positive, a buffer name with a newline in it.
		//
		// USAGE and not UNEXPECTED because the two say opposite things to a
		// program. UNEXPECTED means retrying will not help and nothing the
		// caller controls is at fault; here one corrected argument fixes it,
		// which is exactly what §0.1 reserves usage-class for. It matters most
		// where backends disagree: a name meja refuses is one tmux and zmx
		// accept, so the caller really is being told about their input.
		return backend.Wrapf(backend.CodeUsage, err, "%s", trim(text))
	}
	return backend.Wrapf(backend.CodeUnexpected, err, "%s", trim(text))
}

func trim(text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return "meja failed without a message"
	}
	if i := strings.IndexByte(text, '\n'); i >= 0 {
		text = text[:i]
	}
	return strings.TrimPrefix(text, "meja: ")
}

// named restates an error against the target it was about.
//
// It also resolves an absent SERVER into an absent SESSION. §12.3 requires it:
// a target-addressed operation must answer not-found naming its own target,
// because meja reports the condition in its own vocabulary — a socket path — and
// a caller holding a session name can match nothing against that. The collapse
// is sound rather than convenient: when the socket is gone, every session on it
// is gone with it, which is a definite answer and not a doubtful one.
//
// The attached-client message is deliberately NOT collapsed. It shares a code
// with an absent server but means something else entirely — the session is
// there, and the client we needed was not — so treating it as absence would
// report a live session as missing.
func named(target string, err error) error {
	if err == nil {
		return nil
	}
	switch {
	case backend.CodeOf(err) == backend.CodeSessionNotFound:
		return backend.Wrapf(backend.CodeSessionNotFound, err, "no session %s", target)
	case noServer(err):
		return backend.Wrapf(backend.CodeSessionNotFound, err, "no session %s", target)
	}
	return err
}

// noServer reports whether nothing is listening on the socket.
func noServer(err error) bool {
	return err != nil && strings.Contains(strings.ToLower(err.Error()), "server unavailable")
}

// Version reports the server's version.
func (m *Meja) Version(ctx context.Context) (string, error) {
	cmd := exec.CommandContext(ctx, "meja", "version")
	cmd.WaitDelay = waitDelay
	out, err := cmd.Output()
	if err != nil {
		return "", backend.Wrapf(backend.CodeBackendUnavailable, err, "meja is not runnable")
	}
	return strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(string(out)), "meja ")), nil
}

// Capabilities reports what this backend can do, measured rather than assumed.
func (m *Meja) Capabilities() backend.Capabilities {
	return backend.Capabilities{
		Backend: backend.Meja,
		// capture-pane takes -S/-E like tmux's, so history is a flag on the
		// capture rather than something the backend hands back natively.
		NativeScrollback: false,
		// meja has grouped sessions (new -d -t base -s mirror), but no
		// read-only posture, no key table to bind scrolling into, and no
		// set-option to make a view passive with. A view that a stray keypress
		// could drive is not the read-only window §9 specifies.
		Views: false,
		// No corpse concept: a pane whose command exits is closed.
		RemainOnExit: false,
		// No set-environment/show-environment commands at all.
		ServerEnv: false,
		// send-keys accepts tmux's spelling, C-a through C-z included, and
		// routes them through the attached client that types them. Measured
		// against `cat -v` rather than inferred from that: C-a, C-x, C-l,
		// escape, tab, the arrows and the function keys all arrive.
		ControlKeys: true,
		// new-session takes no -x/-y: meja sizes a session from its first
		// client, so a detached one takes a default (§2.10).
		SpawnSizing: false,
		// new-session takes the argv and execs it, exactly as tmux does.
		SpawnCommand: true,
		// No option store of any kind — no set-option, no show-options — so
		// there is nowhere a status could outlive the process that set it.
		SessionStatus: false,
		// No format field reports the alternate screen, so the flag would
		// always be false and a caller could not tell that from "not tracked".
		TracksAltScreen: false,
		// meja's other servers are profiles (`-L <name>`) resolved under its
		// own store, and nothing in its CLI enumerates them, so a server here
		// is addressed by knowing its socket path and by nothing else (§13.2).
		Servers: false,
		// One client and no views: nothing to hide and nothing to attach bare.
		SessionClient: false,
		Bare:          false,
		// One pane per session: nothing to bring to the front.
		Focus:  false,
		Rename: false,
	}
}
