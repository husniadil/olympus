package backend_test

import (
	"testing"

	"github.com/husniadil/olympus/backend"
)

// The code-to-exit table is transcribed from behavior spec §12, which is the
// independent source of truth. It is semver-bound: an entry here changing value
// is a breaking change, and this test is where that is noticed.
var specTable = map[backend.Code]int{
	backend.CodeUsage:              2,
	backend.CodeSessionNotFound:    3,
	backend.CodeBackendUnavailable: 4,
	backend.CodeTimeout:            5,
	backend.CodeConflict:           6,
	backend.CodeUnsupported:        7,
	backend.CodeUnexpected:         1,
}

func TestExitCodeMatchesTheSpecTable(t *testing.T) {
	for code, want := range specTable {
		if got := backend.ExitCode(code); got != want {
			t.Errorf("ExitCode(%q) = %d, want %d", code, got, want)
		}
	}
}

// "unit-tested exhaustively": every code the package declares must appear in the
// table above, so adding a code without deciding its exit status fails here.
func TestEveryDeclaredCodeIsInTheSpecTable(t *testing.T) {
	declared := backend.Codes()
	if len(declared) != len(specTable) {
		t.Errorf("Codes() has %d entries, spec table has %d", len(declared), len(specTable))
	}
	for _, code := range declared {
		if _, ok := specTable[code]; !ok {
			t.Errorf("Codes() includes %q, which the spec table does not map", code)
		}
	}
}

// §12: "UNEXPECTED is what a machine consumer reads as 'Olympus broke'." An
// unrecognised code is exactly that case, and must not be a distinct exit value.
func TestUnrecognisedCodeExitsAsUnexpected(t *testing.T) {
	for _, code := range []backend.Code{"", "NOT_A_CODE", "usage"} {
		if got := backend.ExitCode(code); got != 1 {
			t.Errorf("ExitCode(%q) = %d, want 1", code, got)
		}
	}
}

// The wire spelling is part of the semver-bound vocabulary, not an internal
// enum name — a consumer branches on these strings.
func TestCodeSpellingsAreTheWireSpellings(t *testing.T) {
	spellings := map[backend.Code]string{
		backend.CodeUsage:              "USAGE",
		backend.CodeSessionNotFound:    "SESSION_NOT_FOUND",
		backend.CodeBackendUnavailable: "BACKEND_UNAVAILABLE",
		backend.CodeTimeout:            "TIMEOUT",
		backend.CodeConflict:           "CONFLICT",
		backend.CodeUnsupported:        "UNSUPPORTED",
		backend.CodeUnexpected:         "UNEXPECTED",
	}
	for code, want := range spellings {
		if string(code) != want {
			t.Errorf("code constant is %q, want %q", string(code), want)
		}
	}
}
