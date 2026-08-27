package mcp_test

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// These drive the tools against a real backend, because a tool that registers
// and returns a schema-valid shape can still do nothing at all.

var counter atomic.Int64

// isolate points the server at a private tmux socket. The MCP door reads its
// configuration from the process environment, since a stateless request carries
// none — which is exactly what makes this possible.
func isolate(t *testing.T) {
	t.Helper()
	skipUnlessFull(t)
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is not installed")
	}
	socket := fmt.Sprintf("olym-%d-%d", os.Getpid(), counter.Add(1))
	t.Setenv("OLYMPUS_BACKEND", "tmux")
	t.Setenv("OLYMPUS_SOCKET", socket)
	t.Cleanup(func() {
		_ = exec.Command("tmux", "-L", socket, "kill-server").Run()
		dir := os.Getenv("TMUX_TMPDIR")
		if dir == "" {
			dir = "/tmp"
		}
		_ = os.Remove(filepath.Join(dir, fmt.Sprintf("tmux-%d", os.Getuid()), socket))
	})
}

// callTool invokes a tool and returns its structured data, failing on a tool
// error.
func (w *wire) callTool(t *testing.T, name string, args map[string]any) map[string]any {
	t.Helper()
	response := w.call("tools/call", map[string]any{
		"name":      name,
		"arguments": args,
		"_meta":     modernMeta(modernVersion),
	})
	result := resultOf(t, response)
	if isError, _ := result["isError"].(bool); isError {
		encoded, _ := json.Marshal(result["content"])
		t.Fatalf("%s returned a tool error: %s", name, encoded)
	}
	structured, ok := result["structuredContent"].(map[string]any)
	if !ok {
		t.Fatalf("%s returned no structured content: %v", name, result)
	}
	return structured
}

// callToolExpectingError invokes a tool that should fail, and returns the text.
func (w *wire) callToolExpectingError(t *testing.T, name string, args map[string]any) string {
	t.Helper()
	response := w.call("tools/call", map[string]any{
		"name":      name,
		"arguments": args,
		"_meta":     modernMeta(modernVersion),
	})
	result := resultOf(t, response)
	if isError, _ := result["isError"].(bool); !isError {
		t.Fatalf("%s succeeded, but should have failed: %v", name, result)
	}
	encoded, _ := json.Marshal(result["content"])
	return string(encoded)
}

func sessionName() string { return fmt.Sprintf("oly-m%d", counter.Add(1)) }

func TestSessionToolsWorkEndToEnd(t *testing.T) {
	isolate(t)
	w := newWire(t)
	name := sessionName()

	started := w.callTool(t, "start_session", map[string]any{"name": name})
	if started["backend"] != "tmux" {
		t.Errorf("the result names backend %v, want tmux — the resolved backend must be disclosed", started["backend"])
	}

	listed := w.callTool(t, "list_sessions", map[string]any{})
	sessions, _ := listed["data"].([]any)
	if len(sessions) != 1 {
		t.Errorf("list_sessions returned %d sessions, want 1", len(sessions))
	}

	// new_session means "must not already exist", which is the whole reason it
	// exists alongside start_session.
	if text := w.callToolExpectingError(t, "new_session", map[string]any{"name": name}); !strings.Contains(text, "CONFLICT") {
		t.Errorf("new_session on an existing name did not report a conflict: %s", text)
	}

	// start_session reuses it instead.
	reused := w.callTool(t, "start_session", map[string]any{"name": name})
	data, _ := reused["data"].(map[string]any)
	if data["outcome"] != "reused" {
		t.Errorf("start_session reported outcome %v for an existing session, want reused", data["outcome"])
	}

	w.callTool(t, "stop_session", map[string]any{"target": name, "force": true})
}

