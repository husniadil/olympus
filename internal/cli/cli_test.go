package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/husniadil/olympus/internal/cli"
)

var counter atomic.Int64

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
		"an unknown flag":       {"ls", "--nonesuch", "--json"},
		"a bad flag value":      {"screen", "x", "--history", "banana", "--json"},
		"too few positionals":   {"screen", "--json"},
		"too many positionals":  {"ls", "extra", "--json"},
		"an unknown subcommand": {"nonesuch", "--json"},
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
		{"key", "oly-never-existed", "enter"},
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
	t.Setenv("ZMX_SESSION", "")
	t.Setenv("TMUX", "")
	t.Setenv("TMUX_PANE", "")

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
