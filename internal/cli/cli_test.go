package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/husniadil/olympus"
	"github.com/husniadil/olympus/backend"
	"github.com/husniadil/olympus/internal/cli"
)

var counter atomic.Int64

// impersonatedAgents are the names the test binary answers to as a stand-in
// agent: copied under one of these names and started in a pane, it sleeps,
// so the pane holds a process called claude or codex without any real agent
// being started under a test. Keyed on argv rather than an environment
// variable because the spawn environment is scrubbed (§1.1).
var impersonatedAgents = map[string]bool{"claude": true, "codex": true}

// paintedScreens are what a stand-in agent prints before it sleeps, when
// started as `<agent> --paint <name>`: a screen shaped the way the real
// agent draws its own, transcribed from the agent's manifest fixtures. It
// is named rather than passed as text so nothing crosses a shell.
var paintedScreens = map[string]string{
	"claude-working": "────────────────────────────────────────────────────────────────\n" +
		"❯\n" +
		"────────────────────────────────────────────────────────────────\n" +
		"  ⏵⏵ auto mode on · 1 shell · esc to interrupt\n",
}

// TestMain lets the test binary re-exec itself as the stand-in agent, the way
// the engine's tests re-exec it as a slot holder.
func TestMain(m *testing.M) {
	if impersonatedAgents[filepath.Base(os.Args[0])] {
		if len(os.Args) == 3 && os.Args[1] == "--paint" {
			fmt.Print(paintedScreens[os.Args[2]])
		}
		time.Sleep(10 * time.Minute)
		return
	}
	os.Exit(m.Run())
}

type result struct {
	code   int
	stdout string
	stderr string
}

func run(t *testing.T, args ...string) result {
	t.Helper()
	var out, errOut bytes.Buffer
	code := cli.Execute(context.Background(), args, &out, &errOut, strings.NewReader(""))
	return result{code: code, stdout: out.String(), stderr: errOut.String()}
}

// envelope decodes the structured output, failing if stdout is not exactly one
// envelope — which is the contract a consumer piping stdout depends on.
func (r result) envelope(t *testing.T) cli.Envelope {
	t.Helper()
	var e cli.Envelope
	if err := json.Unmarshal([]byte(r.stdout), &e); err != nil {
		t.Fatalf("stdout is not a single JSON envelope: %v\nstdout was:\n%s", err, r.stdout)
	}
	return e
}

// isolation returns the flags that keep a test off the operator's live servers
// (§2.9). Every invocation in this file carries them.
func isolation(t *testing.T) []string {
	t.Helper()
	skipUnlessFull(t)
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is not installed")
	}
	socket := fmt.Sprintf("olyc-%d-%d", os.Getpid(), counter.Add(1))
	t.Cleanup(func() {
		_ = exec.Command("tmux", "-L", socket, "kill-server").Run()
		dir := os.Getenv("TMUX_TMPDIR")
		if dir == "" {
			dir = "/tmp"
		}
		_ = os.Remove(filepath.Join(dir, fmt.Sprintf("tmux-%d", os.Getuid()), socket))
	})
	return []string{"--backend", "tmux", "--socket", socket}
}

func name() string { return fmt.Sprintf("oly-c%d", counter.Add(1)) }

func TestVersionInBothModes(t *testing.T) {
	human := run(t, "version")
	if human.code != 0 {
		t.Errorf("exit %d, want 0", human.code)
	}
	if !strings.Contains(human.stdout, "olympus") {
		t.Errorf("stdout %q does not name the program", human.stdout)
	}

	structured := run(t, "version", "--json")
	if structured.code != 0 {
		t.Errorf("exit %d, want 0", structured.code)
	}
	e := structured.envelope(t)
	if !e.OK {
		t.Error("the envelope reports failure")
	}
}

// §12.2's specific failure mode: a framework's own flag validation typically
// prints and exits BEFORE application code runs. Whether a usage failure is
// machine-readable must not depend on which layer caught it.
func TestFlagErrorsReachTheEnvelope(t *testing.T) {
	cases := map[string][]string{
		"an unknown flag":         {"ls", "--nonesuch", "--json"},
		"a bad flag value":        {"screen", "x", "--history", "banana", "--json"},
		"too few positionals":     {"screen", "--json"},
		"too many positionals":    {"ls", "extra", "--json"},
		"an unknown subcommand":   {"nonesuch", "--json"},
		"a missing required flag": {"view", "focus", "x", "--row", "0", "--json"},
	}
	for what, args := range cases {
		got := run(t, args...)
		if got.code != 2 {
			t.Errorf("%s: exit %d, want 2", what, got.code)
		}
		e := got.envelope(t)
		if e.OK {
			t.Errorf("%s: the envelope reports success", what)
		}
		if e.Error == nil || e.Error.Code != "USAGE" {
			t.Errorf("%s: code %v, want USAGE", what, e.Error)
		}
	}
}

func TestAnUnknownBackendIsUsage(t *testing.T) {
	got := run(t, "ls", "--backend", "screen", "--json")
	if got.code != 2 {
		t.Errorf("exit %d, want 2", got.code)
	}
	e := got.envelope(t)
	if e.Error == nil || e.Error.Code != "USAGE" {
		t.Fatalf("code %v, want USAGE", e.Error)
	}
	if !strings.Contains(e.Error.Message, "screen") {
		t.Errorf("the message does not name the rejected value: %s", e.Error.Message)
	}
}

// Nothing diagnostic ever goes to stdout, and no payload ever goes to stderr. A
// consumer piping stdout into a parser must never have to filter it.
func TestHumanFailuresGoToStderrAndLeaveStdoutEmpty(t *testing.T) {
	got := run(t, "ls", "--backend", "screen")
	if got.code != 2 {
		t.Errorf("exit %d, want 2", got.code)
	}
	if got.stdout != "" {
		t.Errorf("stdout carried a diagnostic: %q", got.stdout)
	}
	if !strings.Contains(got.stderr, "screen") {
		t.Errorf("stderr does not explain the failure: %q", got.stderr)
	}
}

func TestDoctorAlwaysSucceeds(t *testing.T) {
	got := run(t, "doctor")
	if got.code != 0 {
		t.Errorf("exit %d, want 0", got.code)
	}
	for _, want := range []string{"RESOLVED", "BACKENDS", "CAPABILITIES", "WHERE SESSIONS LIVE"} {
		if !strings.Contains(got.stdout, want) {
			t.Errorf("the diagnostic has no %q section:\n%s", want, got.stdout)
		}
	}

	structured := run(t, "doctor", "--json")
	if structured.code != 0 {
		t.Errorf("exit %d, want 0", structured.code)
	}
	structured.envelope(t)
}

func TestSessionLifecycleInBothModes(t *testing.T) {
	flags := isolation(t)
	target := name()

	created := run(t, append(flags, "start", target, "--json")...)
	if created.code != 0 {
		t.Fatalf("start: exit %d, stderr %q", created.code, created.stderr)
	}
	e := created.envelope(t)
	if !e.OK {
		t.Fatalf("start reported failure: %+v", e.Error)
	}
	if e.Backend != "tmux" {
		t.Errorf("envelope backend %q, want tmux — the resolved backend must be disclosed", e.Backend)
	}

	listed := run(t, append(flags, "ls")...)
	if listed.code != 0 || !strings.Contains(listed.stdout, target) {
		t.Errorf("ls did not show the session: exit %d\n%s", listed.code, listed.stdout)
	}

	stopped := run(t, append(flags, "stop", target, "--json")...)
	if stopped.code != 0 {
		t.Errorf("stop: exit %d, stderr %q", stopped.code, stopped.stderr)
	}
}

// An empty list is a real answer, and it names the backend — an empty list that
// should not be empty is exactly when a user needs to learn backends are scoped.
func TestAnEmptyListingNamesTheBackend(t *testing.T) {
	flags := isolation(t)
	got := run(t, append(flags, "ls")...)
	if got.code != 0 {
		t.Fatalf("exit %d, want 0", got.code)
	}
	if !strings.Contains(got.stdout, "tmux") {
		t.Errorf("the empty listing does not name the backend: %q", got.stdout)
	}

	structured := run(t, append(flags, "ls", "--json")...)
	// Never null: an empty collection serializes as [].
	if !strings.Contains(structured.stdout, `"data":[]`) {
		t.Errorf("an empty listing did not serialize as []: %s", structured.stdout)
	}
}

