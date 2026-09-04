package agentstate

import (
	"io/fs"
	"reflect"
	"sort"
	"strings"
	"testing"
)

// §3.7 Every vendored manifest parses, validates and compiles under Go's
// regexp. This is the test that reports a pattern RE2 cannot take: upstream
// writes for Rust's regex crate, whose syntax is close to RE2's but not
// identical, and a manifest that fails here is fixed by rewriting the
// pattern minimally, with a comment, never by dropping the rule.
func TestEveryVendoredManifestLoads(t *testing.T) {
	t.Parallel()
	manifests, err := loadAll(manifestFS)
	if err != nil {
		t.Fatalf("loading the vendored manifests: %v", err)
	}

	entries, err := fs.ReadDir(manifestFS, "manifests")
	if err != nil {
		t.Fatal(err)
	}
	var files []string
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".toml") {
			files = append(files, entry.Name())
		}
	}
	if len(files) != 21 {
		t.Errorf("%d manifests embedded, want the 21 vendored: %v", len(files), files)
	}

	// Every manifest is reachable by the canonical name the agent listing
	// reports, which is the manifest's id — upstream names its files after
	// the product (antigravity.toml, github-copilot.toml) and its ids after
	// the agent (agy, copilot).
	want := []string{"agy", "amp", "claude", "cline", "codex", "copilot", "cursor", "devin", "droid",
		"gemini", "grok", "hermes", "kilo", "kimi", "kiro", "maki", "muse", "opencode", "pi", "qodercli", "qwen"}
	if got := Agents(); !reflect.DeepEqual(got, want) {
		t.Errorf("manifests are keyed %v, want %v", got, want)
	}
	for _, alias := range []string{"claude-code", "cursor-agent", "antigravity", "github-copilot", "ghcs"} {
		if _, ok := manifests[alias]; !ok {
			t.Errorf("alias %q resolves to no manifest", alias)
		}
	}
	for _, none := range []string{"aider", "goose", "omp", "mastracode"} {
		if _, ok := Lookup(none); ok {
			t.Errorf("%s has a manifest, but upstream ships none", none)
		}
	}
}

// §3.7 The rule language, transcribed from upstream's own semantics test:
// priority beats file order, nested any/all/not gates compose, a not gate
// vetoes, and line_regex is anchored per line. Plus the one departure: no
// match is unknown, never idle.
func TestRuleSemantics(t *testing.T) {
	t.Parallel()
	m, err := ParseManifest(`
id = "codex"

[[rules]]
id = "low_contains"
state = "idle"
priority = 1
contains = ["match"]

[[rules]]
id = "high_nested_gates"
state = "working"
priority = 10
contains = ["match"]
all = [
  { any = [{ regex = ["w[io]n"] }, { contains = ["fallback"] }] },
]
not = [
  { contains = ["blocked"] },
]

[[rules]]
id = "line_regex"
state = "blocked"
priority = 20
line_regex = ["^exact line$"]

[[rules]]
id = "overlay"
state = "unknown"
priority = 30
skip_state_update = true
contains = ["overlay"]
`)
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		screen string
		want   Result
	}{
		{"match win", Result{State: Working, Rule: "high_nested_gates"}},
		// contains is case-insensitive; regex is not, so the nested gate fails.
		{"MATCH WIN", Result{State: Idle, Rule: "low_contains"}},
		{"match win blocked", Result{State: Idle, Rule: "low_contains"}},
		{"before\nexact line\nafter", Result{State: Blocked, Rule: "line_regex"}},
		{"exact line trailing", Result{State: Unknown}},
		{"ordinary prompt text", Result{State: Unknown}},
		{"match win\noverlay", Result{State: Unknown, Rule: "overlay", SkipStateUpdate: true}},
	}
	for _, c := range cases {
		if got := m.Evaluate(Input{Screen: c.screen}); got != c.want {
			t.Errorf("%q: %+v, want %+v", c.screen, got, c.want)
		}
	}
	if got := m.RuleIDs(); !reflect.DeepEqual(got, []string{"overlay", "line_regex", "high_nested_gates", "low_contains"}) {
		t.Errorf("rules ordered %v", got)
	}
}

