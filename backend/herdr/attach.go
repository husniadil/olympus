package herdr

import (
	"context"
	"os/exec"

	"github.com/husniadil/olympus/backend"
)

// Attach prepares an attach client for the engine to run inside a PTY.
//
// The presence gate is here so an attach onto nothing fails as not-found rather
// than as whatever the client happens to print — and here it is a courtesy
// rather than the load-bearing guard it is on zmx: herdr's attach does not
// upsert a missing terminal, it refuses one (§8.1).
//
// Supersession is the SERVER'S, not Olympus's. herdr allows one attached client
// per terminal and refuses a second unless it asks to take over, so there is no
// pidfile guard to keep and no sweep to run: the mechanism §8.5 builds for zmx
// is already inside the backend. Measured: a second attach without takeover is
// refused with `terminal <id> already has an attached client; retry with
// --takeover`, and with it the prior client is detached cleanly and told
// `terminal attach taken over`.
//
// One consequence is worth stating rather than hiding. Because the refusal is
// the server's, a non-superseding attach onto an occupied terminal fails INSIDE
// the client, after the PTY is running, rather than as a conflict Olympus
// raises before spawning one. herdr's socket API reports no per-terminal client
// count, so there is nothing to check beforehand.
func (h *Herdr) Attach(ctx context.Context, target string, spec backend.AttachSpec) (backend.Attachment, error) {
	if spec.Role == backend.RoleViewer {
		// herdr's read-only stream is not a terminal client: it emits JSON
		// frames for a program to decode, not a rendering for a human to sit
		// in, so there is nothing to hand a PTY. Dropping input silently
		// instead would be worse than saying so — a watcher who believes they
		// cannot type, and can, will eventually type into somebody else's
		// session (§8.7).
		return backend.Attachment{}, backend.Errorf(backend.CodeUnsupported,
			"herdr has no read-only terminal client, so a viewer attach cannot be made passive")
	}

	row, err := h.resolvePane(ctx, target)
	if err != nil {
		return backend.Attachment{}, err
	}
	if row.TerminalID == "" {
		return backend.Attachment{}, backend.Errorf(backend.CodeUnexpected,
			"session %s reports no terminal to attach to", target)
	}

	// The terminal id rather than the pane id: attaching addresses the
	// server-owned terminal, which is the thing a client streams, and it is
	// what the pane row already carries.
	args := []string{"terminal", "attach", row.TerminalID}
	if spec.Supersede {
		args = append(args, "--takeover")
	}
	cmd := exec.CommandContext(ctx, "herdr", args...)
	if h.startedTheServer() {
		// Our server, our configuration directory: it is the one that server
		// was booted against, and there is nothing of anybody else's in it.
		cmd.Env = h.env(attachEnv())
	} else {
		// A server this handle did not start belongs to whoever runs it, and
		// the attach client is the one invocation whose behaviour its
		// configuration decides. Imposing a directory of Olympus's own would
		// hand a human their own terminal configured like a fresh install
		// (src/client/mod.rs:1225-1234).
		cmd.Env = h.socketEnv(attachEnv())
	}
	return backend.Attachment{Cmd: cmd}, nil
}
