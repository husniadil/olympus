package zmx

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/husniadil/olympus/backend"
)

// §2.5 The budget is counted against the directory zmx itself picks: ZMX_DIR,
// then $XDG_RUNTIME_DIR/zmx, then $TMPDIR/zmx-<uid> (measured with
// `zmx version`, which prints the directory it resolved).
func TestTheSocketDirectoryIsResolvedAsZmxResolvesIt(t *testing.T) {
	t.Setenv("ZMX_DIR", "")
	t.Setenv("XDG_RUNTIME_DIR", "")
	t.Setenv("TMPDIR", "/tmp/oly-tmp")
	if got, want := New().validationDir(), filepath.Join("/tmp/oly-tmp", fmt.Sprintf("zmx-%d", os.Getuid())); got != want {
		t.Errorf("with neither set the directory is %q, want %q", got, want)
	}

	t.Setenv("XDG_RUNTIME_DIR", "/run/user/501")
	if got, want := New().validationDir(), "/run/user/501/zmx"; got != want {
		t.Errorf("with XDG_RUNTIME_DIR set the directory is %q, want %q", got, want)
	}

	t.Setenv("ZMX_DIR", "/tmp/oly-zmx")
	if got, want := New().validationDir(), "/tmp/oly-zmx"; got != want {
		t.Errorf("with ZMX_DIR set the directory is %q, want %q", got, want)
	}
}

// §2.5 A session name is the last component of its socket path, so a slash or
// a NUL cannot be one. Refused as usage before zmx is asked, rather than left to
// time out in the registration poll as an unavailable backend.
func TestANameThatCannotBeAPathComponentIsUsage(t *testing.T) {
	z := New(WithDir("/tmp/oly-zmx"))
	for _, name := range []string{"a/b", "/a", "a\x00b", ".", ".."} {
		if err := z.validateName(name); backend.CodeOf(err) != backend.CodeUsage {
			t.Errorf("validating %q is %q, want %q (err %v)", name, backend.CodeOf(err), backend.CodeUsage, err)
		}
	}
	if err := z.validateName("a.b-c_d"); err != nil {
		t.Errorf("validating an ordinary name: %v", err)
	}
}
