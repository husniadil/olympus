package herdr

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// The walk lock shares the engine's lock directory under a shared temp dir, so
// it holds that directory to the same rule: ours and private (behavior §11.1).
func TestTheWalkLockRefusesALockDirectoryItDoesNotOwn(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	elsewhere := t.TempDir()
	if err := os.Symlink(elsewhere, filepath.Join(tmp, walkLockDir)); err != nil {
		t.Fatal(err)
	}
	if lock, err := acquireWalkLock(context.Background(), "/tmp/any.sock"); err == nil {
		lock.release()
		t.Fatal("the walk lock was taken through a symlinked lock directory")
	}
}

func TestTheWalkLockTightensALooseLockDirectory(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	dir := filepath.Join(tmp, walkLockDir)
	if err := os.Mkdir(dir, 0o777); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o777); err != nil {
		t.Fatal(err)
	}
	lock, err := acquireWalkLock(context.Background(), "/tmp/any.sock")
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	lock.release()
	fi, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o700 {
		t.Errorf("the lock directory is %v, want 0700", fi.Mode().Perm())
	}
}
