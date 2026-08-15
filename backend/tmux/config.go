package tmux

import "strconv"

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
	var args []string
	for _, option := range ManagedOptions() {
		if len(args) > 0 {
			args = append(args, ";")
		}
		args = append(args, "set-option", "-g", option[0], option[1])
	}
	return append(args, ";")
}
