package olympus

import (
	"context"
	"os"
	"strings"
	"time"

	"github.com/husniadil/olympus/backend"
	"github.com/husniadil/olympus/backend/tmux"
	"github.com/husniadil/olympus/backend/zmx"
	"github.com/husniadil/olympus/internal/engine"
)

// The typed errors of api §3, re-exported so a caller never has to import the
// mechanical layer just to branch on a failure.
var (
	ErrUsage       = backend.ErrUsage
	ErrNotFound    = backend.ErrNotFound
	ErrUnavailable = backend.ErrUnavailable
	ErrTimeout     = backend.ErrTimeout
	ErrConflict    = backend.ErrConflict
	ErrUnsupported = backend.ErrUnsupported
)

// CodeOf classifies any error into the semver-bound vocabulary of §12.
func CodeOf(err error) backend.Code { return backend.CodeOf(err) }

// ExitCode maps a code to its process exit status.
func ExitCode(code backend.Code) int { return backend.ExitCode(code) }

// An Olympus is a resolved backend plus the decisions made once around it.
type Olympus struct {
	backend    backend.Backend
	resolution Resolution
	scope      string
	locks      *engine.Locks
	lockWait   time.Duration
}

type config struct {
	explicit string
	socket   string
	zmxDir   string
	noLock   bool
	lockWait time.Duration
	installs installedFunc
	env      string
}

// An Option configures Open.
type Option func(*config)

// WithBackend selects a backend explicitly. An explicit choice never falls back
// (behavior §0.3).
func WithBackend(name string) Option {
	return func(c *config) { c.explicit = name }
}

// WithSocket selects the tmux socket. It has no meaning on other backends.
func WithSocket(name string) Option {
	return func(c *config) { c.socket = name }
}

// WithZmxDir selects the zmx socket directory. It has no meaning on other
// backends.
func WithZmxDir(dir string) Option {
	return func(c *config) { c.zmxDir = dir }
}

// WithoutLock disables the per-session write lock, for a caller that already
// serializes its own writes.
//
// This must be an explicit choice. With it, two concurrent ensures of one name
// can both observe "absent" and both create, and the loser's outcome is
// backend-defined (behavior §2.6).
func WithoutLock() Option {
	return func(c *config) { c.noLock = true }
}

// WithLockWait sets how long a writer waits for a contended session.
func WithLockWait(d time.Duration) Option {
	return func(c *config) { c.lockWait = d }
}

// Open resolves a backend, proves it exists, and returns a handle.
//
// The preflight happens HERE rather than at the first operation, so a missing
// backend fails with an actionable error at the point the caller can still do
// something about it (behavior §0.2).
func Open(opts ...Option) (*Olympus, error) {
	cfg := config{
		lockWait: lockWaitDefault(),
		installs: onPath,
		env:      os.Getenv(BackendEnv),
	}
	for _, opt := range opts {
		opt(&cfg)
	}

	resolution, err := resolve(cfg.explicit, cfg.env, cfg.installs)
	if err != nil {
		return nil, err
	}

	o := &Olympus{resolution: resolution, lockWait: cfg.lockWait}
	switch resolution.Backend {
	case backend.Tmux:
		socket := cfg.socket
		if socket == "" {
			socket = tmux.DefaultSocket
		}
		o.backend = tmux.New(tmux.WithSocket(socket))
		o.scope = socket
	case backend.Zmx:
		var options []zmx.Option
		if cfg.zmxDir != "" {
			options = append(options, zmx.WithDir(cfg.zmxDir))
		}
		o.backend = zmx.New(options...)
		o.scope = cfg.zmxDir
	}

	if !cfg.noLock {
		locks, err := engine.NewLocks()
		if err != nil {
			return nil, err
		}
		o.locks = locks
	}
	return o, nil
}

func lockWaitDefault() time.Duration {
	// Read at call time, never cached at process start, so an operator can
	// raise it without restarting anything.
	if v := strings.TrimSpace(os.Getenv(LockWaitEnv)); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			return d
		}
	}
	return DefaultLockWait
}

// Close releases what Open acquired. There is no daemon and no persistent
// state, so this exists for symmetry and for future-proofing rather than
// because anything is currently held open.
func (o *Olympus) Close() error { return nil }

