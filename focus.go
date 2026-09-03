package olympus

import (
	"context"

	"github.com/husniadil/olympus/backend"
)

// Focus steers the server's focus onto a target: the workspace, tab or pane
// its clients show (behavior §8.10).
//
// It exists for a caller holding several clients onto several targets of
// one server. Each client shows the server's ONE focus, so the last attach
// steered wins for all of them, and bringing a client to the front means
// steering the server again — which is this, without attaching anything.
//
// The target reaches the backend AS GIVEN, not resolved to its session: the
// point is precision below the session — a window, a pane — which §10.1's
// session-scoped resolution would discard. Presence is still gated through
// the resolved session, so a target that names nothing is not-found here
// rather than whatever the backend prints. A backend whose clients each
// select their own view has nothing to steer and answers unsupported;
// feature-probe Capabilities.Focus.
func (o *Olympus) Focus(ctx context.Context, target string) error {
	f, ok := o.backend.(backend.Focuser)
	if !ok || !o.backend.Capabilities().Focus {
		return backend.Errorf(backend.CodeUnsupported,
			"%s has no server-side focus to steer: its clients show what each of them selected", o.Backend())
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
	return f.Focus(ctx, target)
}

// Focus steers the server onto this handle's target. A handle opened from a
// pane id on tmux addresses the owning session (§10.1), so a pane-precise
// focus there goes through Olympus.Focus with the id itself.
func (s *Session) Focus(ctx context.Context) error {
	return s.ol.Focus(ctx, s.name)
}
