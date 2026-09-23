package cli

import (
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/husniadil/olympus"
	"github.com/husniadil/olympus/backend"
)

func specFrom(opts []olympus.AttachOption) backend.AttachSpec {
	spec := backend.AttachSpec{Cols: olympus.DefaultCols, Rows: olympus.DefaultRows}
	for _, opt := range opts {
		opt(&spec)
	}
	return spec
}

// --view and --no-mouse reach the ergonomic layer whether or not --bare was
// given, so it can refuse them as USAGE where no view is made rather than the
// door dropping them silently.
func TestAttachPassesViewOptionsWithoutBare(t *testing.T) {
	spec := specFrom(attachFlags{viewName: "olympus-view-x", noMouse: true}.options())
	if spec.BareView != "olympus-view-x" || !spec.BareNoMouse {
		t.Errorf("without --bare the spec is %+v, want the view name and no-mouse carried", spec)
	}
}

// A process inside a meja session is sent back to that session's own server:
// the socket Self reports is the one the handle addresses, as on tmux and
// herdr, rather than meja's default.
func TestOpenAtAddressesTheMejaServerSelfReported(t *testing.T) {
	if _, err := exec.LookPath("meja"); err != nil {
		t.Skip("meja is not installed")
	}
	socket := filepath.Join(t.TempDir(), "m.sock")
	ol, err := (&App{}).openAt(olympus.Identity{Inside: true, Backend: backend.Meja, Session: "s", Scope: socket})
	if err != nil {
		t.Fatalf("openAt: %v", err)
	}
	defer ol.Close()
	scoped, ok := ol.Raw().(interface{ Scope() string })
	if !ok || scoped.Scope() != socket {
		t.Errorf("the handle addresses %v, want the reported socket %s", ol.Raw(), socket)
	}
}

// One of --cols and --rows alone sizes that side and leaves the other at its
// default, as start and new do, rather than being ignored.
func TestAttachSizesTheOneSideGiven(t *testing.T) {
	if spec := specFrom(attachFlags{cols: 132}.options()); spec.Cols != 132 || spec.Rows != olympus.DefaultRows {
		t.Errorf("--cols 132 alone gives %dx%d, want 132x%d", spec.Cols, spec.Rows, olympus.DefaultRows)
	}
	if spec := specFrom(attachFlags{rows: 50}.options()); spec.Cols != olympus.DefaultCols || spec.Rows != 50 {
		t.Errorf("--rows 50 alone gives %dx%d, want %dx50", spec.Cols, spec.Rows, olympus.DefaultCols)
	}
}
