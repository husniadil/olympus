package olympus

import (
	"context"
	"testing"

	"github.com/husniadil/olympus/backend"
)

// An empty id names no run, so a poll with one is refused as USAGE here, once,
// for every door: scanning the scrollback for a marker that cannot exist would
// report the run as died, a wrong answer dressed as a real one.
func TestPollingWithAnEmptyIDIsUsage(t *testing.T) {
	s := &Session{ol: fakeOlympus(&fakeBackend{caps: backend.Capabilities{Backend: backend.Tmux}}), name: "demo"}
	_, err := s.Poll(context.Background(), "")
	if backend.CodeOf(err) != backend.CodeUsage {
		t.Errorf("polling with an empty id is %v, want USAGE", err)
	}
}
