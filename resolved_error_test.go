package olympus

import (
	"testing"

	"github.com/husniadil/olympus/backend"
)

// A failure after resolution still knows which backend it was about, and the
// envelope has to say so: api §2 lets `backend` be omitted only before any
// backend was resolved.
func TestAnOpenFailureAfterResolutionNamesTheBackend(t *testing.T) {
	_, err := open(config{explicit: "tmux", installs: func(backend.Name) bool { return true }}, WithZmxDir("/tmp/x"))
	if CodeOf(err) != backend.CodeUsage {
		t.Fatalf("error is %v, want USAGE", err)
	}
	if got, ok := ResolvedBackendOf(err); !ok || got != backend.Tmux {
		t.Fatalf("ResolvedBackendOf = %q, %v; want tmux, true", got, ok)
	}
}

func TestAnOpenFailureBeforeResolutionNamesNoBackend(t *testing.T) {
	_, err := open(config{explicit: "bogus", installs: func(backend.Name) bool { return true }})
	if err == nil {
		t.Fatal("an unknown backend opened")
	}
	if got, ok := ResolvedBackendOf(err); ok {
		t.Fatalf("ResolvedBackendOf = %q before any backend resolved", got)
	}
}