// info answers the presence tri-state and must NOT fail on an absent target:
// collapsing absent into an error would destroy the distinction between
// "definitely gone" and "could not ask".
func TestInfoOnAnAbsentTargetSucceedsWithStateAbsent(t *testing.T) {
	flags := isolation(t)
	got := run(t, append(flags, "info", "oly-never-existed", "--json")...)
	if got.code != 0 {
		t.Fatalf("exit %d, want 0 — absent is an answer, not a failure", got.code)
	}
	if !strings.Contains(got.stdout, `"state":"absent"`) {
		t.Errorf("the payload does not report absent: %s", got.stdout)
	}
}

// Every other verb DOES report not-found, with its own exit code.
func TestAddressingAnAbsentSessionIsExitThree(t *testing.T) {
	flags := isolation(t)
	for _, args := range [][]string{
		{"screen", "oly-never-existed"},
		{"type", "oly-never-existed", "hello"},
		{"press", "oly-never-existed", "enter"},
	} {
		got := run(t, append(flags, args...)...)
		if got.code != 3 {
			t.Errorf("%v: exit %d, want 3", args, got.code)
		}
	}
}

// §12.1: run's human path exits with the COMMAND's own status so it composes in
// a pipeline, while --json exits 0 and carries that status in the payload.
func TestRunExitCodeDeviation(t *testing.T) {
	flags := isolation(t)
	target := name()
	if got := run(t, append(flags, "start", target)...); got.code != 0 {
		t.Fatalf("start: exit %d, stderr %q", got.code, got.stderr)
	}
	t.Cleanup(func() { run(t, append(flags, "stop", target, "--force")...) })

	// Warm the shell, or the injected line lands before it is reading (§16).
	if got := run(t, append(flags, "run", target, `printf 'warm-%d\n' 1`)...); got.code != 0 {
		t.Fatalf("warming run: exit %d, stderr %q", got.code, got.stderr)
	}

	human := run(t, append(flags, "run", target, "sh -c 'exit 5'")...)
	if human.code != 5 {
		t.Errorf("the human path exited %d, want the command's own 5", human.code)
	}

	structured := run(t, append(flags, "run", target, "sh -c 'exit 5'", "--json")...)
	if structured.code != 0 {
		t.Errorf("the structured path exited %d, want 0 — the command's status belongs in the payload", structured.code)
	}
	e := structured.envelope(t)
	if !e.OK {
		t.Fatalf("the run reported failure: %+v", e.Error)
	}
	data, _ := e.Data.(map[string]any)
	if code, ok := data["exit_code"].(float64); !ok || int(code) != 5 {
		t.Errorf("data.exit_code is %v, want 5", data["exit_code"])
	}
}

// Degraded-operation disclosure is narration: it belongs on stderr, never in
// the data channel.
func TestWarningsNeverReachStdout(t *testing.T) {
	if _, err := exec.LookPath("zmx"); err != nil {
		t.Skip("zmx is not installed")
	}
	dir, err := os.MkdirTemp(os.TempDir(), "olyc")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })

	flags := []string{"--backend", "zmx", "--zmx-dir", dir}
	target := name()
	if got := run(t, append(flags, "start", target)...); got.code != 0 {
		t.Fatalf("start: exit %d, stderr %q", got.code, got.stderr)
	}
	t.Cleanup(func() { run(t, append(flags, "stop", target, "--force")...) })

	got := run(t, append(flags, "screen", target, "--history", "500")...)
	if got.code != 0 {
		t.Fatalf("screen: exit %d, stderr %q", got.code, got.stderr)
	}
	if !strings.Contains(got.stderr, "warning:") {
		t.Errorf("the degraded history request disclosed nothing on stderr: %q", got.stderr)
	}
	if strings.Contains(got.stdout, "warning:") {
		t.Errorf("a warning reached the data channel: %q", got.stdout)
	}
}

// Quiet suppresses narration only. It must never change the data channel.
func TestQuietDoesNotChangeTheDataChannel(t *testing.T) {
	flags := isolation(t)
	loud := run(t, append(flags, "ls", "--json")...)
	quiet := run(t, append(flags, "ls", "--json", "-q")...)
	if loud.stdout != quiet.stdout {
		t.Errorf("quiet changed the structured output:\n%q\nvs\n%q", loud.stdout, quiet.stdout)
	}
}

// The help of every operation with a table says which output to parse, so a
// person learns it before writing the script rather than after it breaks.
func TestHelpPointsScriptsAtTheStableOutput(t *testing.T) {
	for _, verb := range []string{"ls", "info", "start", "stop"} {
		got := run(t, verb, "--help")
		if !strings.Contains(got.stdout, "--json") {
			t.Errorf("%s --help does not mention --json:\n%s", verb, got.stdout)
		}
	}
}

// The verbs added alongside the ergonomic layer, exercised through the door
// rather than only through the library beneath it.
func TestNewVerbIsStrictAndStartIsNot(t *testing.T) {
	flags := isolation(t)
	target := name()

	if got := run(t, append(flags, "new", target)...); got.code != 0 {
		t.Fatalf("new: exit %d, stderr %q", got.code, got.stderr)
	}
	t.Cleanup(func() { run(t, append(flags, "stop", target, "--force")...) })

	// A name that exists is a conflict, which is exit 6 — not a silent reuse.
	again := run(t, append(flags, "new", target, "--json")...)
	if again.code != 6 {
		t.Errorf("new on an existing name: exit %d, want 6", again.code)
	}
	if e := again.envelope(t); e.Error == nil || e.Error.Code != "CONFLICT" {
		t.Errorf("code %v, want CONFLICT", e.Error)
	}

	// start reuses it instead, which is the whole difference between them.
	if got := run(t, append(flags, "start", target)...); got.code != 0 {
		t.Errorf("start on an existing name: exit %d, want 0", got.code)
	}
}

func TestPanesVerbListsAllOrOne(t *testing.T) {
	flags := isolation(t)
	first, second := name(), name()
	run(t, append(flags, "start", first)...)
	run(t, append(flags, "start", second)...)
	t.Cleanup(func() {
		run(t, append(flags, "stop", first, "--force")...)
		run(t, append(flags, "stop", second, "--force")...)
	})

	all := run(t, append(flags, "panes", "--json")...)
	if all.code != 0 {
		t.Fatalf("panes: exit %d, stderr %q", all.code, all.stderr)
	}
	rows, _ := all.envelope(t).Data.([]any)
	if len(rows) < 2 {
		t.Errorf("panes with no target returned %d rows, want at least 2", len(rows))
	}

	one := run(t, append(flags, "panes", first, "--json")...)
	oneRows, _ := one.envelope(t).Data.([]any)
	if len(oneRows) == 0 {
		t.Errorf("panes for one session returned nothing")
	}
	for _, row := range oneRows {
		pane, _ := row.(map[string]any)
		if pane["session_name"] != first {
			t.Errorf("a pane of %v appeared while listing %s", pane["session_name"], first)
		}
	}
}

func TestCapabilitiesVerbAnswersDirectly(t *testing.T) {
	flags := isolation(t)
	got := run(t, append(flags, "capabilities", "--json")...)
	if got.code != 0 {
		t.Fatalf("capabilities: exit %d, stderr %q", got.code, got.stderr)
	}
	caps, _ := got.envelope(t).Data.(map[string]any)
	// tmux has views; a caller must be able to learn that WITHOUT provoking an
	// unsupported error first.
	if views, ok := caps["views"].(bool); !ok || !views {
		t.Errorf("capabilities did not report view support: %v", caps)
	}
	if _, ok := caps["tracks_alt_screen"]; !ok {
		t.Errorf("capabilities omits alt-screen tracking, which callers branch on: %v", caps)
	}
}

