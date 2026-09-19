package herdr

import (
	"context"
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
