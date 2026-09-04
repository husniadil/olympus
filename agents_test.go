package olympus

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/husniadil/olympus/backend"
)

// §3.7 On a backend with no agent detection of its own, an agent is a pane
// whose foreground command has a known agent's name. The heuristic knows the
// name and nothing else: the status is unknown, not guessed, the row says it
// was found by command, and the rest of the row is the pane's own identity.
func TestAgentsAreDerivedFromPaneCommandsWithoutInventingAStatus(t *testing.T) {
	t.Parallel()
	f := &fakeBackend{
		caps: backend.Capabilities{Backend: backend.Tmux},
		panes: []backend.Pane{
			{ID: "%1", SessionName: "build", SessionID: "$1", CurrentCommand: "zsh", CurrentPath: "/repo"},
			{ID: "%2", SessionName: "agent", SessionID: "$2", CurrentCommand: "claude", CurrentPath: "/repo/agent"},
			// zmx reports the spawn argv, so the first token is the program.
			{ID: "%3", SessionName: "fix", SessionID: "$3", CurrentCommand: "/usr/local/bin/codex --resume abc", CurrentPath: "/repo/fix"},
			// Case-sensitive: the binaries' own names, nothing looser.
			{ID: "%4", SessionName: "shout", SessionID: "$4", CurrentCommand: "Claude", CurrentPath: "/x"},
			{ID: "%5", SessionName: "empty", SessionID: "$5", CurrentCommand: "", CurrentPath: "/x"},
			{ID: "%6", SessionName: "cursor", SessionID: "$6", CurrentCommand: "cursor-agent", CurrentPath: "/c"},
		},
	}
	agents, err := fakeOlympus(f).Agents(context.Background())
	if err != nil {
		t.Fatalf("Agents: %v", err)
	}
	want := []backend.Agent{
		{PaneID: "%2", SessionName: "agent", SessionID: "$2", Agent: "claude", Status: "unknown", CWD: "/repo/agent", DetectedBy: "command"},
		{PaneID: "%3", SessionName: "fix", SessionID: "$3", Agent: "codex", Status: "unknown", CWD: "/repo/fix", DetectedBy: "command"},
		{PaneID: "%6", SessionName: "cursor", SessionID: "$6", Agent: "cursor-agent", Status: "unknown", CWD: "/c", DetectedBy: "command"},
	}
	if len(agents) != len(want) {
		t.Fatalf("listed %d agents, want %d: %+v", len(agents), len(want), agents)
	}
	for i := range want {
		if agents[i].Usage != nil || agents[i].Title != "" {
			t.Errorf("row %d carries a title or usage the heuristic cannot know: %+v", i, agents[i])
		}
		if !reflect.DeepEqual(agents[i], want[i]) {
			t.Errorf("row %d is %+v, want %+v", i, agents[i], want[i])
		}
	}
}

// §3.7 The listing is never null: a backend with no agent in any pane answers
// an empty array, and a verb that answers on every backend cannot be
// unsupported.
func TestAgentsAnswerAnEmptyArrayNotNull(t *testing.T) {
	t.Parallel()
	f := &fakeBackend{caps: backend.Capabilities{Backend: backend.Zmx}}
	agents, err := fakeOlympus(f).Agents(context.Background())
	if err != nil {
		t.Fatalf("Agents: %v", err)
	}
	if agents == nil {
		t.Fatal("a listing with no agents is nil, want an empty slice")
	}
	got, _ := json.Marshal(agents)
	if string(got) != "[]" {
		t.Errorf("marshals to %s, want []", got)
	}
}

// A listingBackend is the fake with native detection: what the ergonomic
// layer sees on herdr.
type listingBackend struct {
	*fakeBackend
	agents []backend.Agent
}

func (l *listingBackend) Agents(context.Context) ([]backend.Agent, error) { return l.agents, nil }

// §3.7 A backend with its own detection is asked, not second-guessed: its rows
// pass through with their status, title and usage, and the pane heuristic
// does not run beside them — a pane it already reported would otherwise be
// listed twice.
func TestAgentsPreferTheBackendsOwnDetection(t *testing.T) {
	t.Parallel()
	native := backend.Agent{
		PaneID: "w5F:p1", SessionName: "gamelan", SessionID: "w5F", Agent: "claude",
		Status: "working", Title: "Stop music on Chrome", CWD: "/repo/gamelan", DetectedBy: "herdr",
		Usage: []backend.AgentUsage{{Label: "5h", Percent: 33}, {Label: "7d", Percent: 48}},
	}
	l := &listingBackend{
		fakeBackend: &fakeBackend{
			caps:  backend.Capabilities{Backend: backend.Herdr, AgentStatus: true},
			panes: []backend.Pane{{ID: "w5F:p1", SessionName: "gamelan", SessionID: "w5F", CurrentCommand: "claude"}},
		},
		agents: []backend.Agent{native},
	}
	o := &Olympus{backend: l, resolution: Resolution{Backend: backend.Herdr, Reason: ReasonFlag}}
	agents, err := o.Agents(context.Background())
	if err != nil {
		t.Fatalf("Agents: %v", err)
	}
	if len(agents) != 1 {
		t.Fatalf("listed %d rows, want the backend's one: %+v", len(agents), agents)
	}
	got, _ := json.Marshal(agents[0])
	// Transcribed from api §5's agent row, not read back off the value.
	want := `{"pane_id":"w5F:p1","session_name":"gamelan","session_id":"w5F","agent":"claude","status":"working","title":"Stop music on Chrome","cwd":"/repo/gamelan","detected_by":"herdr","usage":[{"label":"5h","percent":33},{"label":"7d","percent":48}]}`
	if string(got) != want {
		t.Errorf("marshalled to\n\t%s\nwant\n\t%s", got, want)
	}

	// And a native listing with nothing in it is still an array.
	l.agents = nil
	agents, err = o.Agents(context.Background())
	if err != nil || agents == nil {
		t.Errorf("an empty native listing is (%v, %v), want an empty slice", agents, err)
	}
}