// A throwaway run has no target, and cannot be detached because there would be
// nothing left to poll.
func TestRunWithNoTargetUsesAThrowawaySession(t *testing.T) {
	flags := isolation(t)

	got := run(t, append(flags, "run", `printf 'cli-throw-%d\n' 3`)...)
	if got.code != 0 {
		t.Fatalf("throwaway run: exit %d, stderr %q", got.code, got.stderr)
	}
	if !strings.Contains(got.stdout, "cli-throw-3") {
		t.Errorf("stdout %q does not carry the command's output", got.stdout)
	}

	// Nothing left behind.
	listed := run(t, append(flags, "ls", "--json")...)
	if sessions, _ := listed.envelope(t).Data.([]any); len(sessions) != 0 {
		t.Errorf("the throwaway run left %d sessions behind", len(sessions))
	}

	detached := run(t, append(flags, "run", "sleep 1", "--detach", "--json")...)
	if detached.code != 2 {
		t.Errorf("a detached throwaway run: exit %d, want 2", detached.code)
	}
}

// The screen verb takes several targets and emits the map shape api §5
// specifies, not one screen per invocation.
func TestScreenVerbTakesSeveralTargets(t *testing.T) {
	flags := isolation(t)
	first, second := name(), name()
	run(t, append(flags, "start", first)...)
	run(t, append(flags, "start", second)...)
	t.Cleanup(func() {
		run(t, append(flags, "stop", first, "--force")...)
		run(t, append(flags, "stop", second, "--force")...)
	})

	got := run(t, append(flags, "screen", first, second, "--json")...)
	if got.code != 0 {
		t.Fatalf("screen: exit %d, stderr %q", got.code, got.stderr)
	}
	data, _ := got.envelope(t).Data.(map[string]any)
	screens, _ := data["screens"].(map[string]any)
	if len(screens) != 2 {
		t.Errorf("screen returned %d screens, want 2", len(screens))
	}
	if _, ok := data["meta"].(map[string]any); !ok {
		t.Errorf("screen returned no meta map: %v", data)
	}
}

// `self` answers from wherever it is run. Outside a session that answer is
// "nowhere", which is a result and not a failure — a script asking must be able
// to branch on it without treating an error as the answer.
func TestSelfOutsideASessionSucceeds(t *testing.T) {
	// Every variable Self reads, cleared. The suite is routinely RUN from
	// inside one of the sessions it describes — a herdr pane, a tmux session —
	// and an inherited one would make this case fail for the environment it
	// was run in rather than for anything the code does.
	for _, name := range []string{
		"ZMX_SESSION", "ZMX_DIR",
		"TMUX", "TMUX_PANE",
		"MEJA_SESSION_TARGET", "MEJA_SOCKET",
		"HERDR_PANE_ID", "HERDR_WORKSPACE_ID", "HERDR_TAB_ID",
		"HERDR_SESSION", "HERDR_SOCKET_PATH", "HERDR_CLIENT_SOCKET_PATH",
	} {
		t.Setenv(name, "")
	}

	got := run(t, "self", "--json")
	if got.code != 0 {
		t.Fatalf("exit %d, want 0 — being outside a session is an answer", got.code)
	}
	data, _ := got.envelope(t).Data.(map[string]any)
	if inside, _ := data["inside"].(bool); inside {
		t.Errorf("self claims to be inside a session: %v", data)
	}

	human := run(t, "self")
	if human.code != 0 || human.stdout == "" {
		t.Errorf("the human form said nothing: exit %d, stdout %q", human.code, human.stdout)
	}
}

// A human reading `olympus doctor` has to be told which of their tmux.conf
// lines Olympus overrides. Putting it only in --json would disclose it to the
// audience that was never going to be confused by it.
func TestDoctorTellsAHumanWhatItOverrides(t *testing.T) {
	// Both assertions are about the tmux pinning disclosure, which doctor can
	// only print when tmux is there to pin anything on.
	requireTmuxInstalled(t)
	got := run(t, "doctor")
	if got.code != 0 {
		t.Fatalf("exit %d, want 0", got.code)
	}
	if !strings.Contains(got.stdout, "history-limit") {
		t.Errorf("doctor's human output never mentions the options it pins:\n%s", got.stdout)
	}
}

// §17.5: the reader who needs this is the one wondering why a run reported a
// wrong exit code, or why their scrollback is shorter than they asked for. On a
// server Olympus did not start, the answer is that Olympus deliberately left it
// alone — which is invisible unless said.
func TestDoctorDistinguishesAServerItStartedFromOneItFound(t *testing.T) {
	// Both assertions are about the tmux pinning disclosure, which doctor can
	// only print when tmux is there to pin anything on.
	requireTmuxInstalled(t)
	got := run(t, "doctor")
	if got.code != 0 {
		t.Fatalf("exit %d, want 0", got.code)
	}
	if !strings.Contains(got.stdout, "only servers it starts") {
		t.Errorf("doctor never says that Olympus configures only servers it starts:\n%s", got.stdout)
	}
}

// Reading a status nobody set is an answer, not a failure, and the CLI has to
// preserve that: exit 0 with an empty value.
func TestStatusOnAnAbsentSessionIsNotFound(t *testing.T) {
	requireBackend(t)
	got := run(t, "status", "no-such-session-"+t.Name(), "--json")
	if got.code != 3 {
		t.Errorf("exit %d, want 3 (SESSION_NOT_FOUND)", got.code)
	}
}

// The verb is one shape: a target, and flags that decide whether we are
// reading, writing or blocking.
func TestStatusVerbExists(t *testing.T) {
	got := run(t, "status", "--help")
	if got.code != 0 {
		t.Fatalf("exit %d, want 0", got.code)
	}
	for _, flag := range []string{"--set", "--wait"} {
		if !strings.Contains(got.stdout, flag) {
			t.Errorf("status has no %s flag:\n%s", flag, got.stdout)
		}
	}
}

// Every backend Olympus supports must be reachable from the CLI and named
// where a reader looks for the list. A backend that works but is undiscoverable
// is one nobody uses.
func TestTheCLIOffersEveryBackend(t *testing.T) {
	help := run(t, "--help")
	if help.code != 0 {
		t.Fatalf("exit %d, want 0", help.code)
	}
	for _, name := range []string{"zmx", "tmux", "meja"} {
		if !strings.Contains(help.stdout, name) {
			t.Errorf("the --backend flag does not mention %q:\n%s", name, help.stdout)
		}
	}

	// The doctor matrix is where a reader compares them, so an absent backend
	// there reads as an unsupported one.
	doctor := run(t, "doctor")
	if !strings.Contains(doctor.stdout, "meja") {
		t.Errorf("doctor never mentions meja:\n%s", doctor.stdout)
	}

	// An unknown backend stays a usage error, and the message has to list what
	// IS legal or the correction is a guess.
	bad := run(t, "ls", "--backend", "nope")
	if bad.code != 2 {
		t.Errorf("exit %d for an unknown backend, want 2 (USAGE)", bad.code)
	}
	if !strings.Contains(bad.stderr, "meja") {
		t.Errorf("the unknown-backend message does not list meja: %s", bad.stderr)
	}
}

