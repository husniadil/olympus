package engine

import (
	"context"

	"github.com/husniadil/olympus/backend"
)

// Ensure makes a named session exist and be alive, reporting which of three
// things happened (behavior §2.6).
//
// Ensure itself does NO locking. The caller holds the per-session write lock,
// and that is what turns two concurrent ensures of one name into a
// deterministic outcome instead of a race: with locking disabled both can
// observe "absent" and both create, and the loser's outcome is backend-defined.
func Ensure(ctx context.Context, b backend.Backend, spec backend.CreateSpec) (backend.Session, error) {
	// Rejected BEFORE branching on session state, not only inside create.
	// Otherwise the contract becomes state-dependent: a fresh name correctly
	// rejects via the create path, but an already-alive session takes the
	// reuse branch, never reaches create, and silently accepts and ignores the
	// flag (behavior §2.7).
	if spec.RemainOnExit && !b.Capabilities().RemainOnExit {
		return backend.Session{}, backend.Errorf(backend.CodeUnsupported,
			"the %s backend has no remain-on-exit", b.Capabilities().Backend)
	}

	existing, found, err := findSession(ctx, b, spec.Name)
	if err != nil {
		return backend.Session{}, err
	}

	switch {
	case found && !existing.Dead:
		// Options other than the name are ignored, and are NOT applied
		// retroactively: they belong to the create path (§2.7).
		existing.Outcome = backend.OutcomeReused
		return existing, nil

	case found && existing.Dead:
		// Present but dead: reap, then recreate with the given options.
		//
		// This branch is unreachable on both shipped backends — a session
		// created without a corpse flag takes its session with it, and a
		// backend that auto-reaps leaves nothing to find — so a finished
		// session is indistinguishable from an absent one and yields
		// "created". It is implemented anyway so a backend that does leave
		// dead rows behaves correctly rather than reusing a corpse.
		if err := b.Kill(ctx, spec.Name); err != nil && !isAlreadyGone(err) {
			return backend.Session{}, err
		}
		row, err := b.Create(ctx, spec)
		if err != nil {
			return backend.Session{}, err
		}
		row.Outcome = backend.OutcomeReaped
		return row, nil

	default:
		row, err := b.Create(ctx, spec)
		if err != nil {
			return backend.Session{}, err
		}
		row.Outcome = backend.OutcomeCreated
		return row, nil
	}
}

func findSession(ctx context.Context, b backend.Backend, name string) (backend.Session, bool, error) {
	sessions, err := b.Sessions(ctx)
	if err != nil {
		return backend.Session{}, false, err
	}
	for _, s := range sessions {
		if s.Name == name {
			return s, true, nil
		}
	}
	return backend.Session{}, false, nil
}
