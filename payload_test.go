package olympus_test

import (
	"encoding/json"
	"testing"

	"github.com/husniadil/olympus"
	"github.com/husniadil/olympus/backend"
)

// Every expected string in this file is transcribed from the payload shapes of
// api §5, which is the independent source of truth — not read back off a
// marshalled value, which would only assert that the code agrees with itself.
//
// CLAUDE.md non-negotiable #3 makes these shapes semver-bound: a shipped field
// is never renamed, retyped or removed. Five types were already pinned in
// backend/types_test.go; the rest were not, so a rename would have travelled to
// every consumer with nothing objecting. These are the rest.
//
// A diff here is a deliberate decision about the wire contract. It is never a
// refactor's side effect, and it is never fixed by updating the want string
// until §5 has been changed first.

func assertJSON(t *testing.T, value any, want string) {
	t.Helper()
	got, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if string(got) != want {
		t.Errorf("marshalled to\n\t%s\nwant\n\t%s", got, want)
	}
}

// §5 "Presence": `{ "state": "present", "session": { }, "panes": [ ], "capabilities": { } }`.
func TestInfoMarshalsToTheSpecShape(t *testing.T) {
	assertJSON(t, olympus.Info{
		State:        backend.StatePresent,
		Session:      &backend.Session{Name: "build", ID: "$3", Liveness: backend.LivenessPresent, CWD: "/repo"},
		Panes:        []backend.Pane{},
		Capabilities: backend.Capabilities{Backend: backend.Zmx},
	}, `{"state":"present","session":{"name":"build","id":"$3","attached":false,"dead":false,"liveness":"present","cwd":"/repo"},"panes":[],"capabilities":{"native_scrollback":false,"views":false,"remain_on_exit":false,"server_env":false,"control_keys":false,"spawn_sizing":false,"session_status":false,"tracks_alt_screen":false}}`)
}

// §5: "`session` and `panes` are omitted when the target is not present."
//
// Omitted, not null and not empty: an absent target that still carried a
// session object would answer the presence question twice, in disagreement.
func TestAbsentInfoCarriesNoSessionOrPanes(t *testing.T) {
	assertJSON(t, olympus.Info{
		State:        backend.StateAbsent,
		Capabilities: backend.Capabilities{Backend: backend.Zmx},
	}, `{"state":"absent","capabilities":{"native_scrollback":false,"views":false,"remain_on_exit":false,"server_env":false,"control_keys":false,"spawn_sizing":false,"session_status":false,"tracks_alt_screen":false}}`)
}

// §5 "Screen": one call carries several targets, keyed by target name.
func TestScreensMarshalToTheSpecShape(t *testing.T) {
	assertJSON(t, olympus.Screens{
		Screens: map[string]string{"build": "hello"},
		Meta:    map[string]backend.ScreenMeta{"build": {}},
	}, `{"screens":{"build":"hello"},"meta":{"build":{"alt_screen":false,"scroll_position":0}}}`)
}

// §5: "Both maps are always objects, never `null`, including on the zero value a
// failure returns."
//
// A consumer that indexes the map without a nil check is the normal way to write
// this, in every language with a JSON object. `null` makes that a crash on the
// error path — the path least likely to have been exercised.
func TestZeroScreensMarshalAsEmptyObjectsNotNull(t *testing.T) {
	assertJSON(t, olympus.Screens{}, `{"screens":{},"meta":{}}`)
}

// §5 "Wait": the whole Screen is the payload of `wait`, so `line` and `matched`
// are wire fields, not internals.
//
// Like Stopped, this shape was shipped and semver-bound with no entry in §5 at
// all. The spec gained one in the same commit as this test.
func TestScreenMarshalsToTheSpecShape(t *testing.T) {
	assertJSON(t, olympus.Screen{
		Text:    "$ make build\n",
		Line:    "$ make build",
		Matched: true,
	}, `{"text":"$ make build\n","meta":{"alt_screen":false,"scroll_position":0},"line":"$ make build","matched":true}`)
}

