// Package olympus drives real terminal sessions on top of a multiplexer it does
// not embed.
//
// It is the ergonomic layer: the place where defaults are decided, once
// (behavior §17.3). The mechanical contract lives in package backend, and the
// rules both layers implement are specified in docs/terminal-behavior.md.
package olympus

import (
	"fmt"
	"os/exec"
	"runtime"
	"strings"

	"github.com/husniadil/olympus/backend"
)

// BackendEnv names the environment variable that selects a backend.
const BackendEnv = "OLYMPUS_BACKEND"

// preference is the order backends are tried when nothing was chosen: the
// default first, then the fallbacks (behavior §0.3).
//
// meja is LAST, and that placement is load-bearing rather than alphabetical. A
// backend that displaced zmx or tmux would move a caller's sessions to one they
// never chose, and sessions never migrate between backends — so meja answers
// only when it is the last one standing, which beats refusing to run on a host
// that does have a working multiplexer.
var preference = []backend.Name{backend.Zmx, backend.Tmux, backend.Meja}

// A Reason names the resolution rule that applied, satisfying the disclosure
// requirement of behavior §0.4.
type Reason string

const (
	// ReasonFlag is an explicit selection: a flag, a library option, or an MCP
	// parameter.
	ReasonFlag Reason = "flag"
	// ReasonEnv is the environment variable.
	ReasonEnv Reason = "env"
	// ReasonDefault is the default, which was available.
	ReasonDefault Reason = "default"
	// ReasonFallback is the default being unavailable and another supported
	// backend being present.
	ReasonFallback Reason = "fallback"
)

// A Resolution is which backend answered, and why.
//
// The resolved backend — never the requested one — is what must be observable,
// because sessions are backend-scoped: they never migrate and never merge, and
// a session created on one backend is invisible from the other. A silent
// fallback would let a user create sessions, change their installed tooling,
// and find those sessions apparently vanished with nothing explaining why.
type Resolution struct {
	Backend backend.Name
	Reason  Reason
}

// installedFunc reports whether a backend's binary is on PATH. It is injected
// so resolution can be tested on a host with any combination installed.
type installedFunc func(backend.Name) bool

// onPath is the real preflight: a single lookup, no subprocess, on every code
// path (behavior §0.2).
func onPath(name backend.Name) bool {
	_, err := exec.LookPath(string(name))
	return err == nil
}

// resolve chooses a backend from an explicit selection, the environment, and
// what is installed (behavior §0.1, §0.2, §0.3).
func resolve(explicit, env string, installed installedFunc) (Resolution, error) {
	if chosen := strings.TrimSpace(explicit); chosen != "" {
		return resolveExplicit(chosen, ReasonFlag, installed)
	}
	if chosen := strings.TrimSpace(env); chosen != "" {
		return resolveExplicit(chosen, ReasonEnv, installed)
	}

	for i, name := range preference {
		if !installed(name) {
			continue
		}
		reason := ReasonDefault
		if i > 0 {
			// Refusing to start on a host with a working multiplexer installed
			// would be hostile for no gain — but the substitution is disclosed
			// rather than silent.
			reason = ReasonFallback
		}
		return Resolution{Backend: name, Reason: reason}, nil
	}
	return Resolution{}, errNoBackendInstalled()
}

func resolveExplicit(chosen string, reason Reason, installed installedFunc) (Resolution, error) {
	name := backend.Name(chosen)
	if !known(name) {
		// The caller advertised a closed set of legal values and one corrected
		// argument fixes it. Unexpected-class would tell a machine consumer
		// "retrying will not help", which is the opposite of the truth
		// (behavior §0.1).
		return Resolution{}, backend.Errorf(backend.CodeUsage,
			"unknown backend %q; supported backends are %s", chosen, strings.Join(knownNames(), " and "))
	}
	if !installed(name) {
		// No fallback, ever, for an explicit choice. Silently running
		// somewhere the caller did not ask for is worse than failing
		// (behavior §0.3).
		return Resolution{}, backend.Errorf(backend.CodeBackendUnavailable,
			"%s was selected but is not installed: %s not found on PATH.\n%s\nRun `olympus doctor` to see what is available.",
			name, name, installHint(name))
	}
	return Resolution{Backend: name, Reason: reason}, nil
}

func known(name backend.Name) bool {
	for _, candidate := range preference {
		if candidate == name {
			return true
		}
	}
	return false
}

