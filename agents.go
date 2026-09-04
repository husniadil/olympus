package olympus

import (
	"bufio"
	"context"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"github.com/husniadil/olympus/backend"
	"github.com/husniadil/olympus/internal/agentstate"
)

// agentAliases is the vocabulary of agents the command heuristic knows: every
// name a running agent's binary may carry, mapped to the canonical name a
// row reports (behavior §3.7). Matched against a lowercased base name with
// any `.js`/`.exe`-style suffix removed, so an alias is spelled once here in
// its canonical case and a path or a wrapper script still matches.
var agentAliases = map[string]string{
	"pi":              "pi",
	"claude":          "claude",
	"claude-code":     "claude",
	"codex":           "codex",
	"gemini":          "gemini",
	"cursor":          "cursor",
	"cursor-agent":    "cursor",
	"devin":           "devin",
	"devin-cli":       "devin",
	"agy":             "agy",
	"antigravity":     "agy",
	"antigravity-cli": "agy",
	"cline":           "cline",
	"omp":             "omp",
	"mastracode":      "mastracode",
	"mastra-code":     "mastracode",
	"opencode":        "opencode",
	"opencode2":       "opencode",
	"open-code":       "opencode",
	"copilot":         "copilot",
	"github-copilot":  "copilot",
	"ghcs":            "copilot",
	"kimi":            "kimi",
	"kimi-code":       "kimi",
	"kiro":            "kiro",
	"kiro-cli":        "kiro",
	"droid":           "droid",
	"amp":             "amp",
	"amp-local":       "amp",
	"grok":            "grok",
	"grok-build":      "grok",
	"hermes":          "hermes",
	"hermes-agent":    "hermes",
	"kilo":            "kilo",
	"kilo-code":       "kilo",
	"qodercli":        "qodercli",
	"qoderclicn":      "qodercli",
	"qoder":           "qodercli",
	"qodercn":         "qodercli",
	"qwen":            "qwen",
	"qwen-code":       "qwen",
	"maki":            "maki",
	"muse":            "muse",
	"muse-code":       "muse",
	"muse-cli":        "muse",
	"aider":           "aider",
	"goose":           "goose",
}

// agentPackages are the agents recognisable from a directory in a token's
// path rather than its base name: an npm-installed agent runs as
// `node …/@anthropic-ai/claude-code/cli.js`, where no token is named after
// the agent, but the package directory is. Each entry is a directory path
// that must appear whole among the token's directory components.
var agentPackages = []struct{ dir, agent string }{
	{"claude-code", "claude"},
	{"@openai/codex", "codex"},
	{"@google/gemini-cli", "gemini"},
}

// runtimes are the interpreters and shells that run an agent without being
// one: a process whose argv0 is one of these is named by its script argument,
// not by itself. Python is matched separately, by its versioned spellings.
var runtimes = map[string]bool{
	"sh": true, "bash": true, "zsh": true, "fish": true, "tmux": true,
	"node": true, "bun": true, "cmd": true, "powershell": true, "pwsh": true,
}

// A process is one row of the process table: enough to hang it on its parent
// and read what it is running.
type process struct {
	pid, ppid int
	args      string
}

// readProcessTable reads the live process table through `ps`, which spells
// these three columns the same way on macOS and Linux. Each line is pid,
// ppid, then the rest is the command line, spaces and all.
func readProcessTable(ctx context.Context) ([]process, error) {
	out, err := exec.CommandContext(ctx, "ps", "-eo", "pid=,ppid=,args=").Output()
	if err != nil {
		return nil, err
	}
	return parseProcessTable(string(out)), nil
}

// parseProcessTable parses `ps -eo pid=,ppid=,args=` output, skipping any
// line that does not lead with two integers.
func parseProcessTable(out string) []process {
	var table []process
	scanner := bufio.NewScanner(strings.NewReader(out))
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 2 {
			continue
		}
		pid, err := strconv.Atoi(fields[0])
		if err != nil {
			continue
		}
		ppid, err := strconv.Atoi(fields[1])
		if err != nil {
			continue
		}
		table = append(table, process{pid: pid, ppid: ppid, args: strings.Join(fields[2:], " ")})
	}
	return table
}

// A processTree is the process table indexed for walking down from a pane.
type processTree struct {
	argv     map[int][]string
	children map[int][]int
}