// A capture that matched nothing carries neither field. `matched: false` and an
// absent key mean the same thing to the only question a caller asks of this
// payload, and §5 documents the pair as the match report rather than as always-
// present state.
func TestAnUnmatchedScreenCarriesNoLine(t *testing.T) {
	assertJSON(t, olympus.Screen{Text: "waiting\n"},
		`{"text":"waiting\n","meta":{"alt_screen":false,"scroll_position":0}}`)
}

// §5 "Run": `{"exit_code": 0, "output": "…"}`.
func TestResultMarshalsToTheSpecShape(t *testing.T) {
	assertJSON(t, olympus.Result{ExitCode: 0, Output: "done\n"},
		`{"exit_code":0,"output":"done\n"}`)
}

// §5 "Detached run": poll returns status, and exit_code ONLY when completed.
func TestPollResultMarshalsToTheSpecShape(t *testing.T) {
	code := 0
	assertJSON(t, olympus.PollResult{State: "completed", ExitCode: &code, Output: "done\n"},
		`{"status":"completed","exit_code":0,"output":"done\n"}`)
}

// §5: "`exit_code` is omitted unless `status` is `completed` — never a fake
// zero."
//
// This is the single most consequential omission in the contract. A pending run
// reporting `exit_code: 0` reads as success to any consumer that checks the code
// before the status, which is the obvious order to write the check in. Pinned
// for both non-completed states because they arrive by different paths.
func TestAnUnfinishedPollCarriesNoExitCode(t *testing.T) {
	assertJSON(t, olympus.PollResult{State: "pending"}, `{"status":"pending"}`)
	assertJSON(t, olympus.PollResult{State: "died", Reason: "the session is gone"},
		`{"status":"died","reason":"the session is gone"}`)
}

// The zero value must not marshal an exit code either. A caller that builds one
// and forgets to populate it is a bug; one that ships `exit_code: 0` from it is
// a bug that reports success.
func TestTheZeroPollResultCarriesNoExitCode(t *testing.T) {
	assertJSON(t, olympus.PollResult{}, `{"status":""}`)
}

// §5 "Identity": inside is always present; the rest is omitted when it cannot be
// answered.
func TestIdentityMarshalsToTheSpecShape(t *testing.T) {
	assertJSON(t, olympus.Identity{
		Inside:  true,
		Backend: backend.Tmux,
		Session: "build",
		Scope:   "/tmp/olympus.sock",
	}, `{"inside":true,"backend":"tmux","session":"build","scope":"/tmp/olympus.sock"}`)
}

// §5: "Being outside a session exits 0 with `inside: false`."
//
// False is an answer. It must therefore survive marshalling — an `omitempty` on
// this field would turn "definitely nowhere" into a missing key, which is what
// "could not tell" looks like.
func TestBeingOutsideIsAnAnswerNotAnAbsence(t *testing.T) {
	assertJSON(t, olympus.Identity{}, `{"inside":false}`)
}

// §5: "When `nested` is set, `backend`, `session` and `scope` are all empty."
//
// The refusal is the payload. A nested identity that also carried a best guess
// would be acted on, and delivers somebody else's reply to the wrong terminal.
func TestANestedIdentityNamesTheClaimantsAndNothingElse(t *testing.T) {
	assertJSON(t, olympus.Identity{
		Inside: true,
		Nested: []backend.Name{backend.Zmx, backend.Tmux},
	}, `{"inside":true,"nested":["zmx","tmux"]}`)
}

// §6.2: a degraded result carries warnings rather than failing. Their two fields
// are as wire-visible as any payload's — they are what a caller renders when an
// operation half-worked.
func TestWarningMarshalsToTheSpecShape(t *testing.T) {
	assertJSON(t, olympus.Warning{Code: "degraded_cwd", Message: "cwd is the spawn directory"},
		`{"code":"degraded_cwd","message":"cwd is the spawn directory"}`)
}