func knownNames() []string {
	out := make([]string, 0, len(preference))
	for _, name := range preference {
		out = append(out, string(name))
	}
	return out
}

// errNoBackendInstalled is one complete message, not a failure per attempted
// backend (behavior §0.7).
//
// It says what Olympus is, because a first-time user hitting this does not yet
// know that it drives a multiplexer rather than embedding one — and it must not
// degrade to a plain PTY instead, since detach, reattach and durable sessions
// are the whole product, and a mode quietly lacking them would fail later and
// further from the cause.
func errNoBackendInstalled() error {
	var b strings.Builder
	b.WriteString("no terminal multiplexer is installed.\n")
	b.WriteString("Olympus drives an existing multiplexer rather than embedding one, so it needs one of zmx, tmux or meja.\n")
	for _, name := range preference {
		fmt.Fprintf(&b, "  %s: %s\n", name, installHint(name))
	}
	b.WriteString("Run `olympus doctor` for the full picture.")
	return backend.Errorf(backend.CodeBackendUnavailable, "%s", b.String())
}

// installHint gives the install command for the host platform. A raw
// "executable file not found in $PATH" tells a first-time user nothing about
// what Olympus needs (behavior §0.2).
func installHint(name backend.Name) string {
	switch name {
	case backend.Tmux:
		if runtime.GOOS == "darwin" {
			return "install it with `brew install tmux`"
		}
		return "install it with your package manager, for example `apt install tmux`"
	case backend.Zmx:
		// The URL was left out here for a while, on the rule that a wrong one is
		// worse than none in the first message a new user reads. It is in now
		// because it was checked, not because it looked right.
		return "install it from https://github.com/neurosnap/zmx and make sure `zmx` is on your PATH"
	case backend.Meja:
		return "install it with `go install github.com/garindra/meja@latest` and make sure `meja` is on your PATH"
	default:
		return "no install instructions are known"
	}
}

// zmxDirEnv is zmx's own directory variable, read by the zmx binary whether or
// not Olympus passes it (api §4).
const zmxDirEnv = "ZMX_DIR"

// addressing names each backend's isolation options, in the spelling a caller
// types them.
//
// Kept as data rather than as a switch because it is read twice — to reject the
// options a backend cannot use, and to say which backend can — and two copies
// of that list would eventually disagree about one entry.
var addressing = map[backend.Name][]string{
	backend.Tmux: {"socket", "socket-path"},
	backend.Zmx:  {"zmx-dir"},
	backend.Meja: {"socket-path"},
}

// checkAddressing rejects an addressing option the resolved backend cannot use.
//
// A USAGE error rather than a silent no-op, because every one of these options
// exists to ISOLATE — to put a server somewhere the caller controls. Dropping
// one quietly lands them on the shared default while they believe they are
// alone on a private one, and nothing about the result says otherwise. Measured
// before this existed: `--backend meja --socket <private>` resolved to meja's
// default profile and exited 0.
//
// It is USAGE and not UNSUPPORTED because one corrected argument fixes it, and
// §0.1 reserves usage-class for exactly that.
func checkAddressing(name backend.Name, cfg config) error {
	set := map[string]string{"socket": cfg.socket, "socket-path": cfg.socketPath, "zmx-dir": cfg.zmxDir}
	usable := map[string]bool{}
	for _, option := range addressing[name] {
		usable[option] = true
	}

	// Ordered, so the message a caller sees does not depend on map iteration.
	for _, option := range []string{"socket", "socket-path", "zmx-dir"} {
		if set[option] == "" || usable[option] {
			continue
		}
		return backend.Errorf(backend.CodeUsage,
			"--%s does not apply to the %s backend; it addresses %s. %s takes %s",
			option, name, appliesTo(option), name, strings.Join(dashed(addressing[name]), " or "))
	}
	return nil
}

// appliesTo names the backends an option does address, so the message points at
// the fix rather than only at the mistake.
func appliesTo(option string) string {
	var names []string
	for _, candidate := range preference {
		for _, o := range addressing[candidate] {
			if o == option {
				names = append(names, string(candidate))
			}
		}
	}
	return strings.Join(names, " and ")
}

func dashed(options []string) []string {
	out := make([]string, 0, len(options))
	for _, o := range options {
		out = append(out, "--"+o)
	}
	return out
}
