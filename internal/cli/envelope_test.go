package cli

import (
	"encoding/json"
	"errors"
	"strings"
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
// §2: `error.typed` is present only on a refusal whose text was typed.
func TestATypedRefusalCarriesTyped(t *testing.T) {
	typed := backend.Errorf(backend.CodeAgentBlocked, "typed then blocked")
	typed.Typed = true
	assertEnvelopeJSON(t, failureEnvelope(backend.Herdr, typed),
		`{"ok":false,"backend":"herdr","error":{"code":"AGENT_BLOCKED","message":"typed then blocked","typed":true}}`)
	assertEnvelopeJSON(t, failureEnvelope(backend.Herdr, backend.Errorf(backend.CodeAgentBlocked, "blocked")),
		`{"ok":false,"backend":"herdr","error":{"code":"AGENT_BLOCKED","message":"blocked"}}`)
}

// §2: `error.reason` is present only on a refusal that has one.
func TestARefusalWithAReasonCarriesIt(t *testing.T) {
	hidden := backend.Errorf(backend.CodeAgentBlocked, "no input box")
	hidden.Reason = backend.ReasonNoInputBox
	assertEnvelopeJSON(t, failureEnvelope(backend.Herdr, hidden),
		`{"ok":false,"backend":"herdr","error":{"code":"AGENT_BLOCKED","message":"no input box","reason":"no_input_box"}}`)
}

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

// behavior §6.10: a throwaway run that fails and then fails to clean up reports
// both. The run's error is the failure, and the cleanup failure rides beside it,
// as a warning in the envelope and as narration on stderr.
func TestAFailureKeepsTheWarningsThatCameWithIt(t *testing.T) {
	cleanup := []olympus.Warning{{Code: olympus.WarningDegraded, Message: "the throwaway session x could not be cleaned up: gone"}}
	failed := withWarnings(backend.Errorf(backend.CodeTimeout, "timed out"), cleanup)

	var out, errOut strings.Builder
	(&App{Out: &out, Err: &errOut, json: true, resolved: backend.Tmux}).report(failed)
	if got, want := out.String(), `{"ok":false,"backend":"tmux","warnings":[{"code":"DEGRADED","message":"the throwaway session x could not be cleaned up: gone"}],"error":{"code":"TIMEOUT","message":"timed out"}}`+"\n"; got != want {
		t.Errorf("--json failure =\n\t%s\nwant\n\t%s", got, want)
	}

	out.Reset()
	(&App{Out: &out, Err: &errOut}).report(failed)
	if got, want := errOut.String(), "warning: the throwaway session x could not be cleaned up: gone\nolympus: timed out\n"; got != want {
		t.Errorf("human failure on stderr = %q, want %q", got, want)
	}
	if out.Len() != 0 {
		t.Errorf("human failure wrote %q to stdout", out.String())
	}
	if backend.CodeOf(failed) != backend.CodeTimeout {
		t.Errorf("code = %s, want the run's own TIMEOUT", backend.CodeOf(failed))
	}
	if withWarnings(errors.New("x"), nil).Error() != "x" {
		t.Error("an error with no warnings must read as itself")
	}
}