func TestCaptureToolReadsSeveralTargets(t *testing.T) {
	isolate(t)
	w := newWire(t)
	first, second := sessionName(), sessionName()
	w.callTool(t, "start_session", map[string]any{"name": first})
	w.callTool(t, "start_session", map[string]any{"name": second})

	captured := w.callTool(t, "screen", map[string]any{"targets": []string{first, second}})
	data, _ := captured["data"].(map[string]any)
	screens, _ := data["screens"].(map[string]any)
	if len(screens) != 2 {
		t.Errorf("capture returned %d screens, want 2", len(screens))
	}
	if _, ok := data["meta"].(map[string]any); !ok {
		t.Errorf("capture returned no metadata, so a caller cannot tell an empty screen from a skipped one")
	}
}

func TestPaneAndCapabilityToolsAnswerDirectly(t *testing.T) {
	isolate(t)
	w := newWire(t)
	name := sessionName()
	w.callTool(t, "start_session", map[string]any{"name": name})

	// Every pane on the backend, which is a different question from one
	// session's detail.
	all := w.callTool(t, "list_panes", map[string]any{})
	if panes, _ := all["data"].([]any); len(panes) == 0 {
		t.Error("list_panes with no target returned nothing")
	}

	caps := w.callTool(t, "capabilities", map[string]any{})
	data, _ := caps["data"].(map[string]any)
	if views, ok := data["views"].(bool); !ok || !views {
		t.Errorf("capabilities does not report view support for this backend: %v", data)
	}
}

// §6.10 through the MCP door: a run with no session of its own to point at.
func TestThrowawayRunTool(t *testing.T) {
	isolate(t)
	w := newWire(t)

	got := w.callTool(t, "run_command", map[string]any{
		"command":   `printf 'mcp-%d\n' 4`,
		"throwaway": true,
	})
	data, _ := got["data"].(map[string]any)
	output, _ := data["output"].(string)
	if !strings.Contains(output, "mcp-4") {
		t.Errorf("output %q does not contain the command's output", output)
	}

	// Nothing left behind.
	listed := w.callTool(t, "list_sessions", map[string]any{})
	if sessions, _ := listed["data"].([]any); len(sessions) != 0 {
		t.Errorf("the throwaway run left %d sessions behind", len(sessions))
	}

	// It cannot be detached: there would be nothing to poll.
	if text := w.callToolExpectingError(t, "start_run", map[string]any{
		"command": "sleep 1", "throwaway": true,
	}); !strings.Contains(text, "USAGE") {
		t.Errorf("a detached throwaway run was not rejected as a usage error: %s", text)
	}
}

// timeout_seconds must reach a throwaway run too: the throwaway branch creates
// its own session, and a branch that forgot to carry the run's options would
// bound every command by the default instead of by what was asked for.
func TestThrowawayRunToolHonorsTimeoutSeconds(t *testing.T) {
	isolate(t)
	w := newWire(t)

	started := time.Now()
	text := w.callToolExpectingError(t, "run_command", map[string]any{
		"command":         "sleep 120",
		"throwaway":       true,
		"timeout_seconds": 2,
	})
	elapsed := time.Since(started)

	if !strings.Contains(text, "TIMEOUT") {
		t.Errorf("a throwaway run past its budget did not report a timeout: %s", text)
	}
	if elapsed > 30*time.Second {
		t.Errorf("the run took %s, so timeout_seconds was not the budget it used", elapsed)
	}
}

