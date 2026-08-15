package tmux

import (
	"context"
	"os/exec"
	"strconv"

	"github.com/husniadil/olympus/backend"
)

func itoa(n int) string { return strconv.Itoa(n) }

// Attach prepares an attach client for the engine to run inside a PTY.
//
// It returns the command rather than running it: the PTY, the signal handling
// and the terminal restore of §8.2 belong to one shared engine, not to each
// backend.
func (t *Tmux) Attach(ctx context.Context, target string, spec backend.AttachSpec) (backend.Attachment, error) {
	// The presence gate is checked here so an attach onto nothing fails as
	// not-found rather than as whatever the client happens to print.
	if state := t.Probe(ctx, target); state != backend.StatePresent {
		if state == backend.StateAbsent {
			return backend.Attachment{}, backend.Errorf(backend.CodeSessionNotFound, "no session %s", target)
		}
		return backend.Attachment{}, backend.Errorf(backend.CodeBackendUnavailable, "cannot reach tmux to attach %s", target)
	}

	// -T declares this CLIENT's terminal features, and is a global flag, so it
	// sits ahead of the command.
	//
	// tmux strips OSC 8 hyperlinks for any client whose terminal has not
	// declared the capability, and a headless PTY client never answers tmux's
	// runtime probe — so without this they vanish silently, with no error
	// anywhere. Declared per client rather than through the terminal-features
	// SERVER option, which has no per-session form: editing it would change how
	// tmux renders for every other client of that server, including the
	// operator's own sessions when Olympus is pointed at a server they already
	// run (§9.6). A real terminal answers the probe and needs nothing from us.
	//
	// -u belongs to the same group and for the same structural reason, not
	// merely by convention: attach-session takes [-dErx], and tmux rejects
	// `attach-session … -u` outright with "command attach-session: unknown
	// flag -u". Without it the CLIENT — not the pane's programs — sanitizes
	// every non-ASCII byte to "_" before it reaches the consumer. The pane is
	// fine; the stream is not. This is additional to the LANG default: LANG is
	// a belt for the programs inside the pane, -u is for the client (§1.4).
	args := append(t.addressing(), "-u", "-T", "hyperlinks", "attach-session", "-t", sessionTarget(target))
	if spec.Role == backend.RoleViewer {
		// A viewer drops resize as well as input, or a passive watcher
		// reshapes everyone else's terminal (§8.7).
		args = append(args, "-r")
	}
	if spec.Supersede {
		args = append(args, "-d")
	}

	cmd := exec.CommandContext(ctx, "tmux", args...)
	cmd.Env = attachEnv()
	return backend.Attachment{Cmd: cmd}, nil
}
