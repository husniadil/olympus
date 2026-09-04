package herdr

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/husniadil/olympus/backend"
)

// §3.7 herdr detects agents itself, so its rows carry status — working, idle
// or blocked — and title: the rows of `herdr agent list` map onto the shared
// shape, the workspace
// is named the way Sessions names it, and the usage bars are read out of the
// display tokens in numeric order.
func TestAgentListingParsesHerdrRows(t *testing.T) {
	t.Parallel()
	const fixture = `{"id":"cli:agent:list","result":{"agents":[` +
		`{"agent":"claude","agent_status":"idle","cwd":"/home/op/gamelan","focused":false,"foreground_cwd":"/home/op/gamelan","pane_id":"w5F:p1","revision":618,"tab_id":"w5F:t1","terminal_title":"✳ Stop music on Chrome","terminal_title_stripped":"Stop music on Chrome",` +
		`"tokens":{"usage_1":"-    5h: ▰▰▰▱▱▱▱▱▱▱  33%","usage_2":"-    7d: ▰▰▰▰▱▱▱▱▱▱  48%","usage_3":"- fable: ▰▰▰▰▰▰▱▱▱▱  69%","usage_hdr":"usage:"},"workspace_id":"w5F"},` +
		`{"agent":"codex","agent_status":"thinking","cwd":"/home/op/other","pane_id":"w6:p2","tab_id":"w6:t1","terminal_title_stripped":"","tokens":{"usage_1":"not a bar","usage_x":"- 5h: 10%"},"workspace_id":"w6"},` +
		`{"agent":"codex","agent_status":"blocked","cwd":"/home/op/ask","pane_id":"w7:p1","tab_id":"w7:t1","terminal_title_stripped":"Allow command?","tokens":{},"workspace_id":"w7"}` +
		`],"type":"agent_list"}}`
	snap := snapshot{Workspaces: []workspaceRow{{WorkspaceID: "w5F", Label: "gamelan"}}}

	agents, err := parseAgents(fixture, snap)
	if err != nil {
		t.Fatalf("parsing the fixture: %v", err)
	}
	want := []backend.Agent{
		{
			PaneID: "w5F:p1", SessionName: "gamelan", SessionID: "w5F", Agent: "claude",
			Status: "idle", Title: "Stop music on Chrome", CWD: "/home/op/gamelan", DetectedBy: "herdr",
			Usage: []backend.AgentUsage{{Label: "5h", Percent: 33}, {Label: "7d", Percent: 48}, {Label: "fable", Percent: 69}},
		},
		// A workspace the snapshot does not know is named by its id; a status
		// outside the vocabulary is unknown; a bar that does not parse is
		// skipped, and a key that is not usage_<n> is not a bar.
		{
			PaneID: "w6:p2", SessionName: "w6", SessionID: "w6", Agent: "codex",
			Status: "unknown", CWD: "/home/op/other", DetectedBy: "herdr",
		},
		// blocked is a state of its own, not folded into unknown: it is the
		// one a caller most needs to act on.
		{
			PaneID: "w7:p1", SessionName: "w7", SessionID: "w7", Agent: "codex",
			Status: "blocked", Title: "Allow command?", CWD: "/home/op/ask", DetectedBy: "herdr",
		},
	}
	if !reflect.DeepEqual(agents, want) {
		t.Errorf("parsed\n\t%+v\nwant\n\t%+v", agents, want)
	}

	if _, err := parseAgents("not json", snapshot{}); backend.CodeOf(err) != backend.CodeUnexpected {
		t.Errorf("an unparseable listing is %q, want %q", backend.CodeOf(err), backend.CodeUnexpected)
	}
}

// §3.7 The verb answers on a real server, and the shape the fixture above was
// transcribed from is the shape the binary prints: one envelope whose result
// carries an `agents` array. No agent is started here — that would put a real
// coding agent under a test — so the listing is empty, and empty is an array.
func TestAgentsAnswerOnAPrivateServer(t *testing.T) {
	b := liveBackend(t)
	ctx := context.Background()

	out := raw(t, b, "agent", "list")
	var reply struct {
		Result struct {
			Agents *[]json.RawMessage `json:"agents"`
		} `json:"result"`
		Type string `json:"type"`
	}
	if err := json.Unmarshal([]byte(out), &reply); err != nil || reply.Result.Agents == nil {
		t.Fatalf("`herdr agent list` did not print an envelope with result.agents: %v\n%s", err, out)
	}

	agents, err := b.Agents(ctx)
	if err != nil {
		t.Fatalf("Agents: %v", err)
	}
	if agents == nil {
		t.Fatal("Agents answered nil, want an empty slice")
	}
	if len(agents) != len(*reply.Result.Agents) {
		t.Errorf("Agents listed %d rows, the binary %d", len(agents), len(*reply.Result.Agents))
	}
	for _, a := range agents {
		if a.DetectedBy != backend.DetectedByNative || a.PaneID == "" || a.Agent == "" {
			t.Errorf("row %+v is not a natively detected agent in a pane", a)
		}
	}
}