// The parser and validator refuse what upstream refuses: unknown fields, an
// empty rule set, a bad region, a pattern that does not compile, a skip rule
// that names a state, and a gate with nothing positive in it.
func TestManifestValidation(t *testing.T) {
	t.Parallel()
	bad := map[string]string{
		"unknown field":       "id = \"x\"\nbogus = 1\n[[rules]]\nid = \"r\"\ncontains = [\"a\"]\n",
		"no rules":            "id = \"x\"\n",
		"bad region":          "id = \"x\"\n[[rules]]\nid = \"r\"\nregion = \"sideways\"\ncontains = [\"a\"]\n",
		"bad regex":           "id = \"x\"\n[[rules]]\nid = \"r\"\nregex = ['(?<=a)b']\n",
		"skip with state":     "id = \"x\"\n[[rules]]\nid = \"r\"\nstate = \"idle\"\nskip_state_update = true\ncontains = [\"a\"]\n",
		"no positive gate":    "id = \"x\"\n[[rules]]\nid = \"r\"\nnot = [{ contains = [\"a\"] }]\n",
		"unknown gate field":  "id = \"x\"\n[[rules]]\nid = \"r\"\nany = [{ contains = [\"a\"], state = \"idle\" }]\n",
		"bad state":           "id = \"x\"\n[[rules]]\nid = \"r\"\nstate = \"thinking\"\ncontains = [\"a\"]\n",
		"toml outside subset": "id = \"x\"\n[rules]\nid = \"r\"\n",
	}
	for name, src := range bad {
		if _, err := ParseManifest(src); err == nil {
			t.Errorf("%s: parsed, want an error", name)
		}
	}
}

// Regions, checked where their edges are: the bottom-N region reads from
// the N-th non-blank line and keeps blank lines below it; the top-N region
// stops at the N-th; a prompt box is the last two rules; the last rule
// splits what is after it; a codex prompt marker is current only when no
// response block follows it.
func TestRegions(t *testing.T) {
	t.Parallel()
	screen := "one\n\ntwo\nthree\n\n"
	cases := []struct{ spec, want string }{
		{"bottom_non_empty_lines(2)", "two\nthree\n\n"},
		{"bottom_non_empty_lines(9)", "one\n\ntwo\nthree\n\n"},
		{"bottom_non_empty_lines(0)", ""},
		{"bottom_lines(1)", "\n"},
		{"top_non_empty_lines(1)", "one\n"},
		{"top_non_empty_lines(2)", "one\n\ntwo\n"},
		{"top_non_empty_lines(01)", ""},
		{"whole_recent", screen},
		{"osc_title", "title"},
		{"osc_progress", "4;0;"},
	}
	for _, c := range cases {
		if got := region(Input{Screen: screen, OSCTitle: "title", OSCProgress: "4;0;"}, c.spec); got != c.want {
			t.Errorf("%s: %q, want %q", c.spec, got, c.want)
		}
	}

	box := "above\n────────\n❯ typed\n──────── label\nfooter\n"
	if got := region(Input{Screen: box}, "prompt_box_body"); got != "❯ typed\n" {
		t.Errorf("prompt_box_body: %q", got)
	}
	if got := region(Input{Screen: box}, "last_non_empty_above_prompt_box"); got != "above" {
		t.Errorf("last_non_empty_above_prompt_box: %q", got)
	}
	if got := region(Input{Screen: box}, "after_last_horizontal_rule"); got != "footer\n" {
		t.Errorf("after_last_horizontal_rule: %q", got)
	}
	if got := region(Input{Screen: "── x\nfooter"}, "after_last_horizontal_rule"); got != "── x\nfooter" {
		t.Errorf("a short rule with a label is not a rule: %q", got)
	}

	codex := "• done\n› typing\nmore\n"
	if got := region(Input{Screen: codex}, "after_last_prompt_marker"); got != "more\n" {
		t.Errorf("after_last_prompt_marker: %q", got)
	}
	if got := region(Input{Screen: codex}, "whole_recent_without_current_prompt_marker"); got != "" {
		t.Errorf("a current prompt empties the region: %q", got)
	}
	past := "› asked\n• answering\n"
	if got := region(Input{Screen: past}, "whole_recent_without_current_prompt_marker"); got != past {
		t.Errorf("a prompt above a response is not current: %q", got)
	}
}

