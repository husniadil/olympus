package olympus

import (
	"context"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/husniadil/olympus/backend"
	"github.com/husniadil/olympus/backend/herdr"
	"github.com/husniadil/olympus/backend/meja"
	"github.com/husniadil/olympus/backend/tmux"
	"github.com/husniadil/olympus/backend/zmx"
)

// Version floors (behavior §0.5). A version probe costs a subprocess, so it is
// deliberately NOT part of the hot-path preflight: it runs here, and at the
// specific call sites where a below-floor version would misbehave silently
// rather than error.
var floors = map[backend.Name]string{
	// allow-passthrough.
	backend.Tmux: "3.3",
	// The reference version; support is best-effort.
	backend.Zmx: "0.6.0",
	// The OLDEST version this backend was measured against, not the newest it
	// works on: the -F format fields it parses and the resize behaviour its
	// client sizing compensates for were checked here. Support below it is
	// best-effort because none of those were.
	//
	// Deliberately not raised when 0.0.26 stopped requiring a client for
	// ordinary input (§2.10). Injection attempts the operation and attaches
	// only on refusal, so it is already correct on both, and raising the floor
	// would buy nothing: copy mode still refuses without a client, and Follow
	// attaches one on every version.
	backend.Meja: "0.0.25",
	// The version every measurement behind this backend was taken against:
	// the pane, workspace and terminal-session verbs it drives, the error
	// codes it classifies, the raw-byte injection its key vocabulary rests on,
	// and the terminal-id timestamp its created_at is derived from. Support
	// below it is best-effort because nothing was checked there.
	backend.Herdr: "0.8.2",
}

// versionProbeBudget bounds a SINGLE version probe.
//
// The diagnostic must always answer — it is what every backend-unavailable error
// points at (§0.6) — and a probe is a subprocess, which can hang. One was found
// hanging for minutes because the binary on PATH was a version-manager shim that
// reached for the network when HOME changed underneath it. With the caller's
// context inherited unchanged, which is normally Background, `olympus doctor`
// hung with no output and no way to tell which backend was responsible.
//
// Generous rather than tight: printing a version is fast everywhere, so anything
// approaching this is already pathological, and a probe wrongly cut short costs
// only that one backend's version line.
const versionProbeBudget = 10 * time.Second

// A versionProber is the one method the diagnostic needs from a backend, named
// separately so the hang above can be reproduced in a test without a binary that
// hangs.
type versionProber interface {
	Version(context.Context) (string, error)
}

// probeVersion asks a backend its version, under a bound.
//
// The caller's context still wins when it is cancelled first: the budget is a
// backstop against a hung binary, not a floor on how long a cancel takes.
func probeVersion(ctx context.Context, p versionProber) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, versionProbeBudget)
	defer cancel()
	return p.Version(ctx)
}

// A BackendReport is one backend's entry in the diagnostic.
type BackendReport struct {
	Name         backend.Name         `json:"name"`
	Installed    bool                 `json:"installed"`
	Version      string               `json:"version,omitempty"`
	Floor        string               `json:"floor"`
	BelowFloor   bool                 `json:"below_floor"`
	Capabilities backend.Capabilities `json:"capabilities"`
	// Isolation says where this backend's sessions live, because the posture
	// differs sharply between backends and a user who learns one will be
	// surprised by the other (behavior §17.2).
	Isolation string `json:"isolation"`
	// Problem explains why a backend that is on PATH could not be asked its
	// version.
	//
	// "On PATH" and "runnable" are different things, and a version-manager shim
	// left behind by an uninstalled tool is the case that proves it: it
	// satisfies a lookup and fails every call. Resolution stays a single lookup
	// with no subprocess (§0.2) and so cannot tell the difference; the
	// diagnostic already spends subprocesses and explaining exactly this is its
	// job. An empty version alone left the reader to guess between "not
	// runnable", "too slow to answer" and "never asked".
	Problem string `json:"problem,omitempty"`
	// Managed is every option Olympus pins on servers it starts, overriding
	// whatever the operator's configuration says.
	//
	// Disclosed rather than merely done. A socket of our own is not a
	// configuration of our own — the operator's tmux.conf reaches our sessions
	// — so a handful of options the protocol's correctness rests on are pinned
	// back. Doing that silently turns "my tmux.conf is being ignored" into an
	// unanswerable question, which is the exact failure this diagnostic exists
	// to prevent (behavior §17.5).
	Managed map[string]string `json:"managed_options,omitempty"`
}

