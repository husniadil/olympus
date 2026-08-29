package meja

import "os"

// The sanitized spawn environment (behavior §1.1).
//
// The environment is the backend's own to build rather than something handed
// down with the request: every backend applies the same rules, and a curated
// map travelling through CreateSpec would be a second place for them to live.
const (
	spawnTerm   = "xterm-256color"
	defaultLang = "en_US.UTF-8"
)

// strippedVars must go from every meja invocation's environment.
//
// TERM is forced because a host running inside tmux or screen inherits a
// screen-family TERM, and a shell such as zsh then emits its window title as
// the screen sequence ESC k <title> ESC \ — which a consumer that does not
// interpret it renders as literal text, leaking every command name into the
// pane's visible output.
//
// LANG is defaulted because processes started by launchd have no LANG at all,
// degrading output to the C locale and mangling every non-ASCII byte. It is
// read at call time, never cached at process start.
//
// The multiplexer identity of the OTHER backends is stripped for the reason
// their own files give: an inherited TMUX makes tmux treat a new client as a
// nested session, and an inherited ZMX_SESSION retargets a zmx invocation at
// whatever session it names. meja itself is addressed by socket path rather
// than by an ambient variable, so it contributes nothing of its own here — but
// a session meja spawns is one those variables would follow into.
// strippedVars must go from every invocation's environment.
//
// The herdr entries are the same class of hazard as the others and were added
// with that backend: HERDR_PANE_ID is what a process inside a herdr pane is
// identified BY (§1.1), so a session created here from inside one would inherit
// it and then answer "I am in a herdr pane" when asked where it is. HERDR_SESSION
// and the two socket variables retarget herdr's own commands the way ZMX_SESSION
// retargets zmx's.
var strippedVars = []string{
	"TMUX", "TMUX_PANE", "ZMX_SESSION", "ZMX_SESSION_PREFIX",
	"HERDR_SESSION", "HERDR_PANE_ID", "HERDR_WORKSPACE_ID", "HERDR_TAB_ID",
	"HERDR_SOCKET_PATH", "HERDR_CLIENT_SOCKET_PATH",
}

func lang() string {
	if v := os.Getenv("LANG"); v != "" {
		return v
	}
	return defaultLang
}

// spawnEnv is the environment for a meja invocation that creates or writes to a
// session.
//
// It is NOT used for the interactive attach client of §1.3, nor for the
// transient and follow clients, which build their own: those inherit the
// terminal they are actually sitting at.
func spawnEnv() []string {
	out := make([]string, 0, len(os.Environ())+2)
	for _, kv := range os.Environ() {
		if isStripped(kv) || hasKey(kv, "TERM") || hasKey(kv, "LANG") {
			continue
		}
		out = append(out, kv)
	}
	return append(out, "TERM="+spawnTerm, "LANG="+lang())
}

func isStripped(kv string) bool {
	for _, name := range strippedVars {
		if hasKey(kv, name) {
			return true
		}
	}
	return false
}

func hasKey(kv, name string) bool {
	return len(kv) > len(name) && kv[len(name)] == '=' && kv[:len(name)] == name
}
