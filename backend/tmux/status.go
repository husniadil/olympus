package tmux

import (
	"context"
	"strings"
)

// statusOption is where a session's status lives.
//
// A tmux user option: tmux stores it and never acts on it, so it costs the
// session nothing, and it is scoped to the session rather than the server, so
// two sessions cannot read each other's. It also outlives the process that set
// it, which is the point — the reporter is inside the session and the reader is
// outside it, and they never run at the same time.
const statusOption = "@olympus_status"

// statusTarget is how set-option and show-options name a session.
//
// The window form of an exact name. A bare name matches by prefix, so a status
// set on "cwd" would land on "cwdx", and `=name` alone is rejected with "no
// such session" (§10).
func statusTarget(name string) string { return windowTarget(name) }

// SetStatus records an opaque label on a session.
//
// Olympus never interprets the value. Enumerating states would mean naming the
// concerns of whatever is driving the terminal rather than the terminal itself.
func (t *Tmux) SetStatus(ctx context.Context, target, status string) error {
	_, err := t.run(ctx, nil, "set-option", "-t", statusTarget(target), statusOption, escapeTrailingSemicolon(status))
	return named(target, err)
}

// Status reports a session's label, empty when it has never been given one.
//
// Empty is a real answer rather than an error: a session that has reported
// nothing is a state a caller acts on, and collapsing it into a failure would
// destroy the distinction from "could not ask" that §3.5 exists to preserve.
func (t *Tmux) Status(ctx context.Context, target string) (string, error) {
	out, err := t.run(ctx, nil, "show-options", "-t", statusTarget(target), "-qv", statusOption)
	if err != nil {
		return "", named(target, err)
	}
	// -q silences a missing target as well as a missing option, so an empty
	// answer is only "never reported" once the session is known to exist.
	if out == "" {
		if _, err := t.run(ctx, nil, "has-session", "-t", sessionTarget(target)); err != nil {
			return "", named(target, err)
		}
	}
	return strings.TrimRight(out, "\n"), nil
}

var _ interface {
	SetStatus(context.Context, string, string) error
	Status(context.Context, string) (string, error)
} = (*Tmux)(nil)
