//go:build darwin || linux

package olympus

import (
	"context"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/husniadil/olympus/backend"
	"github.com/husniadil/olympus/internal/engine"
)

// An AttachOption configures Attach.
type AttachOption func(*backend.AttachSpec)

// AsViewer attaches read-only.
//
// On a backend with one shared PTY per session this drops resize as well as
// input: a viewer's resize physically resizes the driver's terminal, which is a
// real disruption rather than a self-contained no-op (behavior §8.7).
func AsViewer() AttachOption {
	return func(s *backend.AttachSpec) { s.Role = backend.RoleViewer }
}

// AttachSize sets the PTY's initial size, for a caller whose stdin is not a
// terminal and therefore carries no window to inherit one from.
func AttachSize(cols, rows int) AttachOption {
	return func(s *backend.AttachSpec) { s.Cols, s.Rows = cols, rows }
}

// KeepOtherClients opts out of displacing prior clients.
//
// Superseding is the default, mirroring what attaching to a multiplexer has
// always done; opting out is the explicit choice (behavior §8.4).
func KeepOtherClients() AttachOption {
	return func(s *backend.AttachSpec) { s.Supersede = false }
}

// WithSessionClient asks for the multiplexer's own session client — its own UI,
// with selection, scrollback and copy — rather than a raw per-pane stream.
//
// Only herdr distinguishes the two; the other backends always hand their
// session client, so this is a no-op there and Attach rejects it on a backend
// that has no separate session client. When set, the target names the backend's
// own session rather than an Olympus-resolved pane.
func WithSessionClient() AttachOption {
	return func(s *backend.AttachSpec) { s.SessionClient = true }
}

// AsBare asks for a plain pane with no chrome. What it means depends on the
// backend, and both meanings are decided here rather than in a door:
//
//   - herdr: the session client with its chrome hidden. It implies
//     WithSessionClient, since chrome is the session client's to hide, and the
//     target is a herdr SESSION name.
//   - tmux: a view (behavior §9) onto the session, attached instead of the
//     session itself and killed when the attach ends. A view is already bare by
//     construction — no status bar, no prefix (§9.3) — and a grouped session
//     keeps its own current window, so the target may be `<session>:<window>`
//     to show one window without moving anybody else's. The window is an index
//     or a name; `<session>` alone shows whatever the base is showing.
//
// zmx and meja have neither a chrome-drawing client nor views, so a bare attach
// is refused there as unsupported.
func AsBare() AttachOption {
	return func(s *backend.AttachSpec) { s.Bare = true }
}

// splitBareTarget separates `<session>:<window>` for a bare attach on tmux. The
// first colon is the split: tmux rewrites a colon out of any session name it is
// given (measured: `new-session -s a:b` creates `a_b`), so a session name never
// contains one, and the remainder is the window whole — a window NAME may
// carry a colon.
func splitBareTarget(target string) (session, window string) {
	session, window, _ = strings.Cut(target, ":")
	return session, window
}