// Verification and submission are independent, and both are reachable.
func TestSendToolCanVerifyWithoutSubmitting(t *testing.T) {
	isolate(t)
	w := newWire(t)
	name := sessionName()
	w.callTool(t, "start_session", map[string]any{"name": name})

	// Warm the shell first, or the text lands before it is reading (§16).
	w.callTool(t, "run_command", map[string]any{"target": name, "command": `printf 'warm-%d\n' 1`})

	w.callTool(t, "send_text", map[string]any{
		"target": name, "text": `printf 'unsubmitted-%d\n' 2`, "no_submit": true,
	})

	// Polled, not captured once. send_text has already confirmed the text is on
	// screen, but this capture is a SEPARATE request against the terminal and
	// can outrun the pane's rendering — the same race that makes "assert
	// immediately after writing" unreliable everywhere else in this codebase.
	var screen string
	deadline := time.Now().Add(10 * time.Second)
	for {
		captured := w.callTool(t, "screen", map[string]any{"targets": []string{name}})
		data, _ := captured["data"].(map[string]any)
		screens, _ := data["screens"].(map[string]any)
		screen, _ = screens[name].(string)
		if strings.Contains(screen, "unsubmitted-%d") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the text was never placed in the input line: %q", screen)
		}
		time.Sleep(100 * time.Millisecond)
	}

	// Placed but never executed: the EXPANSION is what would prove execution,
	// and it must be absent.
	if strings.Contains(screen, "unsubmitted-2") {
		t.Errorf("the text was submitted despite no_submit: %q", screen)
	}

	// Atomic and no-submit are contradictory and must be refused rather than
	// silently resolved one way.
	if text := w.callToolExpectingError(t, "send_text", map[string]any{
		"target": name, "text": "x", "atomic": true, "no_submit": true,
	}); !strings.Contains(text, "USAGE") {
		t.Errorf("combining atomic with no_submit was not rejected: %s", text)
	}
}

// The server answers where IT is running, which is the same question the CLI
// answers — an agent whose server sits inside a session can hand that address
// to another agent.
func TestSelfTool(t *testing.T) {
	w := newWire(t)

	got := w.callTool(t, "self", map[string]any{})
	data, _ := got["data"].(map[string]any)
	if _, ok := data["inside"]; !ok {
		t.Fatalf("self returned no answer at all: %v", data)
	}
	// This test process is not inside an Olympus session, and the tool must
	// say so plainly rather than failing or inventing a session.
	if inside, _ := data["inside"].(bool); inside {
		nested, _ := data["nested"].([]any)
		if data["session"] == "" && len(nested) == 0 {
			t.Errorf("self claims to be inside a session it cannot name: %v", data)
		}
	}
}

// The status vocabulary must reach the MCP door too, or an agent driving
// Olympus through it has no way to answer "are you still working?" except by
// scraping a screen that cannot tell it apart from a prompt.
func TestSessionStatusTool(t *testing.T) {
	isolate(t)
	w := newWire(t)
	name := sessionName()
	w.callTool(t, "start_session", map[string]any{"name": name})

	set := w.callTool(t, "session_status", map[string]any{"target": name, "set": "waiting on review"})
	data, _ := set["data"].(map[string]any)
	if data["status"] != "waiting on review" {
		t.Fatalf("setting a status returned %v", data)
	}

	got := w.callTool(t, "session_status", map[string]any{"target": name})
	data, _ = got["data"].(map[string]any)
	if data["status"] != "waiting on review" {
		t.Errorf("reading the status back returned %v", data)
	}
}

// isolateMeja points the door at a meja server nobody else uses.
//
// A socket PATH, never a profile: meja keeps session recovery files beside the
// socket, so a named profile would leave persisted sessions in the operator's
// own store (§2.9). Checked by RUNNING meja, because a dangling shim on PATH
// satisfies a lookup and fails every call.
func isolateMeja(t *testing.T) {
	t.Helper()
	if err := exec.Command("meja", "version").Run(); err != nil {
		t.Skip("meja is not installed or not runnable")
	}
	dir, err := os.MkdirTemp(os.TempDir(), "olym")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	socket := filepath.Join(dir, "m.sock")
	t.Setenv("OLYMPUS_BACKEND", "meja")
	t.Setenv("OLYMPUS_SOCKET_PATH", socket)
	t.Cleanup(func() {
		_ = exec.Command("meja", "-S", socket, "kill-server").Run()
		_ = os.RemoveAll(dir)
	})
}