// §3.7 The screens below are transcribed from upstream's manifest tests and
// from live captures of the agents, and each names the rule that must win.
// claude: a live turn is working, the composer box is idle, a permission
// prompt is blocked, and a transcript viewer is a skip rule — unknown, with
// the rule named. codex: its Working line and its confirmation prompt. pi:
// only its literal, so a quiet prompt is unknown rather than guessed.
func TestVendoredManifestsReadTheScreens(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		agent  string
		in     Input
		want   Result
		wantSt State
	}{
		{
			name:  "claude live turn",
			agent: "claude",
			in: Input{Screen: "────────────────────────────────────────────────────────────────\n" +
				"❯\n" +
				"────────────────────────────────────────────────────────────────\n" +
				"  ⏵⏵ auto mode on · 1 shell · esc to interrupt\n"},
			want: Result{State: Working, Rule: "live_turn_working"},
		},
		{
			name:  "claude activity summary",
			agent: "claude",
			in:    Input{Screen: "\n✻ Sautéing… (12s · ↓ 1.2k tokens · esc to interrupt)\n"},
			want:  Result{State: Working, Rule: "live_turn_working"},
		},
		{
			name:  "claude idle prompt box with a background shell",
			agent: "claude",
			in: Input{Screen: "✻ Sautéed for 10s · 1 shell still running\n\n" +
				"──────────────────────────────────────────────────────── WINDOWS ─\n" +
				"❯\n" +
				"────────────────────────────────────────────────────────────────\n" +
				"  ⏵⏵ auto mode on · 1 shell · ← for agents                     /rc\n"},
			want: Result{State: Idle, Rule: "live_prompt_box"},
		},
		{
			name:  "claude bash permission prompt",
			agent: "claude",
			in: Input{Screen: "do you want to proceed?\n" +
				"bash command: rm -rf /tmp/test\n" +
				"❯ 1. Yes\n" +
				"  2. No\n\n" +
				"Esc to cancel · Tab to amend · ctrl+e to explain\n" +
				"  ⏵⏵ auto mode on · 1 shell · ← for agents\n"},
			want: Result{State: Blocked, Rule: "bash_permission_prompt"},
		},
		{
			name:  "claude permission prompt outranks an idle title",
			agent: "claude",
			in: Input{Screen: "do you want to proceed?\nbash command: rm -rf /tmp/test\n❯ 1. Yes\n   2. No\n\nEsc to cancel · Tab to amend · ctrl+e to explain\n",
				OSCTitle: "✳ Claude Code"},
			want: Result{State: Blocked, Rule: "bash_permission_prompt"},
		},
		{
			name:  "claude working title",
			agent: "claude",
			in:    Input{OSCTitle: "⠂ project"},
			want:  Result{State: Working, Rule: "osc_title_working"},
		},
		{
			name:  "claude idle title",
			agent: "claude",
			in:    Input{OSCTitle: "✳ Claude Code"},
			want:  Result{State: Idle, Rule: "osc_title_idle"},
		},
		{
			name:  "claude transcript viewer is a skip rule",
			agent: "claude",
			in:    Input{Screen: "❯ 1. Yes\n\nShowing detailed transcript · ctrl+o to toggle\n"},
			want:  Result{State: Unknown, Rule: "transcript_viewer", SkipStateUpdate: true},
		},
		{
			name:  "claude with nothing on screen",
			agent: "claude",
			in:    Input{Screen: "  ⏵⏵ auto mode on · 1 shell · ← for agents\n"},
			want:  Result{State: Unknown},
		},
		{
			name:  "codex working line",
			agent: "codex",
			in: Input{Screen: "• I’ll run it and wait for completion.\n\n" +
				"◦ Working (1m 16s • esc to interrupt) · 1 background…\n\n" +
				"› Use /skills to list available skills\n\n" +
				"gpt-5.6-sol default · /work\n", OSCTitle: "project"},
			want: Result{State: Working, Rule: "screen_working_fallback"},
		},
		{
			name:  "codex idle title",
			agent: "codex",
			in:    Input{Screen: "› Use /skills to list available skills\n", OSCTitle: "project"},
			want:  Result{State: Idle, Rule: "osc_title_idle"},
		},
		{
			name:  "codex confirmation outranks its working line",
			agent: "codex",
			in:    Input{Screen: "• Working (4s • esc to interrupt)\n› 1. Yes, proceed\nPress enter to confirm or esc to cancel\n", OSCTitle: "project"},
			want:  Result{State: Blocked, Rule: "live_strong_blocker"},
		},
		{
			name:  "codex with no title and no working line",
			agent: "codex",
			in:    Input{Screen: "› Use /skills to list available skills\n"},
			want:  Result{State: Unknown},
		},
		{
			name:  "pi working literal",
			agent: "pi",
			in:    Input{Screen: "> fix the tests\nWorking...\n"},
			want:  Result{State: Working, Rule: "working_literal"},
		},
		{
			name:  "pi at its prompt is unknown, not idle",
			agent: "pi",
			in:    Input{Screen: "> \n"},
			want:  Result{State: Unknown},
		},
	}
	for _, c := range cases {
		m, ok := Lookup(c.agent)
		if !ok {
			t.Fatalf("%s: no manifest", c.agent)
		}
		if got := m.Evaluate(c.in); got != c.want {
			t.Errorf("%s: %+v, want %+v", c.name, got, c.want)
		}
		if got := Detect(c.agent, c.in); got != c.want.State {
			t.Errorf("%s: Detect says %s, want %s", c.name, got, c.want.State)
		}
	}
	if got := Detect("aider", Input{Screen: "Working..."}); got != Unknown {
		t.Errorf("an agent with no manifest reads %s, want unknown", got)
	}
}

