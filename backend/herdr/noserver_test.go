package herdr

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/husniadil/olympus/backend"
)

// §12.3 With no server running, a target-addressed verb answers not-found for
// its target, as it does wherever it resolves the target first. A capability
// asked before the target used to fail as a dial error instead.
func TestAVerbThatAsksACapabilityFirstIsNotFoundWithNoServer(t *testing.T) {
	h := New(WithSocketPath(filepath.Join(shortDir(t), "h.sock")))
	ctx := context.Background()
	if err := h.Redraw(ctx, "w1"); backend.CodeOf(err) != backend.CodeSessionNotFound {
		t.Errorf("redraw with no server is %q, want %q (err %v)", backend.CodeOf(err), backend.CodeSessionNotFound, err)
	}
	_, err := h.Attach(ctx, "w1", backend.AttachSpec{Role: backend.RoleController, SessionClient: true, Bare: true, ClientTag: "t1"})
	if backend.CodeOf(err) != backend.CodeSessionNotFound {
		t.Errorf("a tagged bare attach with no server is %q, want %q (err %v)", backend.CodeOf(err), backend.CodeSessionNotFound, err)
	}
}

// §12.3 herdr's own no-server answer is not-found wherever it surfaces, and
// still the sentinel the listing and snapshot paths collapse into empty. A
// server that went away between resolving a target and acting on it used to
// surface as UNEXPECTED.
func TestHerdrsNoServerAnswerIsNotFound(t *testing.T) {
	stdout := `{"error":{"code":"server_not_running","message":"no server running"}}`
	err := classify(errors.New("exit status 1"), stdout, "", []string{"pane", "close", "w1:p1"})
	if !errors.Is(err, errNoServer) {
		t.Errorf("classify lost the no-server sentinel: %v", err)
	}
	if got := backend.CodeOf(err); got != backend.CodeSessionNotFound {
		t.Errorf("no server is %q, want %q (err %v)", got, backend.CodeSessionNotFound, err)
	}
}