// §5 "Stop": outcome is gone, graceful or killed, and all three are successes.
//
// §5 did not document this shape at all until this test was written — it was
// shipped, semver-bound and unwritten. The spec gained the entry in the same
// commit.
func TestStoppedMarshalsToTheSpecShape(t *testing.T) {
	for _, outcome := range []string{"gone", "graceful", "killed"} {
		assertJSON(t, olympus.Stopped{Outcome: outcome}, `{"outcome":"`+outcome+`"}`)
	}
}

// §5 "Doctor". The nesting is the point: a diagnostic whose shape drifts is
// still readable by a human and silently unparseable by the tooling that
// collects it, which is the only consumer that reads it at scale.
func TestDiagnosisMarshalsToTheSpecShape(t *testing.T) {
	assertJSON(t, olympus.Diagnosis{
		Resolved: olympus.ResolvedReport{
			Backend: backend.Zmx,
			Reason:  olympus.ReasonDefault,
			Scope:   "/tmp/zmx-501",
		},
		Backends: []olympus.BackendReport{{
			Name:      backend.Zmx,
			Installed: true,
			Version:   "0.6.0",
			Floor:     "0.6.0",
			Isolation: "ZMX_DIR",
		}},
		InstallHints: []string{},
	}, `{"resolved":{"backend":"zmx","reason":"default","socket_or_dir":"/tmp/zmx-501","pinned":false},`+
		`"backends":[{"name":"zmx","installed":true,"version":"0.6.0","floor":"0.6.0","below_floor":false,`+
		`"capabilities":{"native_scrollback":false,"views":false,"remain_on_exit":false,"server_env":false,`+
		`"control_keys":false,"spawn_sizing":false,"session_status":false,"tracks_alt_screen":false},`+
		`"isolation":"ZMX_DIR"}],"install_hints":[]}`)
}

// §5: `managed_options` and `effective_options` answer two different questions —
// what Olympus would pin, and what the server answering right now is actually
// set to. A backend that pins nothing must omit both rather than send `null`,
// because the empty object and the absent key mean the same thing here and
// `null` means neither.
func TestPinningReportsAppearOnlyWhereThereIsPinning(t *testing.T) {
	assertJSON(t, olympus.BackendReport{
		Name:      backend.Tmux,
		Installed: true,
		Floor:     "3.3",
		Managed:   map[string]string{"history-limit": "50000"},
	}, `{"name":"tmux","installed":true,"floor":"3.3","below_floor":false,`+
		`"capabilities":{"native_scrollback":false,"views":false,"remain_on_exit":false,"server_env":false,`+
		`"control_keys":false,"spawn_sizing":false,"session_status":false,"tracks_alt_screen":false},`+
		`"isolation":"","managed_options":{"history-limit":"50000"}}`)

	assertJSON(t, olympus.ResolvedReport{Backend: backend.Zmx, Reason: olympus.ReasonFallback, Scope: "/tmp/z"},
		`{"backend":"zmx","reason":"fallback","socket_or_dir":"/tmp/z","pinned":false}`)
}

// §4 spells the resolution reasons out as a closed set. They are wire values a
// caller branches on to explain itself to a user, so they are as bound as any
// field name.
func TestResolutionReasonSpellings(t *testing.T) {
	spellings := []struct{ name, got, want string }{
		{"ReasonFlag", string(olympus.ReasonFlag), "flag"},
		{"ReasonEnv", string(olympus.ReasonEnv), "env"},
		{"ReasonDefault", string(olympus.ReasonDefault), "default"},
		{"ReasonFallback", string(olympus.ReasonFallback), "fallback"},
	}
	for _, s := range spellings {
		if s.got != s.want {
			t.Errorf("%s is %q, want %q", s.name, s.got, s.want)
		}
	}
}