// Attach hands the caller's terminal to a session until the client exits, and
// returns the CLIENT's exit code.
//
// Once the presence gate has passed this hands off, so the exit code follows
// the attach client's rather than Olympus's vocabulary: an attach exiting 3 is
// not necessarily not-found (behavior §12.1).
func (s *Session) Attach(ctx context.Context, in, out *os.File, errOut *os.File, opts ...AttachOption) (int, error) {
	spec := backend.AttachSpec{Role: backend.RoleController, Supersede: true, Cols: DefaultCols, Rows: DefaultRows}
	for _, opt := range opts {
		opt(&spec)
	}

	// Only herdr has a separate session client; on every other backend the
	// ordinary attach IS the session client, so asking for one there is a
	// caller mistake rather than a silent no-op.
	if spec.SessionClient && s.ol.Backend() != backend.Herdr {
		return 0, backend.Errorf(backend.CodeUnsupported,
			"the %s backend has no separate session client: its attach already hands you one", s.ol.Backend())
	}
	if spec.Bare {
		switch s.ol.Backend() {
		case backend.Herdr:
			spec.SessionClient = true
		case backend.Tmux:
			return s.attachBareView(ctx, in, out, errOut, spec)
		default:
			return 0, backend.Errorf(backend.CodeUnsupported,
				"the %s backend has no bare attach: it draws no chrome to hide and has no views", s.ol.Backend())
		}
	}

	// The supersession handler is installed BEFORE anything else, so a steal
	// landing in the window before the PTY exists has nothing to close rather
	// than racing a half-built terminal (behavior §8.6).
	superseded := make(chan struct{})
	stolen := make(chan os.Signal, 1)
	signal.Notify(stolen, syscall.SIGUSR1)
	defer signal.Stop(stolen)
	go func() {
		if _, ok := <-stolen; ok {
			close(superseded)
		}
	}()

	// The attach guard arbitrates Olympus against Olympus, and is deliberately
	// NOT the write lock: stealing a terminal and serializing writes are
	// different contention problems, and a caller waiting to write must not be
	// able to displace someone's live terminal (behavior §11.3).
	//
	// It applies where the backend co-attaches rather than displacing on its
	// own. A backend whose own client detaches the previous one needs no
	// pidfile.
	if s.ol.Backend() == backend.Zmx {
		guard, err := engine.NewAttachGuard(filepath.Join(os.TempDir(), engine.LockDirName))
		if err != nil {
			return 0, err
		}
		slot, err := guard.Acquire(ctx, s.key(), spec.Supersede, engine.DefaultStealWait, engine.DefaultStealPoll)
		if err != nil {
			return 0, err
		}
		defer slot.Release()
	}

	attachment, err := s.ol.backend.Attach(ctx, s.name, spec)
	if err != nil {
		return 0, err
	}
	return s.runAttach(ctx, attachment, in, out, errOut, spec, superseded)
}

// attachBareView attaches a fresh view onto the session instead of the session
// itself, pinned to a window when the target names one (behavior §8.9).
//
// The view is created and reaped here, above the backend, for the same reason
// the view's name is: the reserved shape of §17.1 lives in one place, and the
// backend's Attach stays the one argv it always was. The reaping rides on the
// attachment's Cleanup, which the engine runs on every exit path (§8.8) — a
// spontaneous client exit must not leave the view behind.
func (s *Session) attachBareView(ctx context.Context, in, out, errOut *os.File, spec backend.AttachSpec) (int, error) {
	attachment, err := s.prepareBareView(ctx, spec)
	if err != nil {
		return 0, err
	}
	// No supersession handler: nobody else can hold a client on a view that
	// did not exist a moment ago, and the pane's own controller is not
	// displaced by a bare attach — that is its point.
	return s.runAttach(ctx, attachment, in, out, errOut, spec, nil)
}

// prepareBareView builds the attachment for a bare attach: a view onto the
// session, pinned to the window the target names, with the view's removal as
// the attachment's cleanup. Separated from running it so the shape can be
// asserted without a terminal.
func (s *Session) prepareBareView(ctx context.Context, spec backend.AttachSpec) (backend.Attachment, error) {
	session, window := splitBareTarget(s.name)
	base, err := s.ol.resolveTarget(ctx, session)
	if err != nil {
		return backend.Attachment{}, err
	}
	// Mouse on, as CreateView defaults it: a bare attach is for looking, and
	// a wheel that scrolls is the point of a view.
	view, err := s.ol.backend.CreateView(ctx, base, backend.ViewSpec{Name: viewName(base), Mouse: true, Window: window})
	if err != nil {
		return backend.Attachment{}, err
	}
	reap := func() error {
		return s.ol.backend.Kill(context.WithoutCancel(ctx), view.Name)
	}

	// The backend sees an ordinary attach onto the view. Bare has been spent;
	// leaving it set would ask a backend that ignores it to ignore it.
	spec.Bare = false
	attachment, err := s.ol.backend.Attach(ctx, view.Name, spec)
	if err != nil {
		_ = reap()
		return backend.Attachment{}, err
	}
	inner := attachment.Cleanup
	attachment.Cleanup = func() error {
		err := reap()
		if inner != nil {
			if innerErr := inner(); err == nil {
				err = innerErr
			}
		}
		return err
	}
	return attachment, nil
}

func (s *Session) runAttach(ctx context.Context, attachment backend.Attachment, in, out, errOut *os.File, spec backend.AttachSpec, superseded <-chan struct{}) (int, error) {
	for _, notice := range attachment.Notices {
		if errOut != nil {
			_, _ = errOut.WriteString("olympus: " + notice + "\n")
		}
	}
	return engine.Attach(ctx, attachment, engine.AttachIO{In: in, Out: out, Err: errOut}, spec, superseded)
}
