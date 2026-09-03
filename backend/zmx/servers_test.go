package zmx_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/husniadil/olympus/backend"
	"github.com/husniadil/olympus/backend/zmx"
)

// §13.2 zmx has one server per directory, so the listing is exactly one row —
// the directory in use, running when it exists — and there is no stopper.
func TestServersIsTheOneDirectory(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "zmx")
	b := zmx.New(zmx.WithDir(dir))

	servers, err := b.Servers(context.Background())
	if err != nil {
		t.Fatalf("listing: %v", err)
	}
	want := []backend.Server{{Name: "default", SocketPath: dir, Running: false, Default: true, Dir: dir}}
	if len(servers) != 1 || servers[0] != want[0] {
		t.Fatalf("listed %+v, want %+v", servers, want)
	}

	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	servers, err = b.Servers(context.Background())
	if err != nil || len(servers) != 1 || !servers[0].Running {
		t.Errorf("with the directory present the listing is %+v (%v), want one running row", servers, err)
	}

	if _, ok := backend.Backend(b).(backend.ServerStopper); ok {
		t.Error("zmx claims to stop a server, and it has no server process apart from its sessions")
	}
}
