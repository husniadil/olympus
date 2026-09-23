// Package privatedir holds Olympus's per-user directories under a shared temp
// root to one rule: a real directory this user owns, private to it
// (behavior §11.1). The engine's locks and attach guard and herdr's walk lock
// share one such directory, so they share the check.
package privatedir

import (
	"os"
	"syscall"

	"github.com/husniadil/olympus/backend"
)

// Ensure creates dir 0700, or accepts an existing one only if it is a real
// directory this user owns, tightening its mode if it is looser.
//
// The default root is a shared temp directory, where another user can create
// the name first. MkdirAll accepts whatever it finds, so without this check our
// lock files and pidfiles would live in a directory someone else controls: they
// could hold our locks, swap our pidfiles, or read the session names.
func Ensure(dir, what string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return backend.Wrapf(backend.CodeUnexpected, err, "creating the %s", what)
	}
	fi, err := os.Lstat(dir)
	if err != nil {
		return backend.Wrapf(backend.CodeUnexpected, err, "inspecting the %s", what)
	}
	if !fi.IsDir() {
		return backend.Errorf(backend.CodeUnexpected, "the %s %s is not a directory (a symlink or file is in its place), so it is not used", what, dir)
	}
	if st, ok := fi.Sys().(*syscall.Stat_t); ok && int(st.Uid) != os.Getuid() {
		return backend.Errorf(backend.CodeUnexpected, "the %s %s belongs to another user (uid %d), so it is not used", what, dir, st.Uid)
	}
	if fi.Mode().Perm()&0o077 != 0 {
		if err := os.Chmod(dir, 0o700); err != nil {
			return backend.Wrapf(backend.CodeUnexpected, err, "restricting the %s %s to its owner", what, dir)
		}
	}
	return nil
}