func newProcessTree(table []process) *processTree {
	t := &processTree{argv: map[int][]string{}, children: map[int][]int{}}
	for _, p := range table {
		t.argv[p.pid] = strings.Fields(p.args)
		t.children[p.ppid] = append(t.children[p.ppid], p.pid)
	}
	// Ascending pid among siblings, so the walk is the same walk on every
	// call: `ps` does not promise an order.
	for _, siblings := range t.children {
		sort.Ints(siblings)
	}
	return t
}

// agentUnder reports the agent running in the subtree rooted at pid, if any.
//
// The pane's own process is asked first, then its direct children: a pane
// spawned onto an agent IS the agent, and a pane spawned onto a shell runs
// the agent as the shell's child. Only when neither names one is the whole
// subtree scored, and the best-scoring process wins — an agent unwrapped
// from an interpreter's argv over a plain binary, so `node …/bin/codex`
// outranks the `codex` helper it forks, and the first found among equals.
// One answer per pane, whatever else below it carries the agent's name.
func (t *processTree) agentUnder(pid int) (string, bool) {
	if name, _, ok := agentInArgv(t.argv[pid]); ok {
		return name, true
	}
	for _, child := range t.children[pid] {
		if name, _, ok := agentInArgv(t.argv[child]); ok {
			return name, true
		}
	}
	best, bestName := 0, ""
	t.walk(pid, func(argv []string) {
		if name, score, ok := agentInArgv(argv); ok && score > best {
			best, bestName = score, name
		}
	})
	return bestName, best > 0
}

// walk visits every process in the subtree rooted at pid, depth-first, the
// root first.
func (t *processTree) walk(pid int, visit func(argv []string)) {
	visit(t.argv[pid])
	for _, child := range t.children[pid] {
		t.walk(child, visit)
	}
}

// Agents lists the agents running in panes on the resolved backend.
//
// Every backend answers; this is never unsupported. A backend that detects
// agents itself (backend.AgentLister) reports rows with a status and a title,
// marked `status_source: "native"`. Everywhere else the rows are derived
// from the pane listing: a pane whose process subtree, or failing a known
// pid its foreground command, names a known agent is an agent, with
// `detected_by: "command"`, and its status is read off a capture of the
// pane by the agent's manifest, marked `status_source: "screen"`. A screen
// no rule recognises, an agent with no manifest, a capture that fails: all
// unknown, with no source — the listing MUST NOT invent a state it cannot
// see (behavior §3.7).
func (o *Olympus) Agents(ctx context.Context) ([]backend.Agent, error) {
	if lister, ok := o.backend.(backend.AgentLister); ok {
		agents, err := lister.Agents(ctx)
		if agents == nil && err == nil {
			agents = []backend.Agent{}
		}
		for i := range agents {
			if agents[i].Status != backend.AgentUnknown {
				agents[i].StatusSource = backend.StatusSourceNative
			}
		}
		return agents, err
	}
	panes, err := o.Panes(ctx, "")
	if err != nil {
		return nil, err
	}

	// The process table is read once per call, and only when a pane has a
	// pid to walk from. If `ps` cannot be run the verb does not fail: the
	// foreground-command match still answers, as it does for a pane with no
	// pid at all, and the listing is worth more than the missing depth. The
	// degradation is not disclosed on the result because Agents carries no
	// warnings; what a caller sees is a listing that may miss an agent
	// running under a shell.
	var tree *processTree
	for _, pane := range panes {
		if pane.PID == 0 {
			continue
		}
		table, err := o.readProcesses(ctx)
		if err == nil {
			tree = newProcessTree(table)
		}
		break
	}

	// A capture addresses a session, whose screen is its active pane's
	// (§10). That is the pane's own screen for every session Olympus
	// creates, and for any session holding one pane; where a session holds
	// several, no pane's status is read off a screen that may be another's.
	panesIn := map[string]int{}
	for _, pane := range panes {
		panesIn[pane.SessionName]++
	}

	agents := []backend.Agent{}
	for _, pane := range panes {
		var name string
		var ok bool
		if pane.PID != 0 && tree != nil {
			name, ok = tree.agentUnder(pane.PID)
		} else {
			name, _, ok = agentInArgv(strings.Fields(pane.CurrentCommand))
		}
		if !ok {
			continue
		}
		status, source := backend.AgentUnknown, ""
		if panesIn[pane.SessionName] == 1 {
			status, source = o.screenStatus(ctx, pane, name)
		}
		agents = append(agents, backend.Agent{
			PaneID:       pane.ID,
			SessionName:  pane.SessionName,
			SessionID:    pane.SessionID,
			Agent:        name,
			Status:       status,
			StatusSource: source,
			CWD:          pane.CurrentPath,
			DetectedBy:   backend.DetectedByCommand,
		})
	}
	return agents, nil
}