// The CLI counterpart of the MCP coverage test: every verb must reach meja, and
// every verb meja cannot serve must exit 7 (UNSUPPORTED) rather than 1
// (UNEXPECTED).
//
// The two exit codes are the whole point. One tells a script "choose another
// approach"; the other tells it "try again". A backend wired in carelessly
// returns the second for the first, and a script then retries forever against a
// capability that does not exist.
func TestEveryCLIVerbIsServedOrRefusedOnMeja(t *testing.T) {
	if err := exec.Command("meja", "version").Run(); err != nil {
		t.Skip("meja is not installed or not runnable")
	}
	dir, err := os.MkdirTemp(os.TempDir(), "olyc")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	socket := filepath.Join(dir, "m.sock")
	t.Cleanup(func() {
		_ = exec.Command("meja", "-S", socket, "kill-server").Run()
		_ = os.RemoveAll(dir)
	})

	name := fmt.Sprintf("oly-cli-meja-%d", os.Getpid())
	where := []string{"--backend", "meja", "--socket-path", socket}
	on := func(args ...string) result { return run(t, append(args, where...)...) }

	if got := on("start", name); got.code != 0 {
		t.Fatalf("start on meja exited %d: %s", got.code, got.stderr)
	}

	for _, args := range [][]string{
		{"ls"},
		{"panes"},
		{"info", name},
		{"capabilities"},
		{"doctor"},
		{"self"},
		{"type", name, "echo served"},
		{"press", name, "enter"},
		{"screen", name},
		{"paste", name, "one\ntwo"},
		{"send", name, "echo confirmed"},
		{"run", name, "echo ran"},
		{"wait", name, "ran", "--timeout", "15s"},
		{"version"},
		// The marker is always the caller's to choose; there is no default.
		{"exit-status", name, "OLYDONE"},
	} {
		if got := on(args...); got.code != 0 {
			t.Errorf("`%s` is not served on meja: exit %d %s", strings.Join(args, " "), got.code, got.stderr)
		}
	}

	// Four verbs are deliberately absent from both lists, and none of them is an
	// oversight: `attach` needs a real terminal and is covered at the backend,
	// where its argv and its refusals can be asserted without one; `mcp` starts
	// a server rather than answering; and `completion` and `help` are cobra's
	// own, identical on every backend.
	//
	// `watch` is deliberately absent from both lists. It IS served on meja — a
	// client is an output tap, so Follow works — and it streams until
	// interrupted, so running it here would hang rather than assert. The
	// conformance suite covers it at the backend, where it can be bounded.
	for _, args := range [][]string{
		{"view", "create", name},
		{"view", "ls", name},
		{"view", "focus", name, "--col", "0", "--row", "0"},
		{"server-env", "PATH"},
		{"status", name},
	} {
		got := on(args...)
		if got.code == 0 {
			continue // served after all, which is not a failure
		}
		if got.code != 7 {
			t.Errorf("`%s` on meja exits %d, want 7 (UNSUPPORTED): %s",
				strings.Join(args, " "), got.code, got.stderr)
		}
	}

	// `new` means "must not already exist", so it needs a name of its own.
	fresh := name + "-fresh"
	if got := on("new", fresh); got.code != 0 {
		t.Errorf("new on meja exited %d: %s", got.code, got.stderr)
	}
	if got := on("new", fresh); got.code != 6 {
		t.Errorf("new on an existing meja session exits %d, want 6 (CONFLICT)", got.code)
	}
	if got := on("stop", fresh); got.code != 0 {
		t.Errorf("stop on meja exited %d: %s", got.code, got.stderr)
	}

	// A detached run and its poll are one pair: the id only exists because the
	// run produced it, so testing either alone tests half a mechanism.
	started := on("run", name, "echo polled", "--detach", "--json")
	if started.code != 0 {
		t.Fatalf("a detached run on meja exited %d: %s", started.code, started.stderr)
	}
	data, _ := started.envelope(t).Data.(map[string]any)
	id, _ := data["command_id"].(string)
	if id == "" {
		t.Fatalf("a detached run returned no id to poll: %v", data)
	}
	if got := on("poll", name, id); got.code != 0 {
		t.Errorf("poll on meja exited %d: %s", got.code, got.stderr)
	}

	if got := on("stop", name); got.code != 0 {
		t.Errorf("stop on meja exited %d: %s", got.code, got.stderr)
	}
}

// §17.4: Olympus does not manage windows, and this pins what that MEANS on a
// backend that has them and a session somebody else has added one to.
//
// Two consequences, neither obvious, both easy to break by "improving" one of
// them:
//
//   - a capture follows the session's ACTIVE window, not window 0
//   - a pane id addresses the session, so it does NOT reach that pane
//
// The second reads like a bug until you see why §10 collapses a pane id to its
// owning session: every name comparison and every write-lock key is
// session-scoped, so a pane-precise target would key locks on something the
// rest of the system cannot see. Pinned here so nobody makes it "precise" and
// takes the locking with it.
func TestWindowsBelongToTheMultiplexerNotToOlympus(t *testing.T) {
	for _, be := range []struct {
		name      string
		newWindow func(socket, session string) *exec.Cmd
	}{
		{"tmux", func(socket, session string) *exec.Cmd {
			return exec.Command("tmux", "-S", socket, "new-window", "-t", "="+session)
		}},
		{"meja", func(socket, session string) *exec.Cmd {
			return exec.Command("meja", "-S", socket, "new-window", "-t", session)
		}},
	} {
		t.Run(be.name, func(t *testing.T) {
			// zmx is absent from this table on purpose: it has no window
			// concept at all, so there is no behaviour here to pin.
			probe := "version"
			if be.name == "tmux" {
				probe = "-V"
			}
			if err := exec.Command(be.name, probe).Run(); err != nil {
				t.Skipf("%s is not installed or not runnable", be.name)
			}
			dir, err := os.MkdirTemp(os.TempDir(), "olyw")
			if err != nil {
				t.Fatalf("MkdirTemp: %v", err)
			}
			socket := filepath.Join(dir, "s.sock")
			t.Cleanup(func() {
				_ = exec.Command(be.name, "-S", socket, "kill-server").Run()
				_ = os.RemoveAll(dir)
			})

			name := fmt.Sprintf("oly-win-%s-%d", be.name, os.Getpid())
			on := func(args ...string) result {
				return run(t, append(args, "--backend", be.name, "--socket-path", socket)...)
			}
			if got := on("start", name); got.code != 0 {
				t.Fatalf("start: %s", got.stderr)
			}

			// Added from OUTSIDE Olympus, which is the only way a second window
			// can exist: Olympus has no verb that makes one.
			if out, err := be.newWindow(socket, name).CombinedOutput(); err != nil {
				t.Fatalf("adding a window with %s: %v\n%s", be.name, err, out)
			}

			panes, _ := on("panes", "--json").envelope(t).Data.([]any)
			windows := map[float64]string{}
			for _, row := range panes {
				p, _ := row.(map[string]any)
				idx, _ := p["window_index"].(float64)
				id, _ := p["pane_id"].(string)
				windows[idx] = id
			}
			if len(windows) < 2 {
				t.Fatalf("a second window exists but Olympus reports %d: %v", len(windows), panes)
			}

			// WHICH window becomes active after somebody adds one differs
			// between multiplexers — measured: tmux switches to the new one,
			// meja does not — so the invariant is asserted without naming it.
			// A pane id and a session name must be indistinguishable as
			// targets. That is what "a pane id addresses the owning session"
			// means, and it holds wherever the active window happens to be.
			somePane := windows[0]
			if somePane == "" {
				t.Fatalf("no pane reported for window 0: %v", windows)
			}
			if got := on("send", name, "echo via-session-name"); got.code != 0 {
				t.Fatalf("send to the session: %s", got.stderr)
			}
			if got := on("send", somePane, "echo via-pane-id"); got.code != 0 {
				t.Fatalf("send to pane %s: %s", somePane, got.stderr)
			}

			// One capture sees both. If a pane id selected its own pane, one of
			// these two markers would have landed somewhere this capture cannot
			// reach — which is exactly the surprise being pinned against.
			//
			// Waited for rather than captured once: a verified send proves the
			// text LANDED, not that it RAN, so capturing straight after races
			// the shell's execution. Only the second marker ever lost that race,
			// because the first had a whole extra send's worth of time — which
			// is what made it read like a pane-addressing defect on a loaded
			// macOS runner rather than the timing bug it is.
			deadline := time.Now().Add(20 * time.Second)
			var screen string
			for {
				screen = on("screen", name).stdout
				if strings.Contains(screen, "via-session-name") && strings.Contains(screen, "via-pane-id") {
					break
				}
				if time.Now().After(deadline) {
					break
				}
				time.Sleep(200 * time.Millisecond)
			}
			if !strings.Contains(screen, "via-session-name") {
				t.Errorf("what was sent to the session name is not on the session's screen:\n%s", screen)
			}
			if !strings.Contains(screen, "via-pane-id") {
				t.Errorf("what was sent to pane %s is not on the session's screen — a pane id selected a pane, "+
					"but §10 makes it an address for the OWNING SESSION, which is what the write lock keys on",
					somePane)
			}
		})
	}
}

// requireBackend skips when no multiplexer is installed.
//
// Cases that assert a SESSION-level outcome need something to have sessions.
// Without this they fail with BACKEND_UNAVAILABLE, which is the CORRECT answer
// on such a machine — so the failure reports a working product as broken, and
// does it on exactly the machines least able to tell the difference.
func requireBackend(t *testing.T) {
	t.Helper()
	for _, name := range []string{"tmux", "zmx", "meja"} {
		if _, err := exec.LookPath(name); err == nil {
			return
		}
	}
	t.Skip("no terminal multiplexer is installed")
}

