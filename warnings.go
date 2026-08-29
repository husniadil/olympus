package olympus

import (
	"fmt"

	"github.com/husniadil/olympus/backend"
)

// WarningDegraded is the code every degraded-operation disclosure carries.
const WarningDegraded = "DEGRADED"

// A Warning is a disclosure attached to a successful result.
//
// These are not errors — the operation returned something real — but a caller
// unaware of the difference draws a wrong conclusion from a success (behavior
// §0.8). They are distinct from UNSUPPORTED, which covers an operation the
// backend has no concept of and which returns nothing at all: failing these
// outright would make the default backend refuse work it can genuinely do.
type Warning struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// An operation names something that can degrade.
type operation int

const (
	opPaneListing operation = iota
	opCaptureHistory
	opCapture
	opCaptureMeta
	opPollWindow
	opGracefulKill
	// opSpawnSize is a create that asked for a size the backend cannot set.
	opSpawnSize
)

// degradations lists what silently differs, per backend and operation
// (behavior §0.8).
//
// Announced once per operation, never once per row: a warning per listed pane
// is noise that trains users to ignore the mechanism entirely.
var degradations = map[backend.Name]map[operation][]string{
	backend.Zmx: {
		opPaneListing: {
			"current_path is the directory the session was created in and does not follow cd",
			"current_command is the spawn command, not the process running now",
		},
		opCaptureHistory: {
			"a history request is accepted and changes nothing: captures already return full scrollback",
		},
		opCapture: {
			"a line wrapped at the terminal's width cannot be rejoined, so it comes back split",
		},
		opCaptureMeta: {
			"alt-screen and scroll position are not tracked and are always zero",
		},
		opPollWindow: {
			"the requested capture window is ignored: scrollback depth is the backend's own",
		},
		opGracefulKill: {
			"a session started directly on a command cannot be interrupted and will be force-killed",
		},
		opSpawnSize: {
			"the requested size is ignored: a session takes its size from the client that attaches it",
		},
	},
	// Listed for the SAME gaps zmx declares, because the gap is what a caller
	// reacts to and it does not become smaller on a different backend. Leaving
	// these out was the inconsistency: meja's capabilities said alt-screen was
	// untracked exactly as zmx's did, and only zmx said so at the call.
	backend.Meja: {
		opCaptureMeta: {
			"alt-screen and scroll position are not tracked and are always zero",
		},
		opSpawnSize: {
			"the requested size is ignored: meja sizes a session from its first client",
		},
	},
	// Listed for the gaps herdr shares with the others, and for two it has on
	// its own. The alt-screen entry is narrower than zmx's and meja's on
	// purpose: herdr's scroll position IS real, and repeating their wording
	// would tell a caller a true field is not tracked.
	backend.Herdr: {
		opPaneListing: {
			"current_command is reported for a targeted pane listing only; a whole-server listing leaves it empty",
			"attached is always false: no per-terminal client count is reported",
		},
		opCapture: {
			"a line wrapped at the terminal's width cannot be rejoined, so it comes back split",
		},
		opCaptureMeta: {
			"alt-screen is not tracked and is always false",
		},
		opSpawnSize: {
			"the requested size is ignored: a pane takes the server's own geometry until a client attaches",
		},
	},
}

// depthCaps is the deepest history a backend will return, for the backends that
// have a ceiling at all.
//
// It is separate from degradations because the disclosure is CONDITIONAL: a
// backend with a cap still honours every request below it, and warning on all
// of them would be exactly the noise §0.8 rules out — as well as untrue, since
// nothing about that answer is narrower than what was asked for. A request
// above the cap really does come back short, and only that one is disclosed.
var depthCaps = map[backend.Name]int{
	// The server clamps silently at 1,000 lines, so Olympus clamps there too
	// and says when a request was reduced (§6.4).
	backend.Herdr: 1000,
}

// warnDepth discloses a history or poll-window request that will come back
// shorter than it was written.
func warnDepth(name backend.Name, lines int) []Warning {
	limit, has := depthCaps[name]
	if !has || lines <= limit {
		return nil
	}
	return []Warning{{
		Code: WarningDegraded,
		Message: fmt.Sprintf(
			"%d lines were asked for and %d is the deepest this backend returns, so the answer is %d lines",
			lines, limit, limit),
	}}
}

// warn returns the disclosures for an operation on a backend.
func warn(name backend.Name, op operation) []Warning {
	messages := degradations[name][op]
	if len(messages) == 0 {
		return nil
	}
	out := make([]Warning, 0, len(messages))
	for _, message := range messages {
		out = append(out, Warning{Code: WarningDegraded, Message: message})
	}
	return out
}

// SizeWarnings are the disclosures that apply to asking for a session size.
//
// Exposed on the handle rather than returned from Create because a caller often
// wants to know BEFORE creating anything — the answer is a property of the
// resolved backend, not of any one session — and because Create already returns
// a Session rather than a result envelope. Feature-probing Capabilities is the
// other half: the capability says whether to ask, this says what happened.
func (o *Olympus) SizeWarnings() []Warning {
	if o.backend.Capabilities().SpawnSizing {
		return nil
	}
	return warn(o.resolution.Backend, opSpawnSize)
}
