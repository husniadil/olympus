package cli

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/husniadil/olympus"
	"github.com/husniadil/olympus/backend"
)

// The envelope is the widest wire surface in the repo: it wraps the payload of
// every verb on both structured doors, so one mis-typed tag here changes all of
// them at once and no per-verb test would object. Every expected string is
// transcribed from api §2.

func assertEnvelopeJSON(t *testing.T, value any, want string) {
	t.Helper()
	got, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if string(got) != want {
		t.Errorf("marshalled to\n\t%s\nwant\n\t%s", got, want)
	}
}

func TestTheSuccessEnvelopeMatchesTheSpecShape(t *testing.T) {
	assertEnvelopeJSON(t, successEnvelope(backend.Zmx, map[string]string{"session": "build"},
		[]olympus.Warning{{Code: "DEGRADED", Message: "current_path is the spawn directory"}}),
		`{"ok":true,"backend":"zmx","data":{"session":"build"},`+
			`"warnings":[{"code":"DEGRADED","message":"current_path is the spawn directory"}]}`)
}

func TestTheFailureEnvelopeMatchesTheSpecShape(t *testing.T) {
	assertEnvelopeJSON(t, failureEnvelope(backend.Zmx, backend.Errorf(backend.CodeSessionNotFound, `session "build" not found`)),
		`{"ok":false,"backend":"zmx","error":{"code":"SESSION_NOT_FOUND","message":"session \"build\" not found"}}`)
}

// §5 "Status": the same two fields in all three modes — read, --set, --wait — so
// a caller parsing the output needs one parser rather than three.
//
// `status` has no `omitempty` on purpose: §5 says it is "empty when the session
// has never reported one", and an empty string is that answer. Dropping the key
// would make never-reported indistinguishable from a shape the caller failed to
// parse.
func TestTheStatusReportMatchesTheSpecShape(t *testing.T) {
	assertEnvelopeJSON(t, StatusReport{Session: "build", Status: "blocked"},
		`{"session":"build","status":"blocked"}`)
	assertEnvelopeJSON(t, StatusReport{Session: "build"},
		`{"session":"build","status":""}`)
}

// §2: "`ok` is always present and is the only field a consumer needs to branch
// on."
//
// It is the one field with no `omitempty`, deliberately: `false` is the failure
// signal, and a tag that dropped it would make every failure look like a success
// with a missing key to a consumer reading `ok` as falsy-by-absence — which is
// how it reads in most languages, so the bug would be invisible until a failure.
func TestOKSurvivesBeingFalse(t *testing.T) {
	assertEnvelopeJSON(t, Envelope{}, `{"ok":false}`)
}

// §2: "`warnings` is omitted when empty, never `null`", and "`data` ... is
// absent for operations with no payload."
//
// A verb that reports nothing must not emit `"data":null`: null is a value, and
// a consumer that reads it as a payload finds a payload that is not there.
func TestAPayloadlessSuccessCarriesNeitherDataNorWarnings(t *testing.T) {
	assertEnvelopeJSON(t, successEnvelope(backend.Tmux, nil, nil), `{"ok":true,"backend":"tmux"}`)
	assertEnvelopeJSON(t, successEnvelope(backend.Tmux, nil, []olympus.Warning{}), `{"ok":true,"backend":"tmux"}`)
}

// §2: "`error` ... carries a code from the behavior spec's §12 vocabulary."
//
// An error with no code attached must still name one. `UNEXPECTED` is the
// vocabulary's answer for "we do not know", and an empty string is not in the
// vocabulary at all — a consumer switching on the code would fall through every
// case and reach whatever its default does, which is usually nothing.
func TestAnUncodedErrorStillCarriesAVocabularyCode(t *testing.T) {
	assertEnvelopeJSON(t, failureEnvelope(backend.Zmx, errors.New("something went wrong")),
		`{"ok":false,"backend":"zmx","error":{"code":"UNEXPECTED","message":"something went wrong"}}`)
}

// §2: "`backend` is the resolved backend ... Present on success and failure
// alike."
//
// The one case where it is legitimately absent is a failure that happened
// BEFORE resolution — nothing answered, so there is no resolved backend to name,
// and inventing one would report that a backend was tried when none was. Pinned
// so the exception stays that narrow.
func TestOnlyAnUnresolvedFailureOmitsTheBackend(t *testing.T) {
	assertEnvelopeJSON(t, failureEnvelope("", backend.Errorf(backend.CodeBackendUnavailable, "no backend is installed")),
		`{"ok":false,"error":{"code":"BACKEND_UNAVAILABLE","message":"no backend is installed"}}`)
}