// skipUnlessFull skips work that drives a real multiplexer when the gate is
// running in short mode.
//
// `make test` is the loop a change is iterated against, and it has to be fast
// enough to run on every edit; `make test-full` is the gate before a commit.
// Splitting them is NOT a reduction in coverage — nothing is deleted, and the
// full gate still runs everything. It is a split between the two questions being
// asked: "did I just break the logic" is answerable in seconds, and paying a
// minute and a half for it means the answer gets asked less often, which is how
// coverage is really lost.
func skipUnlessFull(t *testing.T) {
	t.Helper()
	if testing.Short() {
		t.Skip("driving a real multiplexer; run `make test-full` for this")
	}
}

// requireTmuxInstalled skips a case that asserts tmux-specific output.
func requireTmuxInstalled(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is not installed")
	}
}

// §0.7: with no backend installed, the failure names every backend, says how to
// install each, and points at the diagnostic.
//
// Nothing installed is a supported state, and it must be a supported state on
// EVERY machine — not only on the ones that happen to lack a multiplexer.
//
// The gate now skips backend-driven cases when nothing can drive them, which is
// right, but it means the BACKEND_UNAVAILABLE path would otherwise be exercised
// only by accident: never on a developer's machine, never on a CI runner with
// tmux in the image. So it is simulated instead of waited for. An empty
// directory as the whole PATH removes zmx, tmux and meja at once without
// uninstalling anything.
//
// This is the first thing a new user meets, and the only thing standing between
// them and a message that tells them what to install.
func TestNothingInstalledIsExplainedRatherThanCrashed(t *testing.T) {
	t.Setenv("PATH", t.TempDir())

	got := run(t, "ls")
	if got.code != 4 {
		t.Errorf("exit %d, want 4 (BACKEND_UNAVAILABLE)", got.code)
	}
	// Narration on stderr, never on stdout: a consumer piping stdout into a
	// parser must not have to filter it (api §2.3).
	if got.stdout != "" {
		t.Errorf("the failure wrote to stdout, which a parser is reading:\n%s", got.stdout)
	}
	for _, want := range []string{"zmx", "tmux", "meja", "doctor"} {
		if !strings.Contains(got.stderr, want) {
			t.Errorf("the message never mentions %q, so it does not say what to do:\n%s", want, got.stderr)
		}
	}
}

// The same condition through the structured door has to be an envelope, because
// a program is reading it and a program cannot read prose.
func TestNothingInstalledIsAWellFormedFailureEnvelope(t *testing.T) {
	t.Setenv("PATH", t.TempDir())

	got := run(t, "ls", "--json")
	if got.code != 4 {
		t.Fatalf("exit %d, want 4 (BACKEND_UNAVAILABLE)", got.code)
	}
	envelope := got.envelope(t)
	if envelope.OK {
		t.Error("the envelope reports success with no multiplexer installed")
	}
	if envelope.Error == nil {
		t.Fatal("the failure envelope carries no error")
	}
	if envelope.Error.Code != backend.CodeBackendUnavailable {
		t.Errorf("code is %q, want %q", envelope.Error.Code, backend.CodeBackendUnavailable)
	}
}

// doctor is the one verb that must still SUCCEED, because explaining an empty
// environment is the whole reason it exists (§0.6). A diagnostic that fails when
// nothing is installed is useless at exactly the moment it is needed.
func TestDoctorStillSucceedsWithNothingInstalled(t *testing.T) {
	t.Setenv("PATH", t.TempDir())

	got := run(t, "doctor")
	if got.code != 0 {
		t.Fatalf("exit %d, want 0 — doctor must answer when nothing is installed\n%s%s",
			got.code, got.stdout, got.stderr)
	}
	for _, want := range []string{"zmx", "tmux", "meja"} {
		if !strings.Contains(got.stdout, want) {
			t.Errorf("doctor never mentions %q:\n%s", want, got.stdout)
		}
	}
}

// And through the structured door, where the shape matters as much as the
// success: install_hints is the field a caller reads to tell its user what to do.
func TestDoctorReportsEveryBackendAsMissingWithNothingInstalled(t *testing.T) {
	t.Setenv("PATH", t.TempDir())

	got := run(t, "doctor", "--json")
	if got.code != 0 {
		t.Fatalf("exit %d, want 0", got.code)
	}
	var diagnosis olympus.Diagnosis
	if err := json.Unmarshal(mustData(t, got.envelope(t)), &diagnosis); err != nil {
		t.Fatalf("the diagnosis is not decodable: %v", err)
	}
	for _, report := range diagnosis.Backends {
		if report.Installed {
			t.Errorf("%s reports installed with an empty PATH", report.Name)
		}
	}
	if len(diagnosis.InstallHints) != len(diagnosis.Backends) {
		t.Errorf("%d install hints for %d backends: a caller cannot tell the user what to install",
			len(diagnosis.InstallHints), len(diagnosis.Backends))
	}
	if diagnosis.Resolved.Problem == "" {
		t.Error("the diagnosis does not say why nothing resolved")
	}
}

func mustData(t *testing.T, envelope cli.Envelope) []byte {
	t.Helper()
	encoded, err := json.Marshal(envelope.Data)
	if err != nil {
		t.Fatalf("re-encoding the payload: %v", err)
	}
	return encoded
}

// api §2.3: stdout carries the payload, stderr carries narration, and a consumer
// piping stdout into a parser must never have to filter it.
//
// `attach` cannot honour that, and the reason is structural rather than a bug to
// patch: it HANDS stdout to the multiplexer's client, which then owns it. Every
// byte the session draws goes there, and so does the client's own failure —
// measured as `open terminal failed: terminal does not support clear` on stdout,
// with stderr empty and --json making no difference. There is no point at which
// Olympus could take those bytes back.
//
// So the promise is kept by refusing rather than by pretending: asking for a
// structured envelope from a terminal handoff is a caller mistake, and §12 makes
// a mistake fixable by changing one argument a usage error. Refusing costs a
// caller nothing they were actually getting — what they got before was
// unparseable either way.
func TestAttachRefusesTheStructuredEnvelope(t *testing.T) {
	got := run(t, "attach", "anything", "--json")
	if got.code != 2 {
		t.Fatalf("exit %d, want 2 (USAGE)\nstdout: %s\nstderr: %s", got.code, got.stdout, got.stderr)
	}
	envelope := got.envelope(t)
	if envelope.OK {
		t.Error("the envelope reports success")
	}
	if envelope.Error == nil || envelope.Error.Code != backend.CodeUsage {
		t.Errorf("the refusal is %v, want a usage error", envelope.Error)
	}
	// The refusal must say what to do instead, or it is just a wall.
	if envelope.Error != nil && !strings.Contains(envelope.Error.Message, "attach") {
		t.Errorf("the message does not name the verb it is about: %q", envelope.Error.Message)
	}
}

// Without --json, attach is untouched: it is an interactive handoff and the
// refusal above must not leak into the ordinary path.
func TestAttachWithoutJSONIsNotRefused(t *testing.T) {
	requireTmuxInstalled(t)
	got := run(t, append(isolation(t), "attach", "no-such-session-"+t.Name())...)
	if got.code == 2 {
		t.Errorf("plain attach was refused as a usage error:\n%s%s", got.stdout, got.stderr)
	}
}

