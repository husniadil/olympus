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
// mistyped or already-dead name would silently spawn a fresh unrelated shell
// under that name instead of reporting that the one asked for is gone. A race
// between a session's death and an attach call would otherwise fabricate a
// phantom session that looks completely legitimate (behavior §8.1).
func (z *Zmx) Attach(ctx context.Context, target string, spec backend.AttachSpec) (backend.Attachment, error) {
	switch z.Probe(ctx, target) {
	case backend.StatePresent:
	case backend.StateAbsent:
		return backend.Attachment{}, backend.Errorf(backend.CodeSessionNotFound, "no session %s", target)
	default:
		// Fails closed on a probe error too, for the same reason.
		return backend.Attachment{}, backend.Errorf(backend.CodeBackendUnavailable, "cannot reach zmx to attach %s", target)
	}

	attachment := backend.Attachment{}
	if spec.Supersede {
		if err := z.sweep(ctx, target); err != nil {
			// Best-effort, and LOUD. A failed sweep leaves prior clients
			// co-attached — degraded but usable — which is not a reason to
			// refuse the attach the operator asked for. But it must be said,
			// because a silent no-op here is indistinguishable from a
			// successful steal, which is the precise failure this exists to
			// remove (behavior §8.5).
			attachment.Notices = append(attachment.Notices,
				"could not detach other clients: "+err.Error()+"; they are still attached")
		}
	}

	cmd := exec.CommandContext(ctx, "zmx", "attach", target)
	cmd.Env = z.env(attachEnv())
	attachment.Cmd = cmd
	return attachment, nil
}

// sweep detaches every client from a session, leaving the session alive.
//
// The primitive is undocumented and not discoverable from zmx's own help, which
// lists `zmx detach` as taking no arguments. A session name passed positionally
// is accepted and silently IGNORED. What it actually resolves its target from is
// the ambient ZMX_SESSION — the variable zmx sets inside a session's own shell —
// so aiming it at a session from outside means setting that variable explicitly
// (behavior §8.5).
//
// This is the one place ZMX_SESSION is deliberately SET rather than stripped.
// Everywhere else it is stripped precisely because it silently retargets
// commands (§1.1); here that retargeting is the mechanism.
func (z *Zmx) sweep(ctx context.Context, target string) error {
	cmd := exec.CommandContext(ctx, "zmx", "detach")
	cmd.Env = append(z.env(spawnEnv()), "ZMX_SESSION="+target)
	if out, err := cmd.CombinedOutput(); err != nil {
		return classify(err, string(out), []string{"detach"})
	}
	return nil
}

// The sweep covers what a pidfile guard cannot see: a raw `zmx attach` an
// operator started from their own terminal has no pidfile, no lock, and no
// signal handler.
//
// A residual race is accepted: the sweep is point-in-time, so a client that
// attaches between the sweep and Olympus's own attach survives it. Closing that
// would need a zmx-side exclusive-attach primitive, which does not exist.
