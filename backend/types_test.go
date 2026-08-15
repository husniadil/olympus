package backend_test

import (
	"encoding/json"
	"testing"

	"github.com/husniadil/olympus/backend"
)

// The expected JSON in this file is transcribed from the payload shapes of
// api §5, which is the independent source of truth. Those field names are
// semver-bound from first release: this test is the thing that notices when a
// rename or a retyping would break every shipped consumer, so a diff here is a
// deliberate decision, never a refactor's side effect.

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

func TestSessionMarshalsToTheSpecShape(t *testing.T) {
	assertJSON(t, backend.Session{
		Name:     "build",
		ID:       "$3",
		Attached: false,
		Dead:     false,
		Liveness: backend.LivenessPresent,
		CWD:      "/repo",
		Outcome:  backend.OutcomeCreated,
	}, `{"name":"build","id":"$3","attached":false,"dead":false,"liveness":"present","cwd":"/repo","outcome":"created"}`)
}

// api §5: "outcome appears only on start". A listing row carries no outcome,
// and must not invent one — an empty string would read as a fourth value.
func TestSessionOmitsOutcomeWhenThereIsNone(t *testing.T) {
	assertJSON(t, backend.Session{
		Name:     "build",
		ID:       "$3",
		Liveness: backend.LivenessPresent,
		CWD:      "/repo",
	}, `{"name":"build","id":"$3","attached":false,"dead":false,"liveness":"present","cwd":"/repo"}`)
}

func TestPaneMarshalsToTheSpecShape(t *testing.T) {
	assertJSON(t, backend.Pane{
		ID:             "%7",
		SessionName:    "build",
		SessionID:      "$3",
		WindowIndex:    0,
		Dead:           false,
		CreatedAt:      1786778830,
		CurrentPath:    "/repo",
		CurrentCommand: "zsh",
		Liveness:       backend.LivenessPresent,
	}, `{"pane_id":"%7","session_name":"build","session_id":"$3","window_index":0,"dead":false,"created_at":1786778830,"current_path":"/repo","current_command":"zsh","liveness":"present"}`)
}

func TestScreenMetaMarshalsToTheSpecShape(t *testing.T) {
	assertJSON(t, backend.ScreenMeta{
		AltScreen:      false,
		ScrollPosition: 0,
	}, `{"alt_screen":false,"scroll_position":0}`)
}

func TestCapabilitiesMarshalToTheSpecShape(t *testing.T) {
	assertJSON(t, backend.Capabilities{
		Backend:              backend.Zmx,
		NativeScrollback:     true,
		Views:                false,
		RemainOnExit:         false,
		ServerEnv:            false,
		RendersCurrentScreen: false,
		TracksAltScreen:      false,
	}, `{"native_scrollback":true,"views":false,"remain_on_exit":false,"server_env":false,"renders_current_screen":false,"tracks_alt_screen":false}`)
}

// The tri-states are the reason several of these types exist at all. Their wire
// spellings are as semver-bound as the field names: a consumer's reap decision
// branches on these exact strings (behavior §3.2, §3.5).
func TestTriStateSpellings(t *testing.T) {
	// Keyed by constant name rather than by value: "present" is deliberately a
	// value of two different tri-states, and a map keyed on the value would
	// silently drop half the assertions.
	spellings := []struct{ name, got, want string }{
		{"LivenessPresent", string(backend.LivenessPresent), "present"},
		{"LivenessGone", string(backend.LivenessGone), "gone"},
		{"LivenessUnknown", string(backend.LivenessUnknown), "unknown"},
		{"StatePresent", string(backend.StatePresent), "present"},
		{"StateAbsent", string(backend.StateAbsent), "absent"},
		{"StateError", string(backend.StateError), "error"},
		{"OutcomeCreated", string(backend.OutcomeCreated), "created"},
		{"OutcomeReused", string(backend.OutcomeReused), "reused"},
		{"OutcomeReaped", string(backend.OutcomeReaped), "reaped"},
		{"Tmux", string(backend.Tmux), "tmux"},
		{"Zmx", string(backend.Zmx), "zmx"},
	}
	for _, s := range spellings {
		if s.got != s.want {
			t.Errorf("%s is %q, want %q", s.name, s.got, s.want)
		}
	}
}

// The zero Liveness must not marshal as a lie. An unset classification is
// "unknown" — the value §3.2 requires consumers to treat as still-present —
// and never the empty string, which no consumer branches on.
func TestZeroLivenessIsUnknown(t *testing.T) {
	var row backend.Session
	if got := row.Liveness.OrUnknown(); got != backend.LivenessUnknown {
		t.Errorf("zero Liveness resolves to %q, want %q", got, backend.LivenessUnknown)
	}
	if got := backend.LivenessGone.OrUnknown(); got != backend.LivenessGone {
		t.Errorf("a set Liveness resolves to %q, want %q", got, backend.LivenessGone)
	}
}