// Every tool must reach meja, and every tool meja cannot serve must say so in
// the vocabulary a caller branches on.
//
// UNSUPPORTED and UNEXPECTED are the two answers that must never be confused:
// one means "this backend will never do that, choose another approach", the
// other means "something went wrong, retrying might help". A backend wired in
// carelessly reports the second for the first, and a caller then retries
// forever against a capability that does not exist.
func TestEveryMCPToolIsServedOrRefusedOnMeja(t *testing.T) {
	isolateMeja(t)
	w := newWire(t)
	name := sessionName()

	w.callTool(t, "start_session", map[string]any{"name": name})

	served := []struct {
		tool string
		args map[string]any
	}{
		{"list_sessions", map[string]any{}},
		{"list_panes", map[string]any{}},
		{"session_info", map[string]any{"target": name}},
		{"capabilities", map[string]any{}},
		{"doctor", map[string]any{}},
		{"version", map[string]any{}},
		{"self", map[string]any{}},
		{"type_text", map[string]any{"target": name, "text": "echo served"}},
		{"press_keys", map[string]any{"target": name, "keys": []any{"enter"}}},
		{"screen", map[string]any{"targets": []any{name}}},
		{"paste_text", map[string]any{"target": name, "text": "one\ntwo"}},
		{"send_text", map[string]any{"target": name, "text": "echo confirmed"}},
		{"run_command", map[string]any{"target": name, "command": "echo ran"}},
		{"wait_for", map[string]any{"target": name, "pattern": "ran", "seconds": 15}},
		{"exit_status", map[string]any{"target": name, "marker": "OLYDONE"}},
	}
	for _, c := range served {
		got := w.callTool(t, c.tool, c.args)
		if got["ok"] == false {
			t.Errorf("%s is not served on meja: %v", c.tool, got)
		}
	}

	// meja has no read-only client, no key table and no option store, so these
	// have no mechanism rather than a broken one.
	refused := []struct {
		tool string
		args map[string]any
	}{
		{"create_view", map[string]any{"base": name}},
		{"list_views", map[string]any{"base": name}},
		{"server_env", map[string]any{"key": "PATH"}},
		{"session_status", map[string]any{"target": name}},
		{"scroll_view", map[string]any{"view": name, "lines": 1}},
	}
	// new_session means "must not already exist", so it needs its own name, and
	// its refusal on a taken one is part of what it is.
	fresh := name + "-fresh"
	if got := w.callTool(t, "new_session", map[string]any{"name": fresh}); got["ok"] == false {
		t.Errorf("new_session is not served on meja: %v", got)
	}
	if text := w.callToolExpectingError(t, "new_session", map[string]any{"name": fresh}); !strings.Contains(text, "CONFLICT") {
		t.Errorf("new_session on a taken meja name reports %q, want CONFLICT", text)
	}
	w.callTool(t, "stop_session", map[string]any{"target": fresh})

	// A detached run and its poll are one pair: the id exists only because the
	// run produced it, so testing either alone tests half a mechanism.
	started := w.callTool(t, "start_run", map[string]any{"target": name, "command": "echo polled"})
	startData, _ := started["data"].(map[string]any)
	id, _ := startData["command_id"].(string)
	if id == "" {
		t.Fatalf("start_run on meja returned no id to poll: %v", started)
	}
	if got := w.callTool(t, "poll_run", map[string]any{"target": name, "id": id}); got["ok"] == false {
		t.Errorf("poll_run is not served on meja: %v", got)
	}

	for _, c := range refused {
		text := w.callToolExpectingError(t, c.tool, c.args)
		if !strings.Contains(text, "UNSUPPORTED") {
			t.Errorf("%s on meja reports %q, want UNSUPPORTED — a caller must be able to tell "+
				"'this backend never will' from 'try again'", c.tool, text)
		}
	}

	w.callTool(t, "stop_session", map[string]any{"target": name})
}

