package herdr

import (
	"context"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/husniadil/olympus/backend"
)

// Plain `herdr` starts a server when none answers, so a session client
// launched onto a path whose server died after the target resolved would boot
// a new one there with the caller's own configuration (§8.10). The server is
// asked once more at the last moment the client can still be withheld.
func TestASessionClientIsWithheldFromAServerThatStoppedAnswering(t *testing.T) {
	if err := exec.Command("herdr", "--version").Run(); err != nil {
		t.Skipf("herdr does not run here: %v", err)
	}
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(dir, "config"))
	t.Setenv("XDG_STATE_HOME", filepath.Join(dir, "state"))
	h := New(WithSocketPath(filepath.Join(dir, "herdr.sock")))

	_, _, err := h.sessionClientCommand(context.Background())
	if backend.CodeOf(err) != backend.CodeSessionNotFound {
		t.Fatalf("a session client onto a server that does not answer: got %v, want SESSION_NOT_FOUND", err)
	}
}
