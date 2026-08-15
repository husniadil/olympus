package backend

import (
	"context"
	"os/exec"
)

// A Key names a keypress. The vocabulary is Olympus's own and each backend
// translates it, so a caller never has to know one multiplexer's spelling to
// press a key on another. A key outside the vocabulary is CodeUsage: it is
// input the caller could have validated.
type Key string

const (
	KeyEnter     Key = "enter"
	KeyEscape    Key = "escape"
	KeyTab       Key = "tab"
	KeyBackspace Key = "backspace"
	KeySpace     Key = "space"
	KeyUp        Key = "up"
	KeyDown      Key = "down"
	KeyLeft      Key = "left"
	KeyRight     Key = "right"
	KeyHome      Key = "home"
	KeyEnd       Key = "end"
	KeyPageUp    Key = "page-up"
	KeyPageDown  Key = "page-down"
	KeyCtrlA     Key = "c-a"
	KeyCtrlC     Key = "c-c"
	KeyCtrlD     Key = "c-d"
	KeyCtrlE     Key = "c-e"
	KeyCtrlL     Key = "c-l"
	KeyCtrlU     Key = "c-u"
	KeyCtrlZ     Key = "c-z"
)

// A CreateSpec is a complete session creation request. Every field is explicit:
// the mechanical layer never fills a blank, because defaults are decided once,
// in the ergonomic layer (behavior §17.3). A backend receiving a zero Cols has
// been handed a bug, not a request to choose a width.
type CreateSpec struct {
	Name string
	// Dir is the session's starting directory.
	Dir string
	// Command is the argv to spawn. Empty means a plain login shell. It is
	// spawned by exec, never typed into a shell (behavior §2.3).
	Command []string
	Cols    int
	Rows    int
	// Env is the spawn environment, already filtered for the hygiene rules of
	// behavior §1.1. The backend applies it; it does not curate it.
	Env map[string]string
	// RemainOnExit keeps a corpse to inspect after the session's command
	// exits. It is write-only and applies on the create path only: there is no
	// way to read it off a live session and no way to change one (behavior
	// §2.7). A backend with no corpse concept rejects it as unsupported before
	// invoking anything.
	RemainOnExit bool
}

// ScreenOpts selects what a capture includes. Both are off by default at the
// mechanical layer, matching the cheapest capture.
type ScreenOpts struct {
	// Colors keeps escape sequences in the captured text.
	Colors bool
	// HistoryLines is how many lines of scrollback to include above the
	// visible screen. Zero is the visible screen only.
	HistoryLines int
}

// A Capture is one target's screen and the metadata the text itself cannot
// carry. Empty Text with Meta.AltScreen set means skipped by design, not
// nothing there (behavior §5.3).
type Capture struct {
	Text string
	Meta ScreenMeta
}

// A Role is what an attached client may do (behavior §8.7).
type Role string

const (
	// RoleController may send input and may resize.
	RoleController Role = "controller"
	// RoleViewer may do neither. Dropping input without also dropping resize
	// would let a passive watcher reshape everyone else's terminal.
	RoleViewer Role = "viewer"
)

// An AttachSpec is a complete attach request.
type AttachSpec struct {
	Role Role
	// Supersede displaces any prior client. The backend performs its own
	// supersession mechanism before returning the command — on some backends
	// that needs both a guard and a sweep (behavior §8.5).
	Supersede bool
	Cols      int
	Rows      int
}

// An Attachment is what to run in order to be attached, not something already
// running. The PTY, the signal handling and the terminal restore of behavior
// §8.2 belong to one shared engine; a backend contributes only the client
// command and whatever teardown that client leaves behind.
type Attachment struct {
	// Cmd is executed inside the PTY the engine owns.
	Cmd *exec.Cmd
	// Cleanup releases backend-side state the attach created, such as a view
	// session to reap. It may be nil.
	Cleanup func() error
	// Notices are things the operator must be told about how this attach was
	// set up, even though it succeeded — a supersession sweep that failed, for
	// instance, which leaves prior clients attached. They go to the narration
	// channel: a silent partial failure here is indistinguishable from a clean
	// one, which is the precise thing worth reporting (behavior §8.5).
	Notices []string
}

// Close runs the cleanup, if there is one. It is safe on the zero Attachment so
// the engine can defer it unconditionally — behavior §8.8 requires a
// spontaneous attach exit to reap too, and a cleanup that only runs on the
// tidy path is the way that requirement gets lost.
func (a Attachment) Close() error {
	if a.Cleanup == nil {
		return nil
	}
	return a.Cleanup()
}

