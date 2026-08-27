package tmux

import (
	"context"
	"strconv"
	"strings"
)

// markerOption records that Olympus started this server.
//
// Ownership cannot be inferred from the pinned VALUES: an operator whose own
// config happens to set the same ones — and a large history-limit is an
// ordinary thing to set — would be reported as owning nothing they own. The
// mark is written in the same chain that starts the server, so a server Olympus
// merely finds never receives it: that chain never runs there.
//
// It is a user option, which tmux stores and never acts on, so a server that
// somehow does carry it behaves no differently for having it.
const markerOption = "@olympus_managed"

// HistoryLimit is the scrollback depth Olympus pins on servers it drives.
//
// It is pinned rather than inherited because a capture asking for N lines must
// mean the same thing everywhere. tmux's own default is 2000 and a typical
// operator config raises it, so an unpinned Olympus reads a different depth on
// every machine and reports the difference as no difference at all.
const HistoryLimit = 50000

// ManagedOptions are the server options Olympus sets for itself, and the whole
// of what it takes away from the operator's configuration.
//
// A socket of our own is NOT a configuration of our own: tmux fixes a server's
// configuration at boot from the operator's tmux.conf, and the socket only
// decides which server that is. Everything the operator sets therefore reaches
// our sessions — their keybindings and colours, which is welcome, but also
// options the protocol's correctness rests on, which is not.
//
// The list is deliberately short. Olympus pins what it would otherwise be lying
// about, and nothing cosmetic: an operator who attaches to a session keeps their
// own prefix, their own theme, their own bindings.
func ManagedOptions() [][]string {
	return [][]string{
		// An operator's default-command chooses the shell our sessions run, and
		// the run protocol's exit marker is written BY that shell. Under csh,
		// `echo "OLY_D_id_$?_"` becomes `OLY_D_id_1`: csh reads `$?_` as "is the
		// variable _ set", so the real exit status is replaced by a 1 and the
		// closing delimiter vanishes. A caller is then told a command that
		// failed with 3 succeeded, or is told nothing at all.
		//
		// Empty restores tmux's own behaviour — the operator's login shell,
		// which is theirs to choose and which a human attaching expects. What is
		// taken away is only the ability of a config file to substitute a
		// different one behind Olympus's back.
		//
		// default-shell is deliberately NOT pinned. tmux has no notion of a
		// non-interactive pane, so pinning it would hand a human attaching a
		// bare `sh` prompt instead of their own shell — paying a real cost to
		// the human audience for a guarantee this does not actually give, since
		// their login shell could be non-POSIX either way. That assumption is
		// stated and reported rather than forced (behavior §17.5).
		{"default-command", ""},
		{"history-limit", strconv.Itoa(HistoryLimit)},
	}
}

// ServerRunning reports whether anything is listening on this backend's socket.
//
// It is how Create decides whether the server about to answer is one Olympus is
// starting, and so whether it may configure it at all (§17.5).
func (t *Tmux) ServerRunning(ctx context.Context) bool {
	// Any command that must reach the server will do; list-sessions is chosen
	// because a server with no sessions still answers it, and a missing one
	// gives the message isNoServer already knows how to read.
	if _, err := t.run(ctx, nil, "list-sessions", "-F", ""); err != nil {
		return !isNoServer(err)
	}
	return true
}

// EffectiveOptions reports what the managed options are set to on the server
// answering right now, and whether every one of them already carries Olympus's
// own value.
//
// The second return is how a diagnostic tells a server Olympus started from one
// it merely found: the marker option, not the values, is what records it (§17.5).
func (t *Tmux) EffectiveOptions(ctx context.Context) (map[string]string, bool, error) {
	if !t.ServerRunning(ctx) {
		return nil, false, nil
	}
	mark, err := t.run(ctx, nil, "show-options", "-sqv", markerOption)
	if err != nil {
		return nil, false, err
	}
	pinned := strings.TrimSpace(mark) == "1"

	effective := map[string]string{}
	for _, option := range ManagedOptions() {
		value, err := t.run(ctx, nil, "show-options", "-gqv", option[0])
		if err != nil {
			return nil, false, err
		}
		value = strings.TrimSpace(value)
		effective[option[0]] = value
	}
	return effective, pinned, nil
}

// pinManagedOptions returns the argv prefix that applies them, for chaining
// AHEAD of the command whose behaviour depends on them.
//
// Ahead, not after, and this is the whole reason it is built as a prefix: a
// pane reads default-command and history-limit when it SPAWNS. Setting them
// after new-session sets them for the next session and leaves this one exactly
// as misconfigured as before — a fix that measures as working while fixing
// nothing.
//
// Chaining also reaches further than tmux's own -f flag can. Configuration is
// per-server and fixed at boot, so -f is silently ignored on a server that is
// already running, while these apply to whichever server answers.
func pinManagedOptions() []string {
	// The mark goes on the SERVER scope, alongside the options themselves, so
	// it lives exactly as long as the server it describes.
	args := []string{"set-option", "-s", markerOption, "1"}
	for _, option := range ManagedOptions() {
		if len(args) > 0 {
			args = append(args, ";")
		}
		args = append(args, "set-option", "-g", option[0], option[1])
	}
	return append(args, ";")
}