// A ResolvedReport is which backend answers right now, and why.
type ResolvedReport struct {
	Backend backend.Name `json:"backend"`
	Reason  Reason       `json:"reason"`
	Scope   string       `json:"socket_or_dir"`
	// Pinned says whether Olympus configured the server answering right now.
	//
	// False means a server somebody else started. Olympus configures only
	// servers it starts, because pinning reaches every session on a server and
	// would change ones the caller never asked about (§17.5) — so on a found
	// server the settings below are whatever that server was given, and a
	// default-command among them can make a run report the wrong exit code.
	Pinned bool `json:"pinned"`
	// Effective is what the managed options are actually set to right now, as
	// opposed to what Olympus would pin. It is empty when no server is running.
	Effective map[string]string `json:"effective_options,omitempty"`
	// Problem explains why nothing resolved, when nothing did.
	Problem string `json:"problem,omitempty"`
}

// A Diagnosis is the whole diagnostic.
//
// It is a first-class part of the contract, not a debugging aid: it is what
// every backend-unavailable error points at, and it is what turns "it does not
// work on my machine" into one command's output (behavior §0.6).
type Diagnosis struct {
	Resolved     ResolvedReport  `json:"resolved"`
	Backends     []BackendReport `json:"backends"`
	InstallHints []string        `json:"install_hints"`
}

// Diagnose reports the environment, without side effects.
//
// It deliberately returns no error. A diagnostic that fails when nothing is
// installed is useless at exactly the moment it is most needed — that is the
// case it exists to explain.
func Diagnose(ctx context.Context, opts ...Option) Diagnosis {
	cfg := config{installs: onPath, env: os.Getenv(BackendEnv)}
	for _, opt := range opts {
		opt(&cfg)
	}

	// Both slices start non-nil so an empty one marshals as [] rather than
	// null (api §2). A caller handling both shapes for one field is writing two
	// parsers, and install_hints is empty in the ordinary case — everything
	// installed — so null was what most callers actually saw.
	diagnosis := Diagnosis{
		Backends:     []BackendReport{},
		InstallHints: []string{},
	}
	for _, name := range preference {
		report := BackendReport{Name: name, Floor: floors[name], Installed: cfg.installs(name)}
		if !report.Installed {
			diagnosis.InstallHints = append(diagnosis.InstallHints,
				string(name)+": "+installHint(name))
			diagnosis.Backends = append(diagnosis.Backends, report)
			continue
		}

		b, scope := buildBackend(name, cfg)
		report.Capabilities = b.Capabilities()
		report.Isolation = isolationOf(name, scope)
		report.Managed = managedOf(name)
		version, err := probeVersion(ctx, b)
		if err == nil {
			report.Version = version
			report.BelowFloor = belowFloor(version, floors[name])
		}
		describeProbeFailure(&report, err)
		diagnosis.Backends = append(diagnosis.Backends, report)
	}

	resolution, err := resolve(cfg.explicit, cfg.env, cfg.installs)
	if err != nil {
		diagnosis.Resolved.Problem = err.Error()
		return diagnosis
	}
	b, scope := buildBackend(resolution.Backend, cfg)
	diagnosis.Resolved = ResolvedReport{
		Backend: resolution.Backend,
		Reason:  resolution.Reason,
		Scope:   scope,
	}
	diagnosis.Resolved.Effective, diagnosis.Resolved.Pinned = effectiveOf(ctx, b)
	return diagnosis
}

func buildBackend(name backend.Name, cfg config) (backend.Backend, string) {
	switch name {
	case backend.Herdr:
		var options []herdr.Option
		if cfg.socketPath != "" {
			options = append(options, herdr.WithSocketPath(cfg.socketPath))
		}
		built := herdr.New(options...)
		return built, built.Scope()
	case backend.Meja:
		var options []meja.Option
		if cfg.socketPath != "" {
			options = append(options, meja.WithSocketPath(cfg.socketPath))
		}
		built := meja.New(options...)
		return built, built.Scope()
	case backend.Tmux:
		socket := cfg.socket
		if socket == "" {
			socket = tmux.DefaultSocket
		}
		options := []tmux.Option{tmux.WithSocket(socket)}
		if cfg.socketPath != "" {
			options = append(options, tmux.WithSocketPath(cfg.socketPath))
		}
		built := tmux.New(options...)
		return built, built.Scope()
	default:
		var options []zmx.Option
		if cfg.zmxDir != "" {
			options = append(options, zmx.WithDir(cfg.zmxDir))
		}
		b := zmx.New(options...)
		scope := cfg.zmxDir
		if scope == "" {
			scope = os.Getenv("ZMX_DIR")
		}
		return b, scope
	}
}