// detectionRows is how many lines of a scrollback capture stand in for the
// viewport where the backend cannot capture the viewport alone: the default
// the manifests' own engine assumes when a terminal's height is unknown.
const detectionRows = 24

// screenStatus reads a command-detected agent's status off its pane
// (behavior §3.7): one capture per row, of the visible screen and no
// scrollback, evaluated by the agent's manifest together with the pane's
// title where the backend reports one. The viewport is what the manifests
// were written against — a prompt already answered above it must not read
// as a blocker — so on a backend whose capture is the whole scrollback the
// tail stands in for it. Trailing blanks are trimmed off every line first:
// one backend pads rows to the pane's width, and a pattern anchored at the
// end of a line is written for a row that ends where its text does.
//
// The title is the one the agent set through OSC 0/2, which tmux reports
// as #{pane_title}. tmux's default for a pane no program has titled is the
// host name, and two manifests read any non-empty title as idle, so the
// host name is not passed: it is the terminal's word, not the agent's.
//
// An agent with no manifest is not captured. A capture that fails leaves
// the status unknown with no source: the row is still an agent, and the
// listing is worth more than the missing status.
func (o *Olympus) screenStatus(ctx context.Context, pane backend.Pane, agent string) (status, source string) {
	manifest, ok := agentstate.Lookup(agent)
	if !ok {
		return backend.AgentUnknown, ""
	}
	capture, err := o.backend.Screen(ctx, pane.SessionName, backend.ScreenOpts{})
	if err != nil {
		return backend.AgentUnknown, ""
	}
	screen := trimLineEnds(capture.Text)
	if o.backend.Capabilities().NativeScrollback {
		screen = tailLines(screen, detectionRows)
	}
	title := pane.Title
	if host, err := os.Hostname(); err == nil && title == host {
		title = ""
	}
	state := manifest.Evaluate(agentstate.Input{Screen: screen, OSCTitle: title})
	if state.State == agentstate.Unknown {
		return backend.AgentUnknown, ""
	}
	return string(state.State), backend.StatusSourceScreen
}

// trimLineEnds drops trailing spaces, tabs and carriage returns from every
// line, keeping the line structure.
func trimLineEnds(text string) string {
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		lines[i] = strings.TrimRight(line, " \t\r")
	}
	return strings.Join(lines, "\n")
}

// tailLines is the last n lines of text, trailing newline kept.
func tailLines(text string, n int) string {
	trimmed := strings.TrimSuffix(text, "\n")
	lines := strings.Split(trimmed, "\n")
	if len(lines) <= n {
		return text
	}
	return strings.Join(lines[len(lines)-n:], "\n") + text[len(trimmed):]
}

// readProcesses reads the process table through whatever the handle was
// given, defaulting to `ps`: the seam the unit tests use to walk a fixture
// table instead of the live machine.
func (o *Olympus) readProcesses(ctx context.Context) ([]process, error) {
	if o.processTable != nil {
		return o.processTable(ctx)
	}
	return readProcessTable(ctx)
}

// agentInArgv reports which agent an argv is running, if any, and how
// surely: 3 when the name had to be unwrapped from a runtime's script
// argument (`node …/bin/codex`), 2 when argv0 is the agent's own binary.
//
// A runtime or shell is never the agent itself; it is named by the script
// it runs, found by skipping options — an eval flag (`-e`, `-c`, `-m`) means
// there is no script, and an option that takes a value takes it. Anything
// else is named by argv0 alone: the base name, or the package directory in
// its path. The name reported is the vocabulary's canonical one.
func agentInArgv(argv []string) (name string, score int, ok bool) {
	if len(argv) == 0 {
		return "", 0, false
	}
	runtime := strings.TrimPrefix(lookupName(argv[0]), "-")
	if runtimes[runtime] || isPython(runtime) {
		script := scriptArgument(runtime, argv[1:])
		if script == "" {
			return "", 0, false
		}
		name, ok = agentFromToken(script)
		return name, 3, ok
	}
	name, ok = agentFromToken(argv[0])
	if !ok {
		return "", 0, false
	}
	if _, direct := agentAliases[lookupName(argv[0])]; direct {
		return name, 2, true
	}
	return name, 3, true
}

