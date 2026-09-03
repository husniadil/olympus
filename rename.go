package olympus

import (
	"context"

	"github.com/husniadil/olympus/backend"
)

// Rename gives a target a new name in place (behavior §2.11): a session, or
// a window, tab or pane where the backend names those too.
//
// The target reaches the backend AS GIVEN, as Focus does: the point is the
// level below the session that §10.1's resolution would discard. Presence is
// gated through the resolved session first, so a target naming nothing is
// not-found here. A backend whose names are fixed at creation answers
// unsupported; feature-probe Capabilities.Rename.
func (o *Olympus) Rename(ctx context.Context, target, name string) error {
	r, ok := o.backend.(backend.Renamer)
	if !ok || !o.backend.Capabilities().Rename {
		return backend.Errorf(backend.CodeUnsupported,
			"%s cannot rename: a name there is fixed when the session is created", o.Backend())
	}
	if name == "" {
		return backend.Errorf(backend.CodeUsage, "a name is needed to rename to")
	}
	resolved, err := o.resolveTarget(ctx, target)
	if err != nil {
		return err
	}
	if state := o.backend.Probe(ctx, resolved); state != backend.StatePresent {
		if state == backend.StateAbsent {
			return backend.Errorf(backend.CodeSessionNotFound, "no session %s", target)
		}
		return backend.Errorf(backend.CodeBackendUnavailable, "cannot reach the backend to address %s", target)
	}
	return r.Rename(ctx, target, name)
}