// §5 "Detached run": `start` returns `{"command_id": "…"}`, and the id is the
// only thing a caller holds between starting a run and polling it.
//
// The key was pinned in the ergonomic layer's golden tests, and both doors were
// pointed at that one type — but nothing on an ordinary backend checked that
// either door actually EMITS it. The only cases reading the field were
// meja-specific, so they skip wherever meja is absent, which is everywhere it
// matters. Found by mutation: renaming the tag left both doors' suites green.
func TestADetachedRunReturnsAPollableCommandID(t *testing.T) {
	flags := isolation(t)
	name := fmt.Sprintf("oly-detach-%d-%d", os.Getpid(), counter.Add(1))
	if got := run(t, append(flags, "start", name)...); got.code != 0 {
		t.Fatalf("start: exit %d\n%s", got.code, got.stderr)
	}

	started := run(t, append(flags, "run", name, "echo detached-ok", "--detach", "--json")...)
	if started.code != 0 {
		t.Fatalf("a detached run exited %d: %s", started.code, started.stderr)
	}
	var payload struct {
		CommandID string `json:"command_id"`
	}
	if err := json.Unmarshal(mustData(t, started.envelope(t)), &payload); err != nil {
		t.Fatalf("decoding the start payload: %v", err)
	}
	if payload.CommandID == "" {
		t.Fatalf("no command_id in the start payload: %s", started.stdout)
	}

	// The id has to be usable, not merely present: a well-formed field the
	// other verb rejects is worse than no field.
	polled := run(t, append(flags, "poll", name, payload.CommandID, "--json")...)
	if polled.code != 0 {
		t.Fatalf("polling with the returned id exited %d: %s", polled.code, polled.stderr)
	}
}

// The same rule as attach, and for the same reason: `watch` writes the session's
// raw output stream — escape sequences included — straight to stdout. There is
// no envelope that could contain it without buffering the stream until it ends,
// which is the one thing a follower must not do.
//
// Its help text already SAID "there is no --json for this" and then accepted the
// flag anyway, emitting raw terminal bytes onto the channel a parser is reading.
// Saying it and enforcing it are different things, and only one of them is a
// contract.
func TestWatchRefusesTheStructuredEnvelope(t *testing.T) {
	got := run(t, "watch", "anything", "--json")
	if got.code != 2 {
		t.Fatalf("exit %d, want 2 (USAGE)\nstdout: %s\nstderr: %s", got.code, got.stdout, got.stderr)
	}
	envelope := got.envelope(t)
	if envelope.Error == nil || envelope.Error.Code != backend.CodeUsage {
		t.Errorf("the refusal is %v, want a usage error", envelope.Error)
	}
}

// The README's examples, run.
//
// Nothing tested them, and they are the first thing a reader types. One was
// already wrong: `wait build '\$\s*$'`, offered as "block until the prompt comes
// back", matches only a prompt ending in `$`. It fails under zsh, fish, or
// anything themed — including on the machine it was written on, which is how it
// was found.
//
// Deliberately small and prompt-independent. This asserts the commands still
// WORK; it does not try to verify the README's prose, which would be a test of a
// file's wording rather than of a contract.
func TestTheREADMEExamplesStillRun(t *testing.T) {
	flags := isolation(t)
	name := fmt.Sprintf("oly-readme-%d-%d", os.Getpid(), counter.Add(1))

	for _, example := range []struct {
		what string
		args []string
	}{
		{"start", []string{"start", name}},
		{"run", []string{"run", name, "echo hello from a real terminal"}},
		{"screen", []string{"screen", name}},
		{"send", []string{"send", name, "echo readme-ok"}},
		// On the command's own output, never on a prompt. This is the case the
		// README got wrong, so it is the one worth pinning.
		{"wait", []string{"wait", name, "readme-ok", "--timeout", "20s"}},
		{"panes", []string{"panes"}},
		{"capabilities", []string{"capabilities"}},
		{"status --set", []string{"status", name, "--set", "ready"}},
		{"status --wait", []string{"status", name, "--wait", "ready", "--timeout", "10s"}},
		{"throwaway run", []string{"run", "echo throwaway-ok"}},
		{"self", []string{"self"}},
		{"doctor", []string{"doctor"}},
		{"ls --json", []string{"ls", "--json"}},
		{"stop", []string{"stop", name}},
	} {
		got := run(t, append(flags, example.args...)...)
		if got.code != 0 {
			t.Errorf("README example %q exited %d\n%s%s", example.what, got.code, got.stdout, got.stderr)
		}
	}
}

// `olympus ls --json | jq '.data[].name'` is the README's one piped example, so
// the envelope has to carry a data array of objects with a name — which is what
// makes that pipeline print names rather than null.
func TestTheREADMEJQPipelineWouldFindNames(t *testing.T) {
	flags := isolation(t)
	name := fmt.Sprintf("oly-readme-jq-%d-%d", os.Getpid(), counter.Add(1))
	if got := run(t, append(flags, "start", name)...); got.code != 0 {
		t.Fatalf("start: %s", got.stderr)
	}
	t.Cleanup(func() { run(t, append(flags, "stop", name)...) })

	var rows []struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(mustData(t, run(t, append(flags, "ls", "--json")...).envelope(t)), &rows); err != nil {
		t.Fatalf("`ls --json` data is not an array of objects: %v", err)
	}
	found := false
	for _, row := range rows {
		if row.Name == name {
			found = true
		}
	}
	if !found {
		t.Errorf("`.data[].name` would not have found the session that exists: %v", rows)
	}
}

// The output a run has to produce to push its own start marker out of the
// deepest window the run will ask for. tmux keeps 50,000 lines of history, so
// the completion marker is still there to be read — it is the WINDOW that
// cannot reach the start, which is the case §6.2's relaxation is for.
const linesPastTheRunWindow = "seq 1 12000"

func disclosesTruncation(warnings []olympus.Warning) bool {
	for _, w := range warnings {
		if strings.Contains(w.Message, "output begins partway through") {
			return true
		}
	}
	return false
}

// §6.2: a run that recovered its exit code without its start marker owes the
// caller a disclosure, and the door is where that disclosure either reaches
// them or is dropped. The ergonomic layer produced it correctly while the CLI
// passed nil in its place, so nothing below this line could have caught it.
func TestATruncatedRunDisclosesItselfOnBothPaths(t *testing.T) {
	flags := isolation(t)
	target := name()
	if got := run(t, append(flags, "start", target)...); got.code != 0 {
		t.Fatalf("start: exit %d, stderr %q", got.code, got.stderr)
	}
	t.Cleanup(func() { run(t, append(flags, "stop", target, "--force")...) })

	// Warm the shell, or the injected line lands before it is reading (§16).
	if got := run(t, append(flags, "run", target, `printf 'warm-%d\n' 1`)...); got.code != 0 {
		t.Fatalf("warming run: exit %d, stderr %q", got.code, got.stderr)
	}

	structured := run(t, append(flags, "run", target, linesPastTheRunWindow, "--json")...)
	if structured.code != 0 {
		t.Fatalf("the run exited %d: %s", structured.code, structured.stderr)
	}
	e := structured.envelope(t)
	if !e.OK {
		t.Fatalf("the run reported failure: %+v", e.Error)
	}
	// The whole point: it is an ANSWER, not a timeout. Before the relaxation
	// this run burned its full budget and returned nothing.
	data, _ := e.Data.(map[string]any)
	if code, ok := data["exit_code"].(float64); !ok || int(code) != 0 {
		t.Errorf("data.exit_code is %v, want 0", data["exit_code"])
	}
	if output, _ := data["output"].(string); !strings.Contains(output, "12000") {
		t.Error("the output does not reach the end of the command, so it is not the tail that survived")
	}
	if !disclosesTruncation(e.Warnings) {
		t.Errorf("warnings are %v, want one saying the output begins partway through", e.Warnings)
	}

	human := run(t, append(flags, "run", target, linesPastTheRunWindow)...)
	if human.code != 0 {
		t.Fatalf("the human path exited %d: %s", human.code, human.stderr)
	}
	if !strings.Contains(human.stderr, "output begins partway through") {
		t.Errorf("stderr %q carries no truncation disclosure", human.stderr)
	}
}

// The throwaway path builds its warnings separately, so it can drop the run's
// own while still reporting a cleanup failure.
func TestATruncatedThrowawayRunDisclosesItself(t *testing.T) {
	flags := isolation(t)

	got := run(t, append(flags, "run", linesPastTheRunWindow, "--json")...)
	if got.code != 0 {
		t.Fatalf("the throwaway run exited %d: %s", got.code, got.stderr)
	}
	e := got.envelope(t)
	if !e.OK {
		t.Fatalf("the throwaway run reported failure: %+v", e.Error)
	}
	if !disclosesTruncation(e.Warnings) {
		t.Errorf("warnings are %v, want one saying the output begins partway through", e.Warnings)
	}
}

