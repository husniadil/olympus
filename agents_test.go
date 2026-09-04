package olympus

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/husniadil/olympus/backend"
)

// §3.7 On a backend with no agent detection of its own and no pid on the
// pane, an agent is a pane whose foreground command has a known agent's name.
// The heuristic knows the name and nothing else; with nothing on the screen
// the status is unknown, not guessed, the row says it was found by command,
// and the rest of the row is the pane's own identity.
func TestAgentsAreDerivedFromPaneCommandsWithoutInventingAStatus(t *testing.T) {
	t.Parallel()
	f := &fakeBackend{
		caps: backend.Capabilities{Backend: backend.Tmux},
		panes: []backend.Pane{
			{ID: "%1", SessionName: "build", SessionID: "$1", CurrentCommand: "zsh", CurrentPath: "/repo"},
			{ID: "%2", SessionName: "agent", SessionID: "$2", CurrentCommand: "claude", CurrentPath: "/repo/agent"},
			// zmx reports the spawn argv, so the first token is the program.
			{ID: "%3", SessionName: "fix", SessionID: "$3", CurrentCommand: "/usr/local/bin/codex --resume abc", CurrentPath: "/repo/fix"},
			// Matched lowercased, and reported under the canonical name.
			{ID: "%4", SessionName: "shout", SessionID: "$4", CurrentCommand: "Claude", CurrentPath: "/x"},
			{ID: "%5", SessionName: "empty", SessionID: "$5", CurrentCommand: "", CurrentPath: "/x"},
			{ID: "%6", SessionName: "cursor", SessionID: "$6", CurrentCommand: "cursor-agent", CurrentPath: "/c"},
			// A runtime by itself is nothing: this is the shape tmux reports
			// for a script agent, and without a pid there is nothing to walk.
			{ID: "%7", SessionName: "node", SessionID: "$7", CurrentCommand: "node", CurrentPath: "/n"},
		},
	}
	agents, err := fakeOlympus(f).Agents(context.Background())
	if err != nil {
		t.Fatalf("Agents: %v", err)
	}
	want := []backend.Agent{
		{PaneID: "%2", SessionName: "agent", SessionID: "$2", Agent: "claude", Status: "unknown", CWD: "/repo/agent", DetectedBy: "command"},
		{PaneID: "%3", SessionName: "fix", SessionID: "$3", Agent: "codex", Status: "unknown", CWD: "/repo/fix", DetectedBy: "command"},
		{PaneID: "%4", SessionName: "shout", SessionID: "$4", Agent: "claude", Status: "unknown", CWD: "/x", DetectedBy: "command"},
		{PaneID: "%6", SessionName: "cursor", SessionID: "$6", Agent: "cursor", Status: "unknown", CWD: "/c", DetectedBy: "command"},
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
// pass through with their status, title and usage, marked as the backend's
// own (`status_source: "native"`), and the pane heuristic does not run
// beside them — a pane it already reported would otherwise be listed twice.
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
	want := `{"pane_id":"w5F:p1","session_name":"gamelan","session_id":"w5F","agent":"claude","status":"working","status_source":"native","title":"Stop music on Chrome","cwd":"/repo/gamelan","detected_by":"herdr","usage":[{"label":"5h","percent":33},{"label":"7d","percent":48}]}`
	if string(got) != want {
		t.Errorf("marshalled to\n\t%s\nwant\n\t%s", got, want)
	}

	// A native row whose status the backend could not name carries no
	// source: there is nothing to attribute.
	l.agents = []backend.Agent{{PaneID: "w6:p2", Agent: "codex", Status: "unknown", DetectedBy: "herdr"}}
	agents, err = o.Agents(context.Background())
	if err != nil || len(agents) != 1 || agents[0].StatusSource != "" {
		t.Errorf("an unknown native status is (%+v, %v), want no status_source", agents, err)
	}

	// And a native listing with nothing in it is still an array.
	l.agents = nil
	agents, err = o.Agents(context.Background())
	if err != nil || agents == nil {
		t.Errorf("an empty native listing is (%v, %v), want an empty slice", agents, err)
	}
}

// measuredProcesses is a process table transcribed from `ps -eo
// pid=,ppid=,args=` on a machine running three panes, each holding an agent
// the foreground-command match cannot see:
//
//   - a tmux pane (pid 74924) whose interactive shell runs `pi`, which tmux
//     reports as `node` because that is the process's executable;
//   - a tmux pane (pid 77500) whose shell runs codex installed through npm,
//     `node …/bin/codex`, with helpers below it whose paths also name codex;
//   - a zmx session (pid 78020) whose shell runs claude installed through npm,
//     `node …/@anthropic-ai/claude-code/cli.js`, where no token is called claude.
//
// Plus a pane (pid 80000) that is only a shell with an editor open on a file
// that happens to be named after an agent, one (pid 81000) with nothing in
// the table below it, one (pid 82000) whose shell runs qwen through a version
// manager's shim, `node …/.fnm/bin/qwen`, and one (pid 83000) spawned onto
// claude directly, with a helper below it that carries the name too.
var measuredProcesses = []process{
	{pid: 1, ppid: 0, args: "/sbin/launchd"},
	{pid: 74900, ppid: 1, args: "tmux -L t new-session"},
	{pid: 74924, ppid: 74900, args: "-zsh"},
	{pid: 75269, ppid: 74924, args: "pi"},
	{pid: 77500, ppid: 74900, args: "-zsh"},
	{pid: 77564, ppid: 77500, args: "node /Users/husni/.local/share/mise/installs/node/24.11.0/bin/codex"},
	{pid: 77570, ppid: 77564, args: "/Users/husni/.local/share/mise/installs/node/24.11.0/lib/node_modules/@openai/codex/node_modules/@openai/codex-darwin-arm64/vendor/aarch64-apple-darwin/codex/codex"},
	{pid: 77571, ppid: 77564, args: "node -e const { Worker } = require('worker_threads')"},
	{pid: 78000, ppid: 1, args: "zmx run test zsh"},
	{pid: 78020, ppid: 78000, args: "zsh"},
	{pid: 78031, ppid: 78020, args: "node /Users/husni/.local/share/mise/installs/node/24.11.0/lib/node_modules/@anthropic-ai/claude-code/cli.js --resume"},
	{pid: 78040, ppid: 78031, args: "/bin/zsh -c git status"},
	{pid: 80000, ppid: 74900, args: "-zsh"},
	{pid: 80001, ppid: 80000, args: "vim codex.md"},
	{pid: 81000, ppid: 74900, args: "-zsh"},
	{pid: 82000, ppid: 74900, args: "-zsh"},
	{pid: 82001, ppid: 82000, args: "node /Users/husni/.fnm/bin/qwen"},
	{pid: 83000, ppid: 74900, args: "claude --resume"},
	{pid: 83001, ppid: 83000, args: "/opt/claude --worker"},
}

// §3.7 With a pid on the pane, detection MUST inspect the pane's process
// subtree rather than the foreground command: the pane's own process first,
// then the shell's direct children, then the best-scoring process below —
// named by its own binary, by the script an interpreter's argv runs, or by
// the package directory in its path — and the row reports the vocabulary's
// canonical name. One row per pane, so the helpers below an agent that carry
// its name too do not list it twice. A pane whose subtree names no agent is
// not one, whatever its command says.
func TestAgentsWalkThePanesProcessSubtree(t *testing.T) {
	t.Parallel()
	f := &fakeBackend{
		caps: backend.Capabilities{Backend: backend.Tmux},
		panes: []backend.Pane{
			{ID: "%1", SessionName: "test", SessionID: "$1", CurrentCommand: "node", CurrentPath: "/repo", PID: 74924},
			{ID: "%2", SessionName: "test-2", SessionID: "$2", CurrentCommand: "node", CurrentPath: "/repo/2", PID: 77500},
			{ID: "test", SessionName: "test", SessionID: "test", CurrentCommand: "", CurrentPath: "/z", PID: 78020},
			{ID: "%4", SessionName: "editor", SessionID: "$4", CurrentCommand: "vim", CurrentPath: "/e", PID: 80000},
			// The spawn argv says claude, the tree says a bare shell: the tree
			// is the answer where there is one.
			{ID: "%5", SessionName: "stale", SessionID: "$5", CurrentCommand: "claude", CurrentPath: "/s", PID: 81000},
			// No pid at all: the foreground command still answers.
			{ID: "%6", SessionName: "meja", SessionID: "$6", CurrentCommand: "/opt/bin/gemini", CurrentPath: "/m"},
			{ID: "%7", SessionName: "shim", SessionID: "$7", CurrentCommand: "node", CurrentPath: "/q", PID: 82000},
			{ID: "%8", SessionName: "direct", SessionID: "$8", CurrentCommand: "claude", CurrentPath: "/d", PID: 83000},
		},
	}
	o := fakeOlympus(f)
	reads := 0
	o.processTable = func(context.Context) ([]process, error) {
		reads++
		return measuredProcesses, nil
	}
	agents, err := o.Agents(context.Background())
	if err != nil {
		t.Fatalf("Agents: %v", err)
	}
	if reads != 1 {
		t.Errorf("the process table was read %d times, want once per call", reads)
	}
	want := []backend.Agent{
		{PaneID: "%1", SessionName: "test", SessionID: "$1", Agent: "pi", Status: "unknown", CWD: "/repo", DetectedBy: "command"},
		{PaneID: "%2", SessionName: "test-2", SessionID: "$2", Agent: "codex", Status: "unknown", CWD: "/repo/2", DetectedBy: "command"},
		{PaneID: "test", SessionName: "test", SessionID: "test", Agent: "claude", Status: "unknown", CWD: "/z", DetectedBy: "command"},
		{PaneID: "%6", SessionName: "meja", SessionID: "$6", Agent: "gemini", Status: "unknown", CWD: "/m", DetectedBy: "command"},
		{PaneID: "%7", SessionName: "shim", SessionID: "$7", Agent: "qwen", Status: "unknown", CWD: "/q", DetectedBy: "command"},
		{PaneID: "%8", SessionName: "direct", SessionID: "$8", Agent: "claude", Status: "unknown", CWD: "/d", DetectedBy: "command"},
	}
	if !reflect.DeepEqual(agents, want) {
		t.Errorf("listed\n\t%+v\nwant\n\t%+v", agents, want)
	}
}

// §3.7 The process table is read only when a pane has a pid to walk from,
// and a `ps` that cannot be run does not fail the verb: every pane falls
// back to the foreground-command match and the listing still answers.
func TestAgentsFallBackToTheCommandWhenTheProcessTableIsUnreadable(t *testing.T) {
	t.Parallel()
	withoutPIDs := &fakeBackend{
		caps:  backend.Capabilities{Backend: backend.Meja},
		panes: []backend.Pane{{ID: "%1", SessionName: "a", SessionID: "$1", CurrentCommand: "claude"}},
	}
	o := fakeOlympus(withoutPIDs)
	o.processTable = func(context.Context) ([]process, error) {
		t.Error("the process table was read with no pid to walk from")
		return nil, nil
	}
	if agents, err := o.Agents(context.Background()); err != nil || len(agents) != 1 {
		t.Errorf("listed (%+v, %v), want the one command-matched agent", agents, err)
	}

	withPIDs := &fakeBackend{
		caps: backend.Capabilities{Backend: backend.Tmux},
		panes: []backend.Pane{
			{ID: "%1", SessionName: "a", SessionID: "$1", CurrentCommand: "claude", CurrentPath: "/a", PID: 100},
			{ID: "%2", SessionName: "b", SessionID: "$2", CurrentCommand: "zsh", CurrentPath: "/b", PID: 200},
		},
	}
	o = fakeOlympus(withPIDs)
	o.processTable = func(context.Context) ([]process, error) {
		return nil, errors.New("ps: not found")
	}
	agents, err := o.Agents(context.Background())
	if err != nil {
		t.Fatalf("an unreadable process table failed the verb: %v", err)
	}
	want := []backend.Agent{
		{PaneID: "%1", SessionName: "a", SessionID: "$1", Agent: "claude", Status: "unknown", CWD: "/a", DetectedBy: "command"},
	}
	if !reflect.DeepEqual(agents, want) {
		t.Errorf("listed %+v, want the command-matched row %+v", agents, want)
	}
}

// §3.7 The vocabulary, spelled once: every alias is matched as a lowercased
// base name with a wrapper suffix removed and reported under its canonical
// name; a runtime or shell is named by the script it runs, never by itself,
// and an eval flag means it runs none; the npm package directories count as
// a match on a token's path; and only argv0 and that script are consulted,
// so a file named after an agent does not make its editor one.
func TestAgentVocabularyMatchesAliasesScriptsAndPackagePaths(t *testing.T) {
	t.Parallel()
	aliases := map[string]string{
		"pi": "pi", "claude": "claude", "claude-code": "claude", "codex": "codex",
		"gemini": "gemini", "cursor": "cursor", "cursor-agent": "cursor",
		"devin": "devin", "devin-cli": "devin", "agy": "agy", "antigravity": "agy",
		"antigravity-cli": "agy", "cline": "cline", "omp": "omp",
		"mastracode": "mastracode", "mastra-code": "mastracode",
		"opencode": "opencode", "opencode2": "opencode", "open-code": "opencode",
		"copilot": "copilot", "github-copilot": "copilot", "ghcs": "copilot",
		"kimi": "kimi", "kimi-code": "kimi", "kiro": "kiro", "kiro-cli": "kiro",
		"droid": "droid", "amp": "amp", "amp-local": "amp", "grok": "grok",
		"grok-build": "grok", "hermes": "hermes", "hermes-agent": "hermes",
		"kilo": "kilo", "kilo-code": "kilo", "qodercli": "qodercli",
		"qoderclicn": "qodercli", "qoder": "qodercli", "qodercn": "qodercli",
		"qwen": "qwen", "qwen-code": "qwen", "maki": "maki", "muse": "muse",
		"muse-code": "muse", "muse-cli": "muse", "muse-bin-0.1.0-R708.1": "muse",
		"aider": "aider", "goose": "goose",
	}
	for alias, want := range aliases {
		for _, args := range []string{alias, "/usr/local/bin/" + alias + " --resume", strings.ToUpper(alias)} {
			if got, _, ok := agentInArgv(strings.Fields(args)); !ok || got != want {
				t.Errorf("%q: got (%q, %v), want (%q, true)", args, got, ok, want)
			}
		}
	}
	cases := map[string]string{
		"node /x/node_modules/@anthropic-ai/claude-code/cli.js": "claude",
		"node /x/lib/node_modules/@openai/codex/bin/codex.js":   "codex",
		"node /x/node_modules/@google/gemini-cli/dist/index.js": "gemini",
		"node /x/bin/codex":                                   "codex",
		"node /Users/husni/.fnm/bin/qwen --yolo":              "qwen",
		"node -r /x/hook.js --inspect-port 9229 /x/bin/codex": "codex",
		"node -- /x/bin/codex":                                "codex",
		"bun /x/bin/kimi":                                     "kimi",
		"python3.12 /x/bin/aider":                             "aider",
		"/x/Codex.js":                                         "codex",
		"node":                                                "",
		"node -e require('codex')":                            "",
		"node --eval=codex":                                   "",
		"python3 -m codex":                                    "",
		"sh -c codex":                                         "",
		"-zsh":                                                "",
		"vim codex.md":                                        "",
		"muse-binary":                                         "",
		"/x/claude-code-review/run":                           "",
		"":                                                    "",
	}
	for args, want := range cases {
		got, _, ok := agentInArgv(strings.Fields(args))
		if got != want || ok != (want != "") {
			t.Errorf("%q: got (%q, %v), want (%q, %v)", args, got, ok, want, want != "")
		}
	}
}

// §3.7 The process table is what `ps -eo pid=,ppid=,args=` prints on macOS
// and Linux: two right-aligned integers and then the command line, spaces
// preserved. Anything that does not lead with two integers is skipped.
func TestProcessTableParsing(t *testing.T) {
	t.Parallel()
	out := "    1     0 /sbin/launchd\n" +
		"74924 74900 -zsh\n" +
		"77571 77564 node -e const { Worker } = require('worker_threads')\n" +
		"  PID  PPID ARGS\n" +
		"\n" +
		"  99\n"
	want := []process{
		{pid: 1, ppid: 0, args: "/sbin/launchd"},
		{pid: 74924, ppid: 74900, args: "-zsh"},
		{pid: 77571, ppid: 77564, args: "node -e const { Worker } = require('worker_threads')"},
	}
	if got := parseProcessTable(out); !reflect.DeepEqual(got, want) {
		t.Errorf("parsed\n\t%+v\nwant\n\t%+v", got, want)
	}
}

// claudeWorking is a claude screen mid-turn, transcribed from the
// manifest's own fixtures: the composer box with "esc to interrupt" in the
// footer below it.
const claudeWorking = "────────────────────────────────────────────────────────────────\n" +
	"❯\n" +
	"────────────────────────────────────────────────────────────────\n" +
	"  ⏵⏵ auto mode on · 1 shell · esc to interrupt\n"

// §3.7 A command-detected row's status is read off the pane's screen by the
// agent's manifest, one capture per row, and marked `status_source:
// "screen"`. What the screen does not show is unknown with no source: an
// agent with no manifest (not captured at all), a screen no rule recognises,
// and a title that is only tmux's default host name, which two manifests
// would otherwise read as idle. blocked is a state of its own.
func TestAgentsReadTheirStatusOffTheScreen(t *testing.T) {
	t.Parallel()
	host, err := os.Hostname()
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeBackend{
		caps: backend.Capabilities{Backend: backend.Tmux, AgentStatus: true},
		panes: []backend.Pane{
			{ID: "%1", SessionName: "turn", SessionID: "$1", CurrentCommand: "claude", CurrentPath: "/a"},
			{ID: "%2", SessionName: "quiet", SessionID: "$2", CurrentCommand: "codex", CurrentPath: "/b", Title: "project"},
			{ID: "%3", SessionName: "old", SessionID: "$3", CurrentCommand: "aider", CurrentPath: "/c"},
			{ID: "%4", SessionName: "blank", SessionID: "$4", CurrentCommand: "claude", CurrentPath: "/d"},
			{ID: "%5", SessionName: "host", SessionID: "$5", CurrentCommand: "codex", CurrentPath: "/e", Title: host},
			{ID: "%6", SessionName: "asking", SessionID: "$6", CurrentCommand: "claude", CurrentPath: "/f"},
			// Two panes in one session: a capture of the session is the
			// active pane's screen, which may be the other's, so neither
			// is read.
			{ID: "%7", SessionName: "split", SessionID: "$7", CurrentCommand: "claude", CurrentPath: "/g"},
			{ID: "%8", SessionName: "split", SessionID: "$7", CurrentCommand: "codex", CurrentPath: "/g"},
		},
		// A capture addresses the pane's session (§10). Padded to a width,
		// the way one backend prints rows, since a pattern anchored at the
		// end of a line must still match.
		screens: map[string]string{
			"turn":   padRows(claudeWorking),
			"quiet":  "› Use /skills to list available skills\n",
			"old":    "Working...\n",
			"blank":  "  ⏵⏵ auto mode on · 1 shell · ← for agents\n",
			"host":   "› Use /skills to list available skills\n",
			"asking": "do you want to proceed?\nbash command: rm -rf /tmp/test\n❯ 1. Yes\n  2. No\n\nEsc to cancel · Tab to amend · ctrl+e to explain\n",
			"split":  claudeWorking,
		},
	}
	agents, err := fakeOlympus(f).Agents(context.Background())
	if err != nil {
		t.Fatalf("Agents: %v", err)
	}
	want := []backend.Agent{
		{PaneID: "%1", SessionName: "turn", SessionID: "$1", Agent: "claude", Status: "working", StatusSource: "screen", CWD: "/a", DetectedBy: "command"},
		{PaneID: "%2", SessionName: "quiet", SessionID: "$2", Agent: "codex", Status: "idle", StatusSource: "screen", CWD: "/b", DetectedBy: "command"},
		{PaneID: "%3", SessionName: "old", SessionID: "$3", Agent: "aider", Status: "unknown", CWD: "/c", DetectedBy: "command"},
		{PaneID: "%4", SessionName: "blank", SessionID: "$4", Agent: "claude", Status: "unknown", CWD: "/d", DetectedBy: "command"},
		{PaneID: "%5", SessionName: "host", SessionID: "$5", Agent: "codex", Status: "unknown", CWD: "/e", DetectedBy: "command"},
		{PaneID: "%6", SessionName: "asking", SessionID: "$6", Agent: "claude", Status: "blocked", StatusSource: "screen", CWD: "/f", DetectedBy: "command"},
		{PaneID: "%7", SessionName: "split", SessionID: "$7", Agent: "claude", Status: "unknown", CWD: "/g", DetectedBy: "command"},
		{PaneID: "%8", SessionName: "split", SessionID: "$7", Agent: "codex", Status: "unknown", CWD: "/g", DetectedBy: "command"},
	}
	if !reflect.DeepEqual(agents, want) {
		t.Errorf("listed\n\t%+v\nwant\n\t%+v", agents, want)
	}
	// One capture per row with a manifest in a one-pane session, of the
	// visible screen only; none for the agent without one, none for the
	// shared session.
	if len(f.screenOpts) != 5 {
		t.Errorf("%d captures for eight rows, want one per manifest-bearing row in its own session (5)", len(f.screenOpts))
	}
	for _, opts := range f.screenOpts {
		if opts != (backend.ScreenOpts{}) {
			t.Errorf("a capture asked for %+v, want the visible screen alone", opts)
		}
	}
	got, _ := json.Marshal(agents[0])
	wantJSON := `{"pane_id":"%1","session_name":"turn","session_id":"$1","agent":"claude","status":"working","status_source":"screen","cwd":"/a","detected_by":"command"}`
	if string(got) != wantJSON {
		t.Errorf("marshalled to\n\t%s\nwant\n\t%s", got, wantJSON)
	}
	got, _ = json.Marshal(agents[2])
	if strings.Contains(string(got), "status_source") {
		t.Errorf("an unknown status carries a source: %s", got)
	}
}

// padRows pads every line to 80 columns, the way a width-padding backend
// prints a capture.
func padRows(screen string) string {
	lines := strings.Split(screen, "\n")
	for i, line := range lines {
		if line != "" {
			lines[i] = line + strings.Repeat(" ", 80-len([]rune(line)))
		}
	}
	return strings.Join(lines, "\n")
}

// §3.7 A capture that fails does not fail the listing, and does not invent a
// status either: the row is still an agent, unknown with no source.
func TestAgentsStayUnknownWhenTheCaptureFails(t *testing.T) {
	t.Parallel()
	f := &fakeBackend{
		caps:      backend.Capabilities{Backend: backend.Tmux, AgentStatus: true},
		panes:     []backend.Pane{{ID: "%1", SessionName: "turn", SessionID: "$1", CurrentCommand: "claude", CurrentPath: "/a"}},
		text:      claudeWorking,
		screenErr: backend.Errorf(backend.CodeBackendUnavailable, "the server went away"),
	}
	agents, err := fakeOlympus(f).Agents(context.Background())
	if err != nil {
		t.Fatalf("Agents: %v", err)
	}
	want := []backend.Agent{{PaneID: "%1", SessionName: "turn", SessionID: "$1", Agent: "claude", Status: "unknown", CWD: "/a", DetectedBy: "command"}}
	if !reflect.DeepEqual(agents, want) {
		t.Errorf("listed %+v, want %+v", agents, want)
	}
}

// §3.7 Where the backend's capture is the whole scrollback rather than the
// viewport, the tail stands in for the screen: a state the agent showed
// long ago must not be read as its state now.
func TestAgentsReadOnlyTheTailOfAScrollbackCapture(t *testing.T) {
	t.Parallel()
	stale := "Working...\n" + strings.Repeat("output\n", 40) + "> \n"
	for _, c := range []struct {
		native bool
		want   string
	}{{true, "unknown"}, {false, "working"}} {
		f := &fakeBackend{
			caps:  backend.Capabilities{Backend: backend.Zmx, AgentStatus: true, NativeScrollback: c.native},
			panes: []backend.Pane{{ID: "s", SessionName: "s", SessionID: "s", CurrentCommand: "pi", CurrentPath: "/a"}},
			text:  stale,
		}
		agents, err := fakeOlympus(f).Agents(context.Background())
		if err != nil || len(agents) != 1 {
			t.Fatalf("Agents: %v, %+v", err, agents)
		}
		if agents[0].Status != c.want {
			t.Errorf("native_scrollback=%v: status %s, want %s", c.native, agents[0].Status, c.want)
		}
	}
	if got := tailLines("a\nb\nc\n", 2); got != "b\nc\n" {
		t.Errorf("tailLines kept %q", got)
	}
	if got := tailLines("a\nb", 5); got != "a\nb" {
		t.Errorf("tailLines of a short text is %q", got)
	}
}
