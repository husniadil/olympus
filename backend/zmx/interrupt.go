package zmx

import (
	"context"
	"os/exec"
	"strconv"
	"strings"
	"syscall"

	"github.com/husniadil/olympus/backend"
)

// Interrupt delivers SIGINT to the session's foreground process group.
//
// It deliberately does NOT write 0x03 into the session. zmx's send path does
// not generate a terminal SIGINT at all: a foreground job with an ordinary
// default disposition survives it indefinitely, yet dies immediately from a
// signal aimed at its process group. The process was willing to die; the
// terminal path never produced a signal (§2.8.1, cause 1).
//
// The group is derived from the leader pid the listing reports and that
// process's controlling-tty foreground group. A foreground group equal to the
// leader's own means the session is sitting at its prompt with no foreground
// job, so there is nothing to interrupt and that is a success.
//
// This works for a session running a shell — the default and common case. A
// session exec'd directly onto a non-shell argv cannot be interrupted at all:
// it inherits SIGINT as SIG_IGN, and a signal ignored on entry can never be
// trapped or reset, so the signal returns success while the process survives
// (§2.8.1, cause 2). Graceful kill must fall through to a forced kill there,
// which does work. That is a property of how zmx spawns, not something that can
// be routed around at kill time.
func (z *Zmx) Interrupt(ctx context.Context, target string) error {
	leader, err := z.leaderPID(ctx, target)
	if err != nil {
		return err
	}

	foreground, err := foregroundGroup(ctx, leader)
	if err != nil {
		return err
	}
	if foreground <= 0 || foreground == leader {
		// At the prompt with no foreground job. The desired state already
		// holds.
		return nil
	}

	if err := syscall.Kill(-foreground, syscall.SIGINT); err != nil {
		if err == syscall.ESRCH {
			// It died between reading the group and signalling it, which is
			// the outcome that was wanted.
			return nil
		}
		return backend.Wrapf(backend.CodeUnexpected, err, "interrupting %s", target)
	}
	return nil
}

// leaderPID reads the session's leader process from the listing.
func (z *Zmx) leaderPID(ctx context.Context, target string) (int, error) {
	rows, err := z.listRows(ctx)
	if err != nil {
		return 0, err
	}
	for _, row := range rows {
		if row["name"] != target {
			continue
		}
		pid, err := strconv.Atoi(row["pid"])
		if err != nil || pid <= 0 {
			return 0, backend.Errorf(backend.CodeUnexpected, "session %s reports no usable leader pid", target)
		}
		return pid, nil
	}
	return 0, backend.Errorf(backend.CodeSessionNotFound, "no session %s", target)
}

// foregroundGroup reads a process's controlling-tty foreground process group.
//
// The leader pid alone is not enough: signalling the leader's own group would
// hit the session's shell rather than the job it is running, which is the
// opposite of what an interrupt means.
func foregroundGroup(ctx context.Context, pid int) (int, error) {
	out, err := exec.CommandContext(ctx, "ps", "-o", "tpgid=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		// The process is gone, so there is no foreground job to interrupt.
		return 0, nil
	}
	field := strings.TrimSpace(string(out))
	if field == "" {
		return 0, nil
	}
	group, err := strconv.Atoi(field)
	if err != nil {
		return 0, backend.Wrapf(backend.CodeUnexpected, err, "reading the foreground process group of pid %d", pid)
	}
	return group, nil
}
