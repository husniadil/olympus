package herdr

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/husniadil/olympus/backend"
)

// The default socket's directory is a fixed name in the shared temp root, so
// another user can make it first and choose the configuration a server Olympus
// starts reads. A symlink in its place is refused before any invocation, the
// way the lock directories refuse one (§11.1). A directory another user owns is
// refused by the same check, which a test cannot stage without root.
func TestTheDefaultSocketDirectoryMustBeThisUsersOwn(t *testing.T) {
	root := shortDir(t)
	elsewhere := shortDir(t)
	t.Setenv("TMPDIR", root)
	if err := os.Symlink(elsewhere, filepath.Join(root, DefaultSocketName)); err != nil {
		t.Fatal(err)
	}

	err := New().validateSocketPath()

	if backend.CodeOf(err) != backend.CodeUnexpected {
		t.Fatalf("a symlinked default socket directory was accepted: %v", err)
	}
}

func TestALooseDefaultSocketDirectoryIsTightened(t *testing.T) {
	root := shortDir(t)
	t.Setenv("TMPDIR", root)
	dir := filepath.Join(root, DefaultSocketName)
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o777); err != nil {
		t.Fatal(err)
	}

	if err := New().validateSocketPath(); err != nil {
		t.Fatal(err)
	}

	fi, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o700 {
		t.Fatalf("the default socket directory is %v, want 0700", fi.Mode().Perm())
	}
}

// A socket the caller chose sits where the caller put it, and its directory's
// mode is theirs.
func TestAChosenSocketDirectoryIsLeftAsItIs(t *testing.T) {
	dir := shortDir(t)
	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatal(err)
	}

	if err := New(WithSocketPath(filepath.Join(dir, "h.sock"))).validateSocketPath(); err != nil {
		t.Fatal(err)
	}

	fi, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o755 {
		t.Fatalf("a chosen socket's directory became %v", fi.Mode().Perm())
	}
}