// Backend reports the RESOLVED backend, which is what every door must disclose.
func (o *Olympus) Backend() backend.Name { return o.resolution.Backend }

// Resolution reports the resolved backend and the rule that chose it.
func (o *Olympus) Resolution() Resolution { return o.resolution }

// Capabilities reports the resolved backend's static facts, so a caller can
// feature-probe rather than branch on an unsupported error.
func (o *Olympus) Capabilities() backend.Capabilities { return o.backend.Capabilities() }

// Raw exposes the mechanical backend, for a caller that needs an operation this
// layer does not wrap. Everything this layer decides — defaults, locking,
// resolution — is bypassed by going through it.
func (o *Olympus) Raw() backend.Backend { return o.backend }

// Sessions lists every session on the resolved backend.
//
// Sessions are backend-scoped: this never includes sessions on another backend,
// because they are genuinely different sessions that cannot be addressed from
// here (behavior §0.4).
func (o *Olympus) Sessions(ctx context.Context) ([]backend.Session, error) {
	return o.backend.Sessions(ctx)
}

// resolveTarget swaps a pane id for its owning session, in the one shared place
// every operation calls (behavior §10).
func (o *Olympus) resolveTarget(ctx context.Context, target string) (string, error) {
	if o.backend.Capabilities().Backend == backend.Zmx {
		// No pane-id concept here, so nothing to resolve: a "%"-prefixed
		// target is simply an unknown session name under the ordinary lookup.
		return backend.ResolveTarget(target, nil)
	}
	return backend.ResolveTarget(target, func() ([]backend.Pane, error) {
		return o.backend.Panes(ctx, "")
	})
}

func (o *Olympus) lockKey(session string) engine.LockKey {
	return engine.LockKey{Backend: o.resolution.Backend, Scope: o.scope, Session: session}
}

// An Info is a session's detail, and answers presence as a tri-state.
type Info struct {
	// State is present, absent, or error. Absent is a real answer, not a
	// failure: collapsing it into an error would destroy the distinction a
	// reconciling caller needs between "definitely gone" and "could not ask"
	// (behavior §3.5).
	State backend.State `json:"state"`
	// Session and Panes are omitted when the target is not present.
	Session      *backend.Session     `json:"session,omitempty"`
	Panes        []backend.Pane       `json:"panes,omitempty"`
	Capabilities backend.Capabilities `json:"capabilities"`
	Warnings     []Warning            `json:"-"`
}

// Info reports a session's detail. It MUST NOT error on an absent target: this
// is the only door onto the presence tri-state, so it has to preserve it.
func (o *Olympus) Info(ctx context.Context, target string) (Info, error) {
	info := Info{Capabilities: o.backend.Capabilities()}

	resolved, err := o.resolveTarget(ctx, target)
	if err != nil {
		if CodeOf(err) == backend.CodeSessionNotFound {
			info.State = backend.StateAbsent
			return info, nil
		}
		return info, err
	}

	info.State = o.backend.Probe(ctx, resolved)
	if info.State != backend.StatePresent {
		return info, nil
	}

	// A session can go away between the probe and these reads — most visibly
	// just after a kill, where a listing stays eventually consistent for a
	// moment (§3.3). Surfacing that as an error would break the one rule this
	// door exists for: it MUST NOT error on an absent target. So a target that
	// vanishes underneath us reports absent, which is the truth by the time
	// the caller reads it.
	sessions, err := o.backend.Sessions(ctx)
	if err != nil {
		return info, err
	}
	for i := range sessions {
		if sessions[i].Name == resolved {
			info.Session = &sessions[i]
			break
		}
	}
	if info.Session == nil {
		info.State = backend.StateAbsent
		return info, nil
	}

	panes, err := o.backend.Panes(ctx, resolved)
	if err != nil {
		if CodeOf(err) == backend.CodeSessionNotFound {
			info.State = backend.StateAbsent
			info.Session = nil
			return info, nil
		}
		return info, err
	}
	info.Panes = panes
	info.Warnings = warn(o.resolution.Backend, opPaneListing)
	return info, nil
}
