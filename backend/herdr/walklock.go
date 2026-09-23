package herdr

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/husniadil/olympus/backend"
	"github.com/husniadil/olympus/internal/privatedir"
)

// A walkLock serializes the bare attaches onto one server, across processes:
// each Olympus attach is its own process, and a bare client comes up on the
// server's focus, which the walk of ANOTHER bare client moves. Two tabs
// opened a beat apart read the focus at two moments and one of them walked
// from a workspace it was never on (measured 2026-09-13: the second client
// spawned on w14, the first's walk moved the focus to wY meanwhile, and the
// second walked wY → wZ from w14 and landed on wY). The lock is held from the
// moment the focus is read for a client to the last key of its walk, so what
// was read is where the client comes up. It lives in the reserved lock
// directory (behavior §17.1) under a name the socket path decides, and is
// advisory: only attaches through this path observe it.
type walkLock struct {
	mu   sync.Mutex
	file *os.File
}

const (
	walkLockDir  = "olympus-locks"
	walkLockPoll = 25 * time.Millisecond
	// walkLockWait bounds the wait: the engine settles every client within
	// a few seconds (§8.10), so a holder past this has hung, and an attach
	// that waits forever behind it is worse than one that says so.
	walkLockWait = 15 * time.Second
)

func acquireWalkLock(ctx context.Context, socketPath string) (*walkLock, error) {
	dir := filepath.Join(os.TempDir(), walkLockDir)
	if err := privatedir.Ensure(dir, "lock directory"); err != nil {
		return nil, err
	}
	digest := sha256.Sum256([]byte(socketPath))
	path := filepath.Join(dir, "herdr-walk-"+hex.EncodeToString(digest[:8])+".lock")
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, backend.Wrapf(backend.CodeUnexpected, err, "opening the walk lock")
	}
	deadline := time.Now().Add(walkLockWait)
	for {
		err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			return &walkLock{file: file}, nil
		}
		if err != syscall.EWOULDBLOCK {
			file.Close()
			return nil, backend.Wrapf(backend.CodeUnexpected, err, "taking the walk lock")
		}
		if time.Now().After(deadline) {
			file.Close()
			return nil, backend.Errorf(backend.CodeConflict,
				"another bare attach onto this server has not settled in %s", walkLockWait)
		}
		select {
		case <-ctx.Done():
			file.Close()
			return nil, backend.Wrapf(backend.CodeTimeout, ctx.Err(), "waiting for the walk lock")
		case <-time.After(walkLockPoll):
		}
	}
}

// release drops the lock. Safe on nil, more than once and from two goroutines
// at once: the walk releases it, and so does the attachment's cleanup, for a
// client that ended before it ever settled. The file stays, as the engine's locks do: removing it
// would race a process that has it open and is about to lock the unlinked
// inode.
func (l *walkLock) release() {
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.file == nil {
		return
	}
	_ = syscall.Flock(int(l.file.Fd()), syscall.LOCK_UN)
	_ = l.file.Close()
	l.file = nil
}
