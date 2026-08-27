package engine

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/husniadil/olympus/backend"
)

// LockDirName is the reserved directory for lock files (behavior §17.1). It is
// created 0700: the file names encode session names, which are not the
// operator's to share with every user on the machine.
const LockDirName = "olympus-locks"

// lockPoll is the retry interval while waiting.
const lockPoll = 25 * time.Millisecond

// A LockKey identifies one session's write lock.
//
// The triple matters. Two different sockets or directories MUST never contend
// on the same file even when a session name collides, because they are
// genuinely different sessions that happen to share a name.
type LockKey struct {
	// Backend is the resolved backend.
	Backend backend.Name
	// Scope is the socket name or socket directory.
	Scope string
	// Session is the RESOLVED target. A pane-id caller and a session-name
	// caller addressing the same session would otherwise take two different
	// locks and not serialize at all, which is the failure §11.1 names.
	Session string
}

// filename is the lock file for a key.
//
// The digest covers the whole triple, including the session name, and the
// readable prefix is only there to make the directory diagnosable. Sanitizing
// the name alone would be enough to make the path safe, but not enough to keep
// it unique: two distinct sessions whose names sanitize alike would share a
// lock, and a caller writing to one would get a conflict raised by the other —
// a wrong error about a session it never touched.
func (k LockKey) filename() string {
	digest := sha256.Sum256([]byte(string(k.Backend) + "\x00" + k.Scope + "\x00" + k.Session))
	return fmt.Sprintf("%s-%s-%s.lock", k.Backend, sanitize(k.Session), hex.EncodeToString(digest[:8]))
}

// sanitize keeps a name usable as a path component.
func sanitize(name string) string {
	var b strings.Builder
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '.', r == '_', r == '-':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	out := b.String()
	// A very long name would push the file name past the filesystem's limit;
	// the digest already carries the identity, so the readable part can be cut.
	if len(out) > 64 {
		out = out[:64]
	}
	return out
}

// A Lock is a held write lock.
type Lock struct {
	file *os.File
}

// Locks hands out per-session write locks.
//
// The locking is advisory: flock is cooperative, so only other processes going
// through this same path observe it. A human typing into a raw attach client,
// or any other writer, is unaffected and can still race. That is a documented
// limit, not a defect to engineer around.
type Locks struct {
	dir string
}

// NewLocks builds a lock manager rooted at the reserved directory.
func NewLocks() (*Locks, error) {
	return NewLocksIn(filepath.Join(os.TempDir(), LockDirName))
}

// NewLocksIn builds a lock manager rooted at a given directory, so tests can
// use a private one.
func NewLocksIn(dir string) (*Locks, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, backend.Wrapf(backend.CodeUnexpected, err, "creating the lock directory")
	}
	return &Locks{dir: dir}, nil
}

// Acquire takes a session's write lock, waiting up to the given budget.
//
// Contention is a conflict-class error rather than a timeout: the operation did
// not run out of time doing its own work, it was refused because somebody else
// holds the session.
func (l *Locks) Acquire(ctx context.Context, key LockKey, wait time.Duration) (*Lock, error) {
	path := filepath.Join(l.dir, key.filename())
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, backend.Wrapf(backend.CodeUnexpected, err, "opening the lock for %s", key.Session)
	}

	deadline := time.Now().Add(wait)
	for {
		err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			return &Lock{file: file}, nil
		}
		if err != syscall.EWOULDBLOCK {
			file.Close()
			return nil, backend.Wrapf(backend.CodeUnexpected, err, "locking %s", key.Session)
		}
		if time.Now().After(deadline) {
			file.Close()
			return nil, backend.Errorf(backend.CodeConflict,
				"session %s is being written to by another caller", key.Session)
		}
		select {
		case <-ctx.Done():
			file.Close()
			return nil, backend.Wrapf(backend.CodeTimeout, ctx.Err(), "waiting for the lock on %s", key.Session)
		case <-time.After(lockPoll):
		}
	}
}

// Release drops the lock.
func (lk *Lock) Release() error {
	if lk == nil || lk.file == nil {
		return nil
	}
	// Closing the descriptor releases the flock; unlocking first makes that
	// explicit rather than incidental. The file itself is left in place:
	// removing it would race another process that has it open and is about to
	// lock the now-unlinked inode, which would let both run at once.
	_ = syscall.Flock(int(lk.file.Fd()), syscall.LOCK_UN)
	err := lk.file.Close()
	lk.file = nil
	return err
}

// WithLock runs fn holding a session's write lock.
//
// Passing a nil Locks runs fn unlocked, which is how a caller that already
// serializes its own writes opts out. That has to be an explicit choice at the
// door, never a default.
func WithLock(ctx context.Context, locks *Locks, key LockKey, wait time.Duration, fn func() error) error {
	if locks == nil {
		return fn()
	}
	lock, err := locks.Acquire(ctx, key, wait)
	if err != nil {
		return err
	}
	defer lock.Release()
	return fn()
}
