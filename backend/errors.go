// Package backend defines the mechanical layer: the vocabulary every terminal
// multiplexer backend speaks, and the interface it implements.
//
// The rules this package encodes are specified in docs/terminal-behavior.md,
// and the shapes it marshals are specified in docs/api.md. Both are normative;
// this package is where their vocabulary becomes types.
package backend

// A Code classifies a failure. The set of codes and their process exit statuses
// are a semver-bound contract (behavior §12): a shipped code is never
// repurposed or removed, only added to.
type Code string

const (
	// CodeUsage is input the caller could have validated, including an unknown
	// backend name. Any error a caller could have avoided by changing one
	// argument is this code and not CodeUnexpected.
	CodeUsage Code = "USAGE"
	// CodeSessionNotFound is a target session or pane that does not exist.
	CodeSessionNotFound Code = "SESSION_NOT_FOUND"
	// CodeBackendUnavailable is a selected backend that cannot be reached. It
	// is distinct from CodeUnsupported: the concept exists, the backend does
	// not answer.
	CodeBackendUnavailable Code = "BACKEND_UNAVAILABLE"
	// CodeTimeout is an operation that did not complete or match before its
	// budget elapsed.
	CodeTimeout Code = "TIMEOUT"
	// CodeConflict is a lock or attach slot held by someone else.
	CodeConflict Code = "CONFLICT"
	// CodeUnsupported is a backend with no concept for the operation at all.
	// It is neither "unavailable" nor "absent": absence is a real negative
	// answer, unsupported means the question does not apply.
	CodeUnsupported Code = "UNSUPPORTED"
	// CodeUnexpected is anything not carrying one of the above — read by a
	// machine consumer as "Olympus broke, retrying will not help".
	CodeUnexpected Code = "UNEXPECTED"
)

// codes lists every declared code in vocabulary order. It is the single place a
// new code is registered, so nothing can ship a code without an exit status.
var codes = []Code{
	CodeUsage,
	CodeSessionNotFound,
	CodeBackendUnavailable,
	CodeTimeout,
	CodeConflict,
	CodeUnsupported,
	CodeUnexpected,
}

// exitCodes maps each code to its process exit status (behavior §12).
var exitCodes = map[Code]int{
	CodeUsage:              2,
	CodeSessionNotFound:    3,
	CodeBackendUnavailable: 4,
	CodeTimeout:            5,
	CodeConflict:           6,
	CodeUnsupported:        7,
	CodeUnexpected:         1,
}

// Codes returns every declared code, in vocabulary order. Doors use it to
// enumerate the vocabulary — in help text, in a schema enum — without
// duplicating the list.
func Codes() []Code {
	out := make([]Code, len(codes))
	copy(out, codes)
	return out
}

// ExitCode returns the process exit status for a code. An unrecognised code is
// CodeUnexpected's status, since a code this build does not know is precisely
// the "Olympus broke" case.
//
// This mapping translates failures and nothing else. The two operations whose
// exit status deviates — run, which reports the command's own status, and
// attach, which reports its client's — handle that locally at their door
// (behavior §12.1). Teaching it here would leak run-specific meaning into code
// that every operation shares.
func ExitCode(code Code) int {
	if exit, ok := exitCodes[code]; ok {
		return exit
	}
	return exitCodes[CodeUnexpected]
}