// §6.2 on the detached path. A poll never grows its window, so before the
// relaxation this run answered pending on every poll, forever, while the exit
// code sat on the screen it had just read.
func TestATruncatedDetachedRunPollsAsCompleted(t *testing.T) {
	flags := isolation(t)
	target := name()
	if got := run(t, append(flags, "start", target)...); got.code != 0 {
		t.Fatalf("start: exit %d, stderr %q", got.code, got.stderr)
	}
	t.Cleanup(func() { run(t, append(flags, "stop", target, "--force")...) })

	if got := run(t, append(flags, "run", target, `printf 'warm-%d\n' 1`)...); got.code != 0 {
		t.Fatalf("warming run: exit %d, stderr %q", got.code, got.stderr)
	}

	started := run(t, append(flags, "run", target, linesPastTheRunWindow, "--detach", "--json")...)
	if started.code != 0 {
		t.Fatalf("the detached run exited %d: %s", started.code, started.stderr)
	}
	var start struct {
		CommandID string `json:"command_id"`
	}
	if err := json.Unmarshal(mustData(t, started.envelope(t)), &start); err != nil {
		t.Fatalf("decoding the start payload: %v", err)
	}

	// Polled until the command finishes, not once: the command takes time to
	// produce its output, and pending is the right answer while it does.
	deadline := time.Now().Add(30 * time.Second)
	var last cli.Envelope
	for {
		polled := run(t, append(flags, "poll", target, start.CommandID, "--json")...)
		if polled.code != 0 {
			t.Fatalf("poll exited %d: %s", polled.code, polled.stderr)
		}
		last = polled.envelope(t)
		data, _ := last.Data.(map[string]any)
		if state, _ := data["status"].(string); state == "completed" {
			if code, ok := data["exit_code"].(float64); !ok || int(code) != 0 {
				t.Errorf("data.exit_code is %v, want 0", data["exit_code"])
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the poll never reported completed: %s", polled.stdout)
		}
		time.Sleep(200 * time.Millisecond)
	}
	if !disclosesTruncation(last.Warnings) {
		t.Errorf("warnings are %v, want one saying the output begins partway through", last.Warnings)
	}
}

// §13.2 `servers` lists the level above sessions; `--server` selects one by
// name and is exclusive with every address flag. Scanned against a private
// tmux socket directory, never the operator's.
func TestServersListsAndSelectsByName(t *testing.T) {
	requireTmuxInstalled(t)
	base, err := os.MkdirTemp(os.TempDir(), "olyc")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(base) })
	t.Setenv("TMUX_TMPDIR", base)

	got := run(t, "--backend", "tmux", "servers", "--json")
	if got.code != 0 {
		t.Fatalf("exit %d: %s%s", got.code, got.stdout, got.stderr)
	}
	e := got.envelope(t)
	if !e.OK || e.Backend != backend.Tmux {
		t.Errorf("envelope %+v does not report a tmux success", e)
	}
	if rows, ok := e.Data.([]any); !ok || len(rows) != 0 {
		t.Errorf("data is %v, want an empty array — never null", e.Data)
	}

	human := run(t, "--backend", "tmux", "servers")
	if human.code != 0 || !strings.Contains(human.stdout, "no servers on the tmux backend") {
		t.Errorf("human output %q (exit %d) does not name the backend", human.stdout, human.code)
	}

	conflict := run(t, "--backend", "tmux", "--server", "work", "--socket", "x", "ls", "--json")
	if conflict.code != 2 {
		t.Errorf("--server with --socket exits %d, want 2", conflict.code)
	}
	if e := conflict.envelope(t); e.Error == nil || e.Error.Code != backend.CodeUsage {
		t.Errorf("--server with --socket is %v, want USAGE", e.Error)
	}

	unknown := run(t, "--backend", "tmux", "servers", "stop", "nonesuch", "--json")
	if unknown.code != 3 {
		t.Errorf("stopping an unknown server exits %d, want 3", unknown.code)
	}
	if e := unknown.envelope(t); e.Error == nil || e.Error.Code != backend.CodeSessionNotFound {
		t.Errorf("stopping an unknown server is %v, want SESSION_NOT_FOUND", e.Error)
	}
}

// §13.2 A running server is listed as running and `servers stop` takes it
// down, reporting killed; a second stop reports gone. The server is private:
// its socket is a path inside a directory this test owns, which is also the
// directory the listing scans.
func TestServersStopKillsARunningServer(t *testing.T) {
	skipUnlessFull(t)
	requireTmuxInstalled(t)
	base, err := os.MkdirTemp(os.TempDir(), "olyc")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(base) })
	t.Setenv("TMUX_TMPDIR", base)
	dir := filepath.Join(base, fmt.Sprintf("tmux-%d", os.Getuid()))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	socket := filepath.Join(dir, "live")
	t.Cleanup(func() { _ = exec.Command("tmux", "-S", socket, "kill-server").Run() })
	if out, err := exec.Command("tmux", "-S", socket, "new-session", "-d", "-x", "80", "-y", "24").CombinedOutput(); err != nil {
		t.Fatalf("starting a private server: %v\n%s", err, out)
	}

	// Selected by name, the server's sessions are what `ls` shows.
	listed := run(t, "--backend", "tmux", "--server", "live", "ls", "--json")
	if listed.code != 0 {
		t.Fatalf("ls --server exits %d: %s%s", listed.code, listed.stdout, listed.stderr)
	}
	if rows, _ := listed.envelope(t).Data.([]any); len(rows) != 1 {
		t.Errorf("ls --server live lists %v, want the one session on it", rows)
	}

	got := run(t, "--backend", "tmux", "servers")
	if got.code != 0 || !strings.Contains(got.stdout, "live") {
		t.Fatalf("servers exit %d, stdout %q does not list the running server", got.code, got.stdout)
	}

	stopped := run(t, "--backend", "tmux", "servers", "stop", "live", "--json")
	if stopped.code != 0 {
		t.Fatalf("servers stop exits %d: %s%s", stopped.code, stopped.stdout, stopped.stderr)
	}
	data, _ := stopped.envelope(t).Data.(map[string]any)
	if data["outcome"] != "killed" || data["name"] != "live" {
		t.Errorf("servers stop reports %v, want killed live", data)
	}
	if err := exec.Command("tmux", "-S", socket, "list-sessions").Run(); err == nil {
		t.Error("the server still answers after servers stop")
	}

	again := run(t, "--backend", "tmux", "servers", "stop", "live")
	if again.code != 0 || !strings.Contains(again.stdout, "gone live") {
		t.Errorf("a second stop exits %d with %q, want gone", again.code, again.stdout)
	}
}

