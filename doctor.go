package olympus

import (
	"context"
	"os"
	"strconv"
	"strings"

	"github.com/husniadil/olympus/backend"
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
}

// A ResolvedReport is which backend answers right now, and why.
type ResolvedReport struct {
	Backend backend.Name `json:"backend"`
	Reason  Reason       `json:"reason"`
	Scope   string       `json:"socket_or_dir"`
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

	var diagnosis Diagnosis
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
		if version, err := b.Version(ctx); err == nil {
			report.Version = version
			report.BelowFloor = belowFloor(version, floors[name])
		}
		diagnosis.Backends = append(diagnosis.Backends, report)
	}

	resolution, err := resolve(cfg.explicit, cfg.env, cfg.installs)
	if err != nil {
		diagnosis.Resolved.Problem = err.Error()
		return diagnosis
	}
	_, scope := buildBackend(resolution.Backend, cfg)
	diagnosis.Resolved = ResolvedReport{
		Backend: resolution.Backend,
		Reason:  resolution.Reason,
		Scope:   scope,
	}
	return diagnosis
}

func buildBackend(name backend.Name, cfg config) (backend.Backend, string) {
	switch name {
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

// isolationOf states the posture in the user's terms. The two are opposite and
// both are surprising if you learned the other first (behavior §17.2).
func isolationOf(name backend.Name, scope string) string {
	switch name {
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
