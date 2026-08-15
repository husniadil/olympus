package olympus

import "github.com/husniadil/olympus/backend"

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
	},
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
