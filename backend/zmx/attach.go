package zmx

import (
	"context"
	"os/exec"

	"github.com/husniadil/olympus/backend"
)

// Attach prepares an attach client for the engine to run inside a PTY.
//
// The presence gate is mandatory here and is not defensive politeness: "zmx
// attach <name>" CREATES the session when it does not exist, so attaching to a
// mistyped or already-dead name would silently spawn a new empty session
// instead of reporting that the one asked for is gone (§8.1).
func (z *Zmx) Attach(ctx context.Context, target string, spec backend.AttachSpec) (backend.Attachment, error) {
	switch z.Probe(ctx, target) {
	case backend.StatePresent:
	case backend.StateAbsent:
		return backend.Attachment{}, backend.Errorf(backend.CodeSessionNotFound, "no session %s", target)
	default:
		return backend.Attachment{}, backend.Errorf(backend.CodeBackendUnavailable, "cannot reach zmx to attach %s", target)
	}

	if spec.Supersede {
		// Displace prior clients before the new one connects. Scoped to this
		// backend's own socket directory, so it can never sweep a daemon it
		// does not own (§8.5).
		cmd := z.command(ctx, "detach")
		// Best effort: no prior client is the ordinary case, not a failure.
		_ = cmd.Run()
	}

	cmd := exec.CommandContext(ctx, "zmx", "attach", target)
	cmd.Env = z.env(attachEnv())
	return backend.Attachment{Cmd: cmd}, nil
}