// An operator's own ZMX_DIR must not break a server running on another backend.
//
// It is zmx's OWN variable, exported for their own use of zmx, and api §4 says
// the zmx binary reads it whether or not Olympus passes it along. So passing it
// was redundant, and once an addressing option that does not apply became a
// usage error, redundant turned into fatal: every tool call would fail for an
// operator who happens to have it set.
func TestAForeignZmxDirDoesNotBreakAnotherBackend(t *testing.T) {
	isolate(t)
	t.Setenv("ZMX_DIR", "/tmp/some-operator-daemon")

	w := newWire(t)
	got := w.callTool(t, "list_sessions", map[string]any{})
	if got["ok"] == false {
		t.Fatalf("an exported ZMX_DIR broke a tmux server: %v", got)
	}
	if got["backend"] != "tmux" {
		t.Errorf("resolved %v, want tmux", got["backend"])
	}
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

// The MCP half of the same pin. Both doors mirror one type now, and this is what
// notices if either stops — the only cases reading this field were meja-specific
// and skip wherever meja is absent.
func TestStartRunReturnsAPollableCommandID(t *testing.T) {
	isolate(t)
	w := newWire(t)
	name := sessionName()
	w.callTool(t, "start_session", map[string]any{"name": name})

	started := w.callTool(t, "start_run", map[string]any{"target": name, "command": "echo detached-ok"})
	data, _ := started["data"].(map[string]any)
	id, _ := data["command_id"].(string)
	if id == "" {
		t.Fatalf("start_run returned no command_id: %v", started)
	}

	// Present is not enough: the other tool has to accept it. Note the
	// asymmetry — start_run RETURNS `command_id` and poll_run TAKES `id`. Both
	// names are shipped and semver-bound, so neither can be renamed now; this
	// case exists partly so the pairing is written down somewhere executable.
	polled := w.callTool(t, "poll_run", map[string]any{"target": name, "id": id})
	polledData, _ := polled["data"].(map[string]any)
	if polledData["status"] == nil {
		t.Errorf("polling with the returned id reported no status: %v", polled)
	}
}

// start_run RETURNS `command_id` and poll_run has always TAKEN `id`. Both are
// shipped and semver-bound, so neither can be renamed — but a new name can be
// added (CLAUDE.md #3), and the obvious thing for a caller to try is the name
// they were just handed.
//
// Getting it wrong costs a round trip and a schema error that reads like the
// tool is broken, which is a poor way to learn that two names mean one thing.
func TestPollRunAlsoAcceptsTheNameStartRunHandedBack(t *testing.T) {
	isolate(t)
	w := newWire(t)
	name := sessionName()
	w.callTool(t, "start_session", map[string]any{"name": name})

	started := w.callTool(t, "start_run", map[string]any{"target": name, "command": "echo aliased"})
	data, _ := started["data"].(map[string]any)
	id, _ := data["command_id"].(string)
	if id == "" {
		t.Fatalf("start_run returned no command_id: %v", started)
	}

	polled := w.callTool(t, "poll_run", map[string]any{"target": name, "command_id": id})
	polledData, _ := polled["data"].(map[string]any)
	if polledData["status"] == nil {
		t.Errorf("poll_run rejected the name start_run handed back: %v", polled)
	}
}

// The original name keeps working, unchanged. An alias that displaced it would
// be a rename wearing a friendlier word.
func TestPollRunStillAcceptsItsOriginalName(t *testing.T) {
	isolate(t)
	w := newWire(t)
	name := sessionName()
	w.callTool(t, "start_session", map[string]any{"name": name})

	started := w.callTool(t, "start_run", map[string]any{"target": name, "command": "echo original"})
	data, _ := started["data"].(map[string]any)
	id, _ := data["command_id"].(string)

	polled := w.callTool(t, "poll_run", map[string]any{"target": name, "id": id})
	polledData, _ := polled["data"].(map[string]any)
	if polledData["status"] == nil {
		t.Errorf("poll_run rejected its own original parameter name: %v", polled)
	}
}

// Accepting either name means the schema can no longer REQUIRE one, so the
// requirement has to move rather than disappear. A poll with no id at all would
// otherwise scan the scrollback for a marker that cannot exist and report the
// run as died — a wrong answer dressed as a real one.
func TestPollRunWithNoIDAtAllIsAUsageError(t *testing.T) {
	isolate(t)
	w := newWire(t)
	name := sessionName()
	w.callTool(t, "start_session", map[string]any{"name": name})

	text := w.callToolExpectingError(t, "poll_run", map[string]any{"target": name})
	if !strings.Contains(text, "USAGE") {
		t.Errorf("polling with no id is %q, want a usage error", text)
	}
}
