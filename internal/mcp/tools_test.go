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

	captured := w.callTool(t, "capture", map[string]any{"targets": []string{first, second}})
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
		captured := w.callTool(t, "capture", map[string]any{"targets": []string{name}})
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