// The TOML subset: what the manifests use parses to the expected tree, and
// what they do not use is refused rather than misread.
func TestTOMLSubset(t *testing.T) {
	t.Parallel()
	doc, err := parseTOML("# comment\nid = \"a\\u00e9\" # trailing\nn = -3\nb = true\nlist = [\n  'x', # inner\n  \"y\",\n]\n[[rules]]\nid = 'r'\nany = [ { contains = [\"a\"], not = [ { regex = ['^\\s*$'] } ] } ]\n[[rules]]\nid = 'q'\n")
	if err != nil {
		t.Fatal(err)
	}
	want := tomlTable{
		"id": "aé", "n": int64(-3), "b": true, "list": []any{"x", "y"},
		"rules": []any{
			tomlTable{"id": "r", "any": []any{tomlTable{"contains": []any{"a"}, "not": []any{tomlTable{"regex": []any{`^\s*$`}}}}}},
			tomlTable{"id": "q"},
		},
	}
	if !reflect.DeepEqual(doc, want) {
		t.Errorf("parsed\n\t%#v\nwant\n\t%#v", doc, want)
	}
	for name, src := range map[string]string{
		"table header":   "[rules]\n",
		"dotted key":     "a.b = 1\n",
		"multi-line":     "s = \"\"\"x\"\"\"\n",
		"trailing junk":  "a = 1 b = 2\n",
		"unterminated":   "a = 'x\n",
		"duplicate key":  "a = 1\na = 2\n",
		"float":          "a = 1.5\n",
		"unknown escape": "a = \"\\q\"\n",
	} {
		if _, err := parseTOML(src); err == nil {
			t.Errorf("%s: parsed, want an error", name)
		}
	}
	keys := make([]string, 0, len(doc))
	for k := range doc {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	if !reflect.DeepEqual(keys, []string{"b", "id", "list", "n", "rules"}) {
		t.Errorf("keys %v", keys)
	}
}