// scriptArgument is the script a runtime's remaining argv names, or empty
// when it runs none: an eval flag, or nothing but options.
func scriptArgument(runtime string, rest []string) string {
	var evalFlags, moduleFlags []string
	switch {
	case runtime == "node" || runtime == "bun":
		evalFlags = []string{"-e", "--eval", "-p", "--print"}
	case isPython(runtime):
		evalFlags, moduleFlags = []string{"-c"}, []string{"-m"}
	case runtime == "sh" || runtime == "bash" || runtime == "zsh" || runtime == "fish":
		evalFlags = []string{"-c"}
	default:
		// tmux, cmd, powershell, pwsh: nothing Olympus runs an agent through.
		return ""
	}
	for i := 0; i < len(rest); i++ {
		arg := rest[i]
		if arg == "--" {
			if i+1 < len(rest) {
				return rest[i+1]
			}
			return ""
		}
		if flagMatches(arg, evalFlags) || flagMatches(arg, moduleFlags) {
			return ""
		}
		if strings.HasPrefix(arg, "-") {
			if optionTakesValue(arg) {
				i++
			}
			continue
		}
		return arg
	}
	return ""
}

// flagMatches reports whether arg is one of flags, spelled bare (`-e`), with
// its payload attached (`-eXYZ`), or as `--flag=value`.
func flagMatches(arg string, flags []string) bool {
	for _, flag := range flags {
		if arg == flag {
			return true
		}
		if strings.HasPrefix(flag, "--") {
			if strings.HasPrefix(arg, flag+"=") {
				return true
			}
		} else if strings.HasPrefix(arg, flag) && len(arg) > len(flag) {
			return true
		}
	}
	return false
}

// optionTakesValue lists the runtime options whose value is the next argv
// token, so that value is not mistaken for the script.
func optionTakesValue(arg string) bool {
	switch arg {
	case "-r", "--require", "--loader", "--import", "--experimental-loader",
		"--inspect-port", "-W", "-X", "-S", "-L", "-o":
		return true
	}
	return false
}

// agentFromToken names the agent a single path token is, if any: by its base
// name in the vocabulary, by muse's versioned launcher shape, or by a known
// package directory in its path.
func agentFromToken(token string) (string, bool) {
	token = strings.Trim(token, `"'`)
	if token == "" || strings.HasPrefix(token, "-") {
		return "", false
	}
	base := lookupName(token)
	if name, ok := agentAliases[base]; ok {
		return name, true
	}
	// Muse's launcher execs `muse-bin-<version>`, so the running process
	// never carries a bare alias; a digit right after the prefix keeps
	// `muse-binary` out.
	if rest, ok := strings.CutPrefix(base, "muse-bin-"); ok && rest != "" && unicode.IsDigit(rune(rest[0])) {
		return "muse", true
	}
	dir := "/" + strings.Join(pathComponents(token), "/") + "/"
	for _, entry := range agentPackages {
		if strings.Contains(dir, "/"+entry.dir+"/") {
			return entry.agent, true
		}
	}
	return "", false
}

// lookupName is a token's base name as the vocabulary spells it: lowercased,
// with a wrapper suffix removed, so `Codex.js` and `/x/bin/codex` both look
// up as codex.
func lookupName(token string) string {
	components := pathComponents(token)
	if len(components) == 0 {
		return ""
	}
	name := strings.ToLower(components[len(components)-1])
	for _, suffix := range []string{".exe", ".cmd", ".bat", ".ps1", ".js"} {
		if strings.HasSuffix(name, suffix) {
			return strings.TrimSuffix(name, suffix)
		}
	}
	return name
}

// pathComponents splits a path on either separator, dropping empty parts.
func pathComponents(token string) []string {
	var out []string
	for _, part := range strings.FieldsFunc(token, func(r rune) bool { return r == '/' || r == '\\' }) {
		out = append(out, part)
	}
	return out
}

// isPython reports a python runtime by any versioned spelling: python,
// python3, python3.12.
func isPython(name string) bool {
	if name == "python" {
		return true
	}
	version, ok := strings.CutPrefix(name, "python")
	if !ok || version == "" {
		return false
	}
	for _, part := range strings.Split(version, ".") {
		if part == "" {
			return false
		}
		for _, r := range part {
			if !unicode.IsDigit(r) {
				return false
			}
		}
	}
	return true
}
