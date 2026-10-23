package herdr

import (
	"strings"
	"testing"
)

// An invocation an error names may hold a caller's text, so the message
// carries only its start.
func TestAnErrorNamesOnlyTheStartOfAnInvocation(t *testing.T) {
	long := strings.Repeat("é", 200)
	got := argsForMessage([]string{"pane", "send-text", "w1:p1", long})
	if len(got) > 124 || !strings.HasSuffix(got, "…") || !strings.HasPrefix(got, "pane send-text w1:p1 é") {
		t.Fatalf("argsForMessage = %q (%d bytes)", got, len(got))
	}
	if !strings.HasPrefix(got, strings.TrimSuffix(got, "…")) || strings.ContainsRune(strings.TrimSuffix(got, "…"), '�') {
		t.Fatalf("the cut split a character: %q", got)
	}
	if got := argsForMessage([]string{"pane", "list"}); got != "pane list" {
		t.Fatalf("a short invocation was changed: %q", got)
	}
}
