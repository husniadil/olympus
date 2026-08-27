package zmx

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

// strippedVars must go from every zmx invocation's environment.
//
// An inherited ZMX_SESSION is not a cosmetic leak. "zmx attach <name> <argv>"
// with it set does not create or attach <name>: it switches the CURRENT
// session's daemon, yanking that session's leader client over to <name>. On the
// attach path it is worse still — the name is ignored entirely and the call
// fails with `session "<ambient>" does not exist`, where <ambient> is whatever
// ZMX_SESSION held. It does not degrade; it silently retargets (§1.1, §1.3).
var strippedVars = []string{"TMUX", "TMUX_PANE", "ZMX_SESSION", "ZMX_SESSION_PREFIX"}

func lang() string {
	if v := os.Getenv("LANG"); v != "" {
		return v
	}
	return defaultLang
}

// spawnEnv is the environment for a zmx invocation that creates or writes to a
// session. TERM is forced so a screen-family value inherited from an enclosing
// multiplexer cannot make a shell emit its window title as literal text.
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

// attachEnv is the environment for an interactive attach client (behavior §1.3).
//
// It does NOT reuse spawnEnv: an interactive attach must inherit the operator's
// real TERM, since forcing one would misrepresent the terminal the human is
// sitting at. The strips and the LANG default still apply.
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