// A View is a grouped, independently-scrollable window onto an existing
// session (behavior §9). Its lifetime is independent of the base's, but the
// window and pane are shared.
type View struct {
	Name     string `json:"name"`
	Base     string `json:"base"`
	ID       string `json:"id"`
	Attached bool   `json:"attached"`
}

// A Backend drives one multiplexer. It is the mechanical layer: explicit,
// complete, and free of defaults. Every rule it must satisfy is specified in
// docs/terminal-behavior.md, and every rule that can be observed through this
// interface is enforced by the conformance suite in backend/backendtest, which
// is exported so a third-party backend can prove itself against the same one
// the shipped backends run.
//
// Targets arriving here are already resolved (ResolveTarget). Operations that a
// backend has no concept for return CodeUnsupported — distinct from
// CodeBackendUnavailable, and distinct from a real negative answer — and a
// consumer is expected to branch on Capabilities rather than on that error.
type Backend interface {
	// Capabilities are static facts, so this takes no context and starts no
	// subprocess (behavior §13).
	Capabilities() Capabilities
	// Version reports the multiplexer's version, for the floors of §0.5 and
	// the diagnostic of §0.6. A backend that is not installed reports
	// CodeBackendUnavailable.
	Version(ctx context.Context) (string, error)

	// Create makes a new session. It is not ensure-semantics: deciding to
	// reuse or reap an existing one is shared logic above this interface
	// (behavior §2.6).
	Create(ctx context.Context, spec CreateSpec) (Session, error)
	// Sessions lists every session. No server running is an empty list, not an
	// error (behavior §3.3).
	Sessions(ctx context.Context) ([]Session, error)
	// Panes lists panes. An empty target lists every pane on the server, which
	// is what target resolution consumes.
	Panes(ctx context.Context, target string) ([]Pane, error)
	// Probe answers presence. It returns no error by design: a backend that
	// cannot be reached is StateError, so the tri-state of behavior §3.5
	// cannot collapse into a transport failure at the call site.
	Probe(ctx context.Context, target string) State
	// Kill ends a session immediately.
	Kill(ctx context.Context, target string) error
	// Interrupt asks the foreground process to stop. The graceful-kill
	// sequence that decides when to escalate to Kill is shared logic above
	// this interface (behavior §2.8); the mechanism is backend-specific and
	// lives here.
	Interrupt(ctx context.Context, target string) error

	// Type injects literal text. It never submits, on any backend
	// (behavior §4.3).
	Type(ctx context.Context, target, text string) error
	// Paste injects multi-line text. It never submits either (behavior §4.6);
	// normalization happens above this interface.
	Paste(ctx context.Context, target, text string) error
	// Press sends named keys.
	Press(ctx context.Context, target string, keys ...Key) error
	// Submit writes the terminator alone, as a keypress rather than as part of
	// a paste. The pacing that separates it from the preceding text is a
	// default, so it is applied above this interface (behavior §4.5).
	Submit(ctx context.Context, target string) error
	// SendAtomic writes text and its terminator indivisibly, trading the
	// verification of §7 for atomicity (behavior §4.7).
	SendAtomic(ctx context.Context, target, text string) error

	// Screen captures one target.
	Screen(ctx context.Context, target string, opts ScreenOpts) (Capture, error)

	// Attach prepares a client for the engine to run inside a PTY.
	Attach(ctx context.Context, target string, spec AttachSpec) (Attachment, error)

	// CreateView adds a view onto base under the given name. The name is
	// supplied rather than generated here so the reserved-name format of
	// behavior §17.1 lives in one place.
	CreateView(ctx context.Context, base, name string) (View, error)
	// ScrollView scrolls a view by a number of lines, negative for back into
	// history.
	ScrollView(ctx context.Context, view string, lines int) error
	// Views lists the views onto a base session, or onto every session when
	// base is empty.
	Views(ctx context.Context, base string) ([]View, error)

	// ServerEnv reads a key from the multiplexer server's global environment.
	// An unset key is a real negative answer — present false, no error — and
	// is not the same as a backend with no such concept, which is
	// CodeUnsupported (behavior §12).
	ServerEnv(ctx context.Context, key string) (value string, present bool, err error)
}
