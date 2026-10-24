package mcp

import (
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/husniadil/olympus"
	"github.com/husniadil/olympus/backend"
)

// api §2's `error.typed` reaches an MCP client as a line of its own, and only
// on a refusal whose text was typed.
func TestATypedRefusalSaysSoInTheToolError(t *testing.T) {
	typed := backend.Errorf(backend.CodeAgentBlocked, "typed then blocked")
	typed.Typed = true
	lines := func(r *sdk.CallToolResult) []string {
		var out []string
		for _, c := range r.Content {
			out = append(out, c.(*sdk.TextContent).Text)
		}
		return out
	}
	if got := lines(toolError(typed)); len(got) != 2 || got[1] != "typed: true" {
		t.Errorf("typed refusal content = %q, want the code line and \"typed: true\"", got)
	}
	if got := lines(toolError(backend.Errorf(backend.CodeAgentBlocked, "blocked"))); len(got) != 1 {
		t.Errorf("untyped refusal content = %q, want only the code line", got)
	}
	// api §2's `error.reason` the same way, after `typed` where both are set.
	hidden := backend.Errorf(backend.CodeAgentBlocked, "box gone after typing")
	hidden.Typed = true
	hidden.Reason = backend.ReasonNoInputBox
	if got := lines(toolError(hidden)); len(got) != 3 || got[1] != "typed: true" || got[2] != "reason: no_input_box" {
		t.Errorf("refusal with a reason content = %q, want the code line, \"typed: true\" and \"reason: no_input_box\"", got)
	}
}

// behavior §6.10: a throwaway run that fails and then fails to clean up reports
// both. The structured content carries the cleanup failure beside the error.
func TestAToolFailureKeepsTheWarningsThatCameWithIt(t *testing.T) {
	cleanup := []olympus.Warning{{Code: olympus.WarningDegraded, Message: "the throwaway session x could not be cleaned up: gone"}}
	result, structured := failure[olympus.Result](backend.Tmux, withWarnings(backend.Errorf(backend.CodeTimeout, "timed out"), cleanup))
	if !result.IsError {
		t.Fatal("the result is not an error")
	}
	if got := result.Content[0].(*sdk.TextContent).Text; got != "TIMEOUT: timed out" {
		t.Errorf("error line = %q, want the run's own TIMEOUT", got)
	}
	if structured.Backend != backend.Tmux || len(structured.Warnings) != 1 || structured.Warnings[0] != cleanup[0] {
		t.Errorf("structured content = %+v, want the backend and the cleanup warning", structured)
	}
	if _, plain := failure[olympus.Result](backend.Tmux, backend.Errorf(backend.CodeTimeout, "timed out")); plain.Warnings != nil {
		t.Errorf("a failure with no warnings carries %v", plain.Warnings)
	}
}
