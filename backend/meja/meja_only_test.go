package meja_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/husniadil/olympus/backend"
	"github.com/husniadil/olympus/backend/meja"
)

func newBackend(t *testing.T) (backend.Backend, string) {
	t.Helper()
	dir, err := os.MkdirTemp(os.TempDir(), "olym")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	socket := filepath.Join(dir, "m.sock")
	t.Cleanup(func() { _ = exec.Command("meja", "-S", socket, "kill-server").Run() })
	return meja.New(meja.WithSocketPath(socket)), socket
}

// §8: the attach client must address the server it was configured with, and a
// viewer must be refused rather than silently handed a client that can type.
//
// meja has no read-only client. Accepting a viewer attach and giving it a full
// one is the dangerous failure: a watcher who believes they cannot type, and
// can, will eventually type into somebody else's session.
func TestAttachAddressesItsServerAndRefusesAViewer(t *testing.T) {
	requireMeja(t)
	b, socket := newBackend(t)
	ctx := context.Background()

	if _, err := b.Create(ctx, backend.CreateSpec{Name: "att", Dir: t.TempDir()}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	att, err := b.Attach(ctx, "att", backend.AttachSpec{Role: backend.RoleController})
	if err != nil {
		t.Fatalf("preparing an attach: %v", err)
	}
	args := strings.Join(att.Cmd.Args, " ")
	if !strings.Contains(args, "-S "+socket) {
		t.Errorf("attach argv does not address the configured server:\n  %s", args)
	}
	if !strings.Contains(args, "attach -t att") {
		t.Errorf("attach argv does not target the session:\n  %s", args)
	}

	if _, err := b.Attach(ctx, "att", backend.AttachSpec{Role: backend.RoleViewer}); backend.CodeOf(err) != backend.CodeUnsupported {
		t.Errorf("a viewer attach reports %v, want UNSUPPORTED — meja has no read-only client", backend.CodeOf(err))
	}

	// Supersede has no mechanism either, and a silent no-op would leave the
	// caller believing prior clients were displaced when they are still there
	// — and meja sizes the session to the smallest of them (§8.5).
	superseded, err := b.Attach(ctx, "att", backend.AttachSpec{Role: backend.RoleController, Supersede: true})
	if err != nil {
		t.Fatalf("preparing a superseding attach: %v", err)
	}
	if len(superseded.Notices) == 0 {
		t.Error("a superseding attach on meja says nothing, but nothing was superseded")
	}

	// §10: attaching onto nothing is not-found, decided before the client runs.
	if _, err := b.Attach(ctx, "never-existed", backend.AttachSpec{}); backend.CodeOf(err) != backend.CodeSessionNotFound {
		t.Errorf("attaching to an absent session reports %v, want SESSION_NOT_FOUND", backend.CodeOf(err))
	}
}
