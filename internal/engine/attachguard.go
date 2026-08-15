//go:build darwin || linux

package engine

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/husniadil/olympus/backend"
)

// Attach-guard defaults (behavior §17.3).
const (
	DefaultStealWait = 3 * time.Second
	DefaultStealPoll = 50 * time.Millisecond
	// acquisitionAttempts bounds the restart loop below. A restart happens
	// only when another process replaced the pidfile between the open and the
	// lock, which cannot repeat indefinitely in practice.
	acquisitionAttempts = 8
)

// An AttachGuard arbitrates the attach slot for a session.
//
// This is a SEPARATE mechanism from the write lock and must not reuse it:
// stealing an attach slot is a different contention problem from serializing
// session writes, and one caller waiting to write should not be able to
// displace another caller's live terminal (behavior §11.3).
type AttachGuard struct {
	dir string
}

// NewAttachGuard builds a guard rooted at a directory.
func NewAttachGuard(dir string) (*AttachGuard, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, backend.Wrapf(backend.CodeUnexpected, err, "creating the attach-guard directory")
	}
	return &AttachGuard{dir: dir}, nil
}

// An AttachSlot is a held attach slot.
type AttachSlot struct {
	file *os.File
	path string
}

// Release drops the slot.
func (s *AttachSlot) Release() error {
	if s == nil || s.file == nil {
		return nil
	}
	_ = syscall.Flock(int(s.file.Fd()), syscall.LOCK_UN)
	err := s.file.Close()
	s.file = nil
	return err
}

func (g *AttachGuard) path(key LockKey) string {
	digest := sha256.Sum256([]byte(string(key.Backend) + "\x00" + key.Scope + "\x00" + key.Session))
	return filepath.Join(g.dir, fmt.Sprintf("olympus-attach-%s-%s.pid", hex.EncodeToString(digest[:8]), sanitize(key.Session)))
}

// Acquire takes the attach slot, optionally displacing a live holder.
//
// The flock — not the pidfile's content — is the exclusivity mechanism. The pid
// is read only to know whom to signal, so a stale file left by a holder that
// crashed between writing it and exiting is reclaimed silently rather than
// blocking the slot forever.
func (g *AttachGuard) Acquire(ctx context.Context, key LockKey, steal bool, wait, poll time.Duration) (*AttachSlot, error) {
	path := g.path(key)

	for attempt := 0; attempt < acquisitionAttempts; attempt++ {
		slot, retry, err := g.tryAcquire(ctx, path, key, steal, wait, poll)
		if err != nil {
			return nil, err
		}
		if retry {
			continue
		}
		return slot, nil
	}
	return nil, backend.Errorf(backend.CodeConflict,
		"could not settle the attach slot for %s: it kept being replaced underneath us", key.Session)
}

// tryAcquire is one attempt. It reports retry=true when the path was replaced
// underneath it and the whole acquisition must restart from the top.
func (g *AttachGuard) tryAcquire(ctx context.Context, path string, key LockKey, steal bool, wait, poll time.Duration) (*AttachSlot, bool, error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, false, backend.Wrapf(backend.CodeUnexpected, err, "opening the attach slot for %s", key.Session)
	}

	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		if err != syscall.EWOULDBLOCK {
			file.Close()
			return nil, false, backend.Wrapf(backend.CodeUnexpected, err, "locking the attach slot for %s", key.Session)
		}
		holder := readPID(file)
		file.Close()

		if !steal {
			// An immediate conflict, BEFORE any PTY is spawned: there is no
			// point building a terminal we are about to throw away.
			return nil, false, backend.Errorf(backend.CodeConflict,
				"session %s is already attached by pid %d; pass the steal option to take it over", key.Session, holder)
		}
		if err := g.steal(ctx, path, key, holder, wait, poll); err != nil {
			return nil, false, err
		}
		return nil, true, nil
	}

	// Winning the flock is NOT proof of holding the slot.
	//
	// unlink detaches a name from an inode without invalidating an fd a waiter
	// already holds on it. A waiter blocked on an fd opened before a concurrent
	// release can win the lock on that now-nameless inode at the same moment a
	// third acquisition creates a brand-new inode at the same path and also
	// succeeds — two callers both believing they hold the slot. So the fd's
	// identity is re-verified against a fresh stat of the path, and any
	// mismatch restarts the acquisition so it lands on whatever inode is
	// actually there now (behavior §8.5).
	same, err := sameInode(file, path)
	if err != nil || !same {
		_ = syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
		file.Close()
		return nil, true, nil
	}

	if err := writePID(file, os.Getpid()); err != nil {
		_ = syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
		file.Close()
		return nil, false, err
	}
	return &AttachSlot{file: file, path: path}, false, nil
}

// steal signals the holder and waits for the slot to free.
func (g *AttachGuard) steal(ctx context.Context, path string, key LockKey, holder int, wait, poll time.Duration) error {
	if holder > 0 {
		// Accepted risk: between reading the pid and signalling it, the OS
		// could reap the holder and recycle its pid onto an unrelated process.
		// This is unfixable without a handle-based signal primitive, which Go
		// does not expose. The window is narrow but the blast radius is not
		// uniformly bounded, since a process without a handler for this signal
		// hits the POSIX default disposition, which is termination
		// (behavior §8.6).
		_ = syscall.Kill(holder, syscall.SIGUSR1)
	}

	deadline := time.Now().Add(wait)
	for {
		file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
		if err == nil {
			if lockErr := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); lockErr == nil {
				_ = syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
				file.Close()
				return nil
			}
			file.Close()
		}
		if time.Now().After(deadline) {
			// The holder hung, or the signal was lost. Giving up with the same
			// conflict shape beats blocking forever.
			return backend.Errorf(backend.CodeConflict,
				"session %s is attached by pid %d, which did not release within %s", key.Session, holder, wait)
		}
		select {
		case <-ctx.Done():
			return backend.Wrapf(backend.CodeTimeout, ctx.Err(), "waiting for the attach slot on %s", key.Session)
		case <-time.After(poll):
		}
	}
}

func readPID(file *os.File) int {
	if _, err := file.Seek(0, 0); err != nil {
		return 0
	}
	buffer := make([]byte, 32)
	n, _ := file.Read(buffer)
	pid, err := strconv.Atoi(strings.TrimSpace(string(buffer[:n])))
	if err != nil {
		return 0
	}
	return pid
}

func writePID(file *os.File, pid int) error {
	if err := file.Truncate(0); err != nil {
		return backend.Wrapf(backend.CodeUnexpected, err, "recording the attach holder")
	}
	if _, err := file.Seek(0, 0); err != nil {
		return backend.Wrapf(backend.CodeUnexpected, err, "recording the attach holder")
	}
	if _, err := file.WriteString(strconv.Itoa(pid)); err != nil {
		return backend.Wrapf(backend.CodeUnexpected, err, "recording the attach holder")
	}
	return file.Sync()
}

// sameInode reports whether an open file still names the same inode as the path.
func sameInode(file *os.File, path string) (bool, error) {
	held, err := file.Stat()
	if err != nil {
		return false, err
	}
	current, err := os.Stat(path)
	if err != nil {
		// The path is simply gone, which is itself a mismatch.
		return false, nil
	}
	heldStat, ok := held.Sys().(*syscall.Stat_t)
	currentStat, ok2 := current.Sys().(*syscall.Stat_t)
	if !ok || !ok2 {
		return true, nil
	}
	return heldStat.Dev == currentStat.Dev && heldStat.Ino == currentStat.Ino, nil
}