// managedOf reports what Olympus pins, for the backend that has a configuration
// file to be pinned against.
func managedOf(name backend.Name) map[string]string {
	if name == backend.Herdr {
		// herdr is the one backend whose configuration follows its
		// configuration DIRECTORY, which this backend moves alongside the
		// socket — so what is pinned here is a file Olympus owns rather than
		// the operator's. It is disclosed anyway: both entries turn off a
		// background network check the server would otherwise run at boot, and
		// a tool that silently decides when a program may reach the network
		// turns "why did this call home" into an unanswerable question
		// (§17.5).
		return herdr.ManagedConfig()
	}
	if name != backend.Tmux {
		return nil
	}
	pinned := map[string]string{}
	for _, option := range tmux.ManagedOptions() {
		pinned[option[0]] = option[1]
	}
	return pinned
}

// effectiveOf reads what the managed options are actually set to, and whether
// they are Olympus's own values.
//
// Read rather than assumed: the whole point of the distinction is that on a
// server Olympus did not start, what Olympus WOULD pin and what is in force are
// different things, and only the second one decides how a run behaves.
func effectiveOf(ctx context.Context, b backend.Backend) (map[string]string, bool) {
	reader, ok := b.(interface {
		EffectiveOptions(context.Context) (map[string]string, bool, error)
	})
	if !ok {
		return nil, false
	}
	// Bounded for the same reason as the version probe: this too shells out, and
	// the diagnostic's whole value is that it answers when the environment is
	// broken. A server that accepts a connection and then never replies would
	// otherwise hang the command sent to explain it.
	ctx, cancel := context.WithTimeout(ctx, versionProbeBudget)
	defer cancel()
	effective, pinned, err := reader.EffectiveOptions(ctx)
	if err != nil {
		// A diagnostic that fails when the environment is broken is useless at
		// the moment it is most needed. No server running is the ordinary case
		// here, not an error worth reporting as one.
		return nil, false
	}
	return effective, pinned
}

// isolationOf states the posture in the user's terms. The two are opposite and
// both are surprising if you learned the other first (behavior §17.2).
func isolationOf(name backend.Name, scope string) string {
	switch name {
	case backend.Herdr:
		// Worth stating separately from tmux's and meja's: pointing the socket
		// somewhere private is not enough on its own here, because herdr keeps
		// a session's persisted layout in its configuration directory rather
		// than beside the socket. Olympus moves that directory with the socket,
		// and this says where it went.
		return "socket at " + scope + "; its configuration and saved layout live beside it, invisible to your own herdr"
	case backend.Meja:
		if strings.HasPrefix(scope, "/") {
			// Worth stating separately from tmux's: meja keeps each server's
			// session RECOVERY files beside its socket, so the path decides
			// where persisted sessions land as well as which server answers.
			return "socket at " + scope + "; its saved sessions live beside it, invisible to your default meja"
		}
		return "your default meja profile in ~/.meja/default; these sessions appear in your own `meja ls` and are saved into your own store"
	case backend.Tmux:
		if strings.HasPrefix(scope, "/") {
			return "socket at " + scope + "; these sessions are invisible to any tmux not pointed at that path"
		}
		return "private socket " + strconv.Quote(scope) + "; these sessions do not appear in a plain `tmux ls`"
	default:
		where := scope
		if where == "" {
			where = "the default directory for this user"
		}
		return "shared daemon in " + where + "; these sessions appear in your own `zmx list` alongside everything else"
	}
}

// belowFloor compares a reported version against a floor, leniently.
//
// Versions here carry suffixes a strict parser would reject ("3.7b"), and a
// diagnostic that errors on an unfamiliar version string is worse than one that
// declines to judge it: an unparseable version is reported as-is and NOT
// flagged, since claiming a working backend is below its floor would send a
// user chasing the wrong problem.
func belowFloor(version, floor string) bool {
	got, ok := parseVersion(version)
	if !ok {
		return false
	}
	want, ok := parseVersion(floor)
	if !ok {
		return false
	}
	for i := range want {
		if got[i] != want[i] {
			return got[i] < want[i]
		}
	}
	return false
}

// parseVersion reads the leading numeric components, ignoring any suffix.
func parseVersion(version string) ([3]int, bool) {
	var out [3]int
	fields := strings.SplitN(strings.TrimSpace(version), ".", 4)
	if len(fields) == 0 || fields[0] == "" {
		return out, false
	}
	for i := 0; i < len(fields) && i < 3; i++ {
		digits := fields[i]
		end := 0
		for end < len(digits) && digits[end] >= '0' && digits[end] <= '9' {
			end++
		}
		if end == 0 {
			if i == 0 {
				return out, false
			}
			break
		}
		n, err := strconv.Atoi(digits[:end])
		if err != nil {
			return out, false
		}
		out[i] = n
	}
	return out, true
}

// describeProbeFailure records why a version could not be read, and makes sure a
// failed probe leaves no version behind to be believed.
func describeProbeFailure(report *BackendReport, err error) {
	if err == nil {
		return
	}
	report.Version = ""
	report.BelowFloor = false
	report.Problem = "on PATH but could not be run: " + err.Error()
}