// §3.7 `agents` answers on a backend with no agent detection of its own: a
// pane whose process subtree holds a known agent is listed, found by command
// and with an unknown status; a pane running a shell is not; and with none
// the answer is an empty array, never null, never unsupported. The "agents"
// are this test binary copied under the names claude and codex (TestMain) —
// real binaries whose names the process table reports, where a copied system
// sleep is killed by macOS and a symlink or script reports the program
// underneath. claude runs as the pane's own process; codex runs as the CHILD
// of a shell whose foreground command tmux reports as the shell, so only the
// walk down from `#{pane_pid}` can find it.
func TestAgentsListsPanesRunningAKnownAgentCommand(t *testing.T) {
	flags := isolation(t)
	dir, err := os.MkdirTemp(os.TempDir(), "olya")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })

	none := run(t, append(flags, "agents", "--json")...)
	if none.code != 0 {
		t.Fatalf("exit %d: %s%s", none.code, none.stdout, none.stderr)
	}
	if rows, ok := none.envelope(t).Data.([]any); !ok || len(rows) != 0 {
		t.Errorf("data is %v, want an empty array — never null", none.envelope(t).Data)
	}
	human := run(t, append(flags, "agents")...)
	if human.code != 0 || !strings.Contains(human.stdout, "no agents on the tmux backend") {
		t.Errorf("human output %q (exit %d) does not name the backend", human.stdout, human.code)
	}

	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	binary, err := os.ReadFile(self)
	if err != nil {
		t.Fatal(err)
	}
	fakes := map[string]string{}
	for agent := range impersonatedAgents {
		fakes[agent] = filepath.Join(dir, agent)
		if err := os.WriteFile(fakes[agent], binary, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	shell, agent, child := name(), name(), name()
	for _, n := range []string{shell, agent, child} {
		t.Cleanup(func() { run(t, append(flags, "stop", n, "--force")...) })
	}
	if got := run(t, append(flags, "start", shell)...); got.code != 0 {
		t.Fatalf("starting a shell session: exit %d: %s%s", got.code, got.stdout, got.stderr)
	}
	if got := run(t, append(flags, "start", agent, "--dir", dir, fakes["claude"])...); got.code != 0 {
		t.Fatalf("starting the agent session: exit %d: %s%s", got.code, got.stdout, got.stderr)
	}
	// A second command after the agent keeps the shell from exec'ing it, so
	// codex is the shell's child and the shell stays the pane's process.
	if got := run(t, append(flags, "start", child, "--dir", dir, "--", "sh", "-c", fakes["codex"]+"; true")...); got.code != 0 {
		t.Fatalf("starting the shell-wrapped agent session: exit %d: %s%s", got.code, got.stdout, got.stderr)
	}

	// tmux hands the argv to `sh -c`, which reads as the pane's command until
	// the shell has exec'd the program — a few milliseconds, but a real
	// window, so the listing is polled rather than read once.
	var listed result
	var rows []any
	for deadline := time.Now().Add(5 * time.Second); ; {
		listed = run(t, append(flags, "agents", "--json")...)
		if listed.code != 0 {
			t.Fatalf("exit %d: %s%s", listed.code, listed.stdout, listed.stderr)
		}
		rows, _ = listed.envelope(t).Data.([]any)
		if len(rows) == 2 || time.Now().After(deadline) {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if len(rows) != 2 {
		t.Fatalf("listed %d agents, want the two impersonated: %s", len(rows), listed.stdout)
	}
	bySession := map[string]map[string]any{}
	for _, r := range rows {
		row, _ := r.(map[string]any)
		bySession[row["session_name"].(string)] = row
	}
	for session, want := range map[string]string{agent: "claude", child: "codex"} {
		row := bySession[session]
		for field, value := range map[string]any{
			"agent": want, "status": "unknown", "detected_by": "command",
		} {
			if row[field] != value {
				t.Errorf("%s: %s is %v, want %v (row %v)", session, field, row[field], value, row)
			}
		}
		for _, absent := range []string{"title", "usage"} {
			if _, present := row[absent]; present {
				t.Errorf("%s is present on a command-detected row: %v", absent, row)
			}
		}
	}

	// The proof that the tree was walked: the wrapped agent's pane reports
	// the shell as its foreground command, not codex, so the command match
	// alone could not have listed it.
	panes := run(t, append(flags, "panes", child, "--json")...)
	if panes.code != 0 {
		t.Fatalf("exit %d: %s%s", panes.code, panes.stdout, panes.stderr)
	}
	paneRows, _ := panes.envelope(t).Data.([]any)
	if len(paneRows) != 1 {
		t.Fatalf("listed %d panes for %s, want one: %s", len(paneRows), child, panes.stdout)
	}
	pane, _ := paneRows[0].(map[string]any)
	if pid, _ := pane["pid"].(float64); pid <= 0 {
		t.Errorf("the pane row carries pid %v, want the pane's process id", pane["pid"])
	}
	if pane["current_command"] == "codex" {
		t.Errorf("the pane's foreground command is codex, so this case did not exercise the walk: %v", pane)
	}

	table := run(t, append(flags, "agents")...)
	if !strings.HasPrefix(table.stdout, "PANE") || !strings.Contains(table.stdout, "claude") {
		t.Errorf("human table %q does not lead with the columns and name the agent", table.stdout)
	}
}

// §3.7 On a backend with no agent detection of its own, a command-detected
// row's status is read off the pane's screen and says so: a pane running a
// binary named claude that has painted a claude-shaped turn — the composer
// box with "esc to interrupt" below it — lists as working, with
// `status_source: "screen"`, and the capability says rows carry a status.
// The impersonated binary paints the fixture itself and sleeps, so no real
// agent is started and no text crosses a shell.
func TestAgentsReadStatusOffTheScreen(t *testing.T) {
	flags := isolation(t)
	dir, err := os.MkdirTemp(os.TempDir(), "olys")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	binary, err := os.ReadFile(self)
	if err != nil {
		t.Fatal(err)
	}
	fake := filepath.Join(dir, "claude")
	if err := os.WriteFile(fake, binary, 0o755); err != nil {
		t.Fatal(err)
	}
	session := name()
	t.Cleanup(func() { run(t, append(flags, "stop", session, "--force")...) })
	if got := run(t, append(flags, "start", session, "--dir", dir, "--", fake, "--paint", "claude-working")...); got.code != 0 {
		t.Fatalf("starting the painted agent session: exit %d: %s%s", got.code, got.stdout, got.stderr)
	}

	// The paint lands a few milliseconds after the pane exists, so the
	// listing is polled until the status is read or the deadline passes.
	var listed result
	var row map[string]any
	for deadline := time.Now().Add(5 * time.Second); ; {
		listed = run(t, append(flags, "agents", "--json")...)
		if listed.code != 0 {
			t.Fatalf("exit %d: %s%s", listed.code, listed.stdout, listed.stderr)
		}
		rows, _ := listed.envelope(t).Data.([]any)
		if len(rows) == 1 {
			row, _ = rows[0].(map[string]any)
		}
		if row != nil && row["status"] == "working" || time.Now().After(deadline) {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if row == nil {
		t.Fatalf("no agent listed: %s", listed.stdout)
	}
	for field, value := range map[string]any{
		"agent": "claude", "status": "working", "status_source": "screen", "detected_by": "command",
	} {
		if row[field] != value {
			t.Errorf("%s is %v, want %v (row %v)", field, row[field], value, row)
		}
	}

	caps := run(t, append(flags, "capabilities", "--json")...)
	if caps.code != 0 {
		t.Fatalf("exit %d: %s%s", caps.code, caps.stdout, caps.stderr)
	}
	if got, _ := caps.envelope(t).Data.(map[string]any); got["agent_status"] != true {
		t.Errorf("agent_status is %v on tmux, want true: rows carry a screen-derived status", got["agent_status"])
	}

	table := run(t, append(flags, "agents")...)
	if !strings.Contains(table.stdout, "working") {
		t.Errorf("human table %q does not show the status", table.stdout)
	}
}

// §3.7 `kinds` is the agent vocabulary read back off the detection tables.
// The CLI door translates and does not decide, so its `--json` payload must
// be exactly what the Go door returns — and it must answer without resolving
// a backend, so an operator with no multiplexer installed can still ask what
// Olympus knows.
func TestKindsMatchesTheGoDoorAndNeedsNoBackend(t *testing.T) {
	// A backend that cannot exist: if the verb resolved one, this would fail.
	t.Setenv("OLYMPUS_BACKEND", "nonesuch")

	structured := run(t, "kinds", "--json")
	if structured.code != 0 {
		t.Fatalf("kinds --json exits %d: %s%s", structured.code, structured.stdout, structured.stderr)
	}
	encoded, err := json.Marshal(structured.envelope(t).Data)
	if err != nil {
		t.Fatalf("re-encoding the payload: %v", err)
	}
	var got []backend.AgentKind
	if err := json.Unmarshal(encoded, &got); err != nil {
		t.Fatalf("the payload is not an array of agent kinds: %v\n%s", err, structured.stdout)
	}
	if want := olympus.Kinds(); !reflect.DeepEqual(got, want) {
		t.Errorf("the CLI reports\n\t%v\nthe Go door\n\t%v", got, want)
	}
	if len(got) == 0 {
		t.Fatal("the vocabulary is empty")
	}

	human := run(t, "kinds")
	if human.code != 0 {
		t.Fatalf("kinds exits %d: %s%s", human.code, human.stdout, human.stderr)
	}
	if !strings.Contains(human.stdout, "NAME") || !strings.Contains(human.stdout, "EXECUTABLES") ||
		!strings.Contains(human.stdout, "PACKAGES") {
		t.Errorf("the table has no header:\n%s", human.stdout)
	}
	if !strings.Contains(human.stdout, "claude-code") {
		t.Errorf("the table does not name claude's npm spelling:\n%s", human.stdout)
	}
}
