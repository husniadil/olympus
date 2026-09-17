package mcp

import (
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

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
}
