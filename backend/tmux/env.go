package tmux

import "os"

// The sanitized spawn environment (behavior §1.1).
//
// The environment is the backend's own to build rather than something handed
// down with the request: every backend applies the same rules, and a curated
// map travelling through CreateSpec would be a second place for them to live.
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
// Multiplexer identity is stripped because an inherited TMUX makes tmux treat
// the new client as a nested session, and an inherited ZMX_SESSION is worse
// still: it can yank a live session's leader client somewhere else.
const (
	spawnTerm   = "xterm-256color"
	defaultLang = "en_US.UTF-8"
)

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

// clientEnv is the environment for the tmux client Olympus execs.
func clientEnv() []string {
	out := make([]string, 0, len(os.Environ())+2)
	for _, kv := range os.Environ() {
		if isStripped(kv) || hasKey(kv, "TERM") || hasKey(kv, "LANG") {
			continue
		}
		out = append(out, kv)
	}
	return append(out, "TERM="+spawnTerm, "LANG="+lang())
}

// attachEnv is the environment for an interactive attach client (behavior §1.3).
//
// It deliberately does NOT reuse the spawn environment: an interactive attach
// must inherit the operator's real TERM, since forcing xterm-256color would
// misrepresent the terminal the human is actually sitting at. The strips and
// the LANG default still apply.
func attachEnv() []string {
	out := make([]string, 0, len(os.Environ())+1)
	hasLang := os.Getenv("LANG") != ""
	for _, kv := range os.Environ() {
		if isStripped(kv) {
			continue
		}
		out = append(out, kv)
	}
	if !hasLang {
		out = append(out, "LANG="+defaultLang)
	}
	return out
}

// sessionEnv is what new-session carries per-session via -e (behavior §1.2).
//
// Setting cmd.Env on the client is not sufficient: a new session's environment
// is seeded from the server's global environment, fixed when the server booted.
// The ZMX_* entries are set-to-empty rather than omitted, which is what yields
// an empty value in the pane even against a server whose global environment
// carries a poisoned one.
func sessionEnv() []string {
	env := []string{"TERM=" + spawnTerm, "LANG=" + lang()}
	for _, name := range strippedVars {
		env = append(env, name+"=")
	}
	return env
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
