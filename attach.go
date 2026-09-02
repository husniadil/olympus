//go:build darwin || linux

package olympus

import (
	"context"
	"os"
	"os/signal"
	"path/filepath"
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

// AsBare asks for a chrome-hidden session client, rendered as a plain pane. It
// implies WithSessionClient, since chrome is the session client's to hide.
func AsBare() AttachOption {
	return func(s *backend.AttachSpec) { s.SessionClient, s.Bare = true, true }
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
	for _, notice := range attachment.Notices {
		if errOut != nil {
			_, _ = errOut.WriteString("olympus: " + notice + "\n")
		}
	}

	return engine.Attach(ctx, attachment, engine.AttachIO{In: in, Out: out, Err: errOut}, spec, superseded)
}
