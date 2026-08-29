package herdr

import "os"

// The sanitized spawn environment (behavior §1.1).
const (
	spawnTerm   = "xterm-256color"
	defaultLang = "en_US.UTF-8"
)

// strippedVars must go from every herdr invocation's environment.
//
// The first four are §1.1's multiplexer identity strip, unchanged.
//
// The herdr pair is the same class of hazard as ZMX_SESSION and is stripped for
// the same reason, even though the precedence currently protects us:
// HERDR_SESSION selects a NAMED server whose socket lives under herdr's
// configuration directory, and HERDR_CLIENT_SOCKET_PATH selects the client
// socket on its own. Both would silently retarget every call at a server the
// caller did not choose. herdr resolves HERDR_SOCKET_PATH first and forces the
// session override off when it is set (src/session.rs:81-83), so this is
// defence rather than a fix — but the ordering is herdr's to change and the
// consequence of it changing is that a test suite drives the operator's live
// session.
var strippedVars = []string{
	"TMUX", "TMUX_PANE", "ZMX_SESSION", "ZMX_SESSION_PREFIX",
	"HERDR_SESSION", "HERDR_CLIENT_SOCKET_PATH", "HERDR_SOCKET_PATH",
	// The identity of whatever herdr pane Olympus itself is running in. A
	// server started from inside one would otherwise inherit it, and this
	// backend's own socket override is appended after the strip.
	"HERDR_PANE_ID", "HERDR_WORKSPACE_ID", "HERDR_TAB_ID",
}

func lang() string {
	if v := os.Getenv("LANG"); v != "" {
		return v
	}
	return defaultLang
}

// invocationEnv is the environment every herdr CLI call runs with.
//
// Unlike zmx and tmux, this is NOT the environment a session is spawned with:
// a pane inherits the SERVER's environment, which was fixed when the server
// booted and is not ours to change from a client. The spawn hygiene of §1.1 is
// applied per pane instead, through the creation request's own env arguments —
// see spawnEnvArgs.
func invocationEnv() []string {
	out := make([]string, 0, len(os.Environ()))
	for _, kv := range os.Environ() {
		if isStripped(kv) {
			continue
		}
		out = append(out, kv)
	}
	return out
}

// attachEnv is the environment for an interactive attach client (behavior §1.3).
//
// It does NOT force TERM: an interactive attach must inherit the operator's
// real terminal, since forcing one would misrepresent the terminal the human is
// sitting at. The strips and the LANG default still apply.
//
// It is also the ONE invocation whose configuration directory matters, which is
// why Attach chooses between two environments rather than using one. Every
// other verb this backend runs is a JSON request over the socket and reads no
// configuration at all; the attach client loads it and takes its mouse capture,
// scroll lines, focus-redraw, host-cursor, sound and paste-key settings from
// there (src/client/mod.rs:1225-1234, reached from run_terminal_attach at
// src/client/mod.rs:940-947). Pointing that at a directory of Olympus's own
// would hand a human attaching to their own server a client configured like a
// fresh install.
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

// spawnEnvArgs is §1.1's spawn hygiene, as creation-request arguments.
//
// This is the ONLY place the rules can be applied on herdr. A pane's
// environment is seeded from the server's, which is a second leak in exactly
// the way §1.2 describes for tmux: a server somebody else started carries their
// environment into every pane Olympus makes there. The creation request's own
// env arguments override it per pane, and an empty VALUE is a real empty
// variable in the pane rather than an omitted one — measured.
func spawnEnvArgs() []string {
	args := []string{
		"--env", "TERM=" + spawnTerm,
		"--env", "LANG=" + lang(),
	}
	for _, name := range []string{"TMUX", "TMUX_PANE", "ZMX_SESSION", "ZMX_SESSION_PREFIX"} {
		args = append(args, "--env", name+"=")
	}
	return args
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
