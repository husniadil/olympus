package meja

import (
	"context"
	"os/exec"

	"github.com/husniadil/olympus/backend"
)

// Attach prepares an attach client for the engine to run inside a PTY.
//
// The command is returned rather than run: the PTY, the signal handling and the
// terminal restore of §8.2 belong to one shared engine, not to each backend.
func (m *Meja) Attach(ctx context.Context, target string, spec backend.AttachSpec) (backend.Attachment, error) {
	// Checked here so an attach onto nothing fails as not-found rather than as
	// whatever the client happens to print.
	if state := m.Probe(ctx, target); state != backend.StatePresent {
		if state == backend.StateAbsent {
			return backend.Attachment{}, backend.Errorf(backend.CodeSessionNotFound, "no session %s", target)
		}
		return backend.Attachment{}, backend.Errorf(backend.CodeBackendUnavailable, "cannot reach meja to attach %s", target)
	}

	attachment := backend.Attachment{
		Cmd: exec.CommandContext(ctx, "meja", append(m.addressing(), "attach", "-t", target)...),
	}
	if spec.Role == backend.RoleViewer {
		// meja has no read-only client. Dropping input silently would be worse
		// than saying so: a watcher who believes they cannot type, and can,
		// will eventually type into somebody else's session (§8.7).
		return backend.Attachment{}, backend.Errorf(backend.CodeUnsupported,
			"meja has no read-only client, so a viewer attach cannot be made passive")
	}
	if spec.Supersede {
		attachment.Notices = append(attachment.Notices,
			"meja has no supersede: any prior client stays attached, and meja sizes the session to the smallest of them")
	}
	return attachment, nil
}
