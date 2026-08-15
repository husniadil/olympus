package engine_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/husniadil/olympus/backend"
	"github.com/husniadil/olympus/internal/engine"
)

func newLocks(t *testing.T) *engine.Locks {
	t.Helper()
	locks, err := engine.NewLocksIn(filepath.Join(t.TempDir(), "locks"))
	if err != nil {
		t.Fatalf("NewLocksIn: %v", err)
	}
	return locks
}

func key(session string) engine.LockKey {
	return engine.LockKey{Backend: backend.Tmux, Scope: "olympus", Session: session}
}

func TestOneWriterAtATimePerSession(t *testing.T) {
	locks := newLocks(t)
	ctx := context.Background()

	held, err := locks.Acquire(ctx, key("build"), time.Second)
	if err != nil {
		t.Fatalf("acquiring: %v", err)
	}

	// A second writer must be refused rather than allowed to interleave.
	_, err = locks.Acquire(ctx, key("build"), 50*time.Millisecond)
	if !errors.Is(err, backend.ErrConflict) {
		t.Errorf("a second writer got %v, want a conflict", err)
	}

	if err := held.Release(); err != nil {
		t.Fatalf("releasing: %v", err)
	}
	after, err := locks.Acquire(ctx, key("build"), time.Second)
	if err != nil {
		t.Fatalf("acquiring after release: %v", err)
	}
	_ = after.Release()
}

// Contention is a conflict, not a timeout. The operation was refused because
// somebody else holds the session, which is a different thing from running out
// of time doing its own work — and a caller retries them differently.
func TestContentionIsAConflictNotATimeout(t *testing.T) {
	locks := newLocks(t)
	held, err := locks.Acquire(context.Background(), key("build"), time.Second)
	if err != nil {
		t.Fatalf("acquiring: %v", err)
	}
	defer held.Release()

	_, err = locks.Acquire(context.Background(), key("build"), 20*time.Millisecond)
	if backend.CodeOf(err) != backend.CodeConflict {
		t.Errorf("contention is %q, want %q", backend.CodeOf(err), backend.CodeConflict)
	}
	if !strings.Contains(err.Error(), "build") {
		t.Errorf("the error %q does not name the session", err.Error())
	}
}

// Different sockets or directories are genuinely different sessions that happen
// to share a name, and must never contend.
func TestTheSameNameOnDifferentScopesDoesNotContend(t *testing.T) {
	locks := newLocks(t)
	ctx := context.Background()

	first, err := locks.Acquire(ctx, engine.LockKey{Backend: backend.Tmux, Scope: "socket-a", Session: "build"}, time.Second)
	if err != nil {
		t.Fatalf("acquiring the first: %v", err)
	}
	defer first.Release()

	second, err := locks.Acquire(ctx, engine.LockKey{Backend: backend.Tmux, Scope: "socket-b", Session: "build"}, 200*time.Millisecond)
	if err != nil {
		t.Fatalf("the same name on a different socket contended: %v", err)
	}
	_ = second.Release()
}

// The same name on different backends is likewise a different session — the
// backend is part of the identity, which is why every envelope discloses it.
func TestTheSameNameOnDifferentBackendsDoesNotContend(t *testing.T) {
	locks := newLocks(t)
	ctx := context.Background()

	first, err := locks.Acquire(ctx, engine.LockKey{Backend: backend.Tmux, Scope: "s", Session: "build"}, time.Second)
	if err != nil {
		t.Fatalf("acquiring: %v", err)
	}
	defer first.Release()

	second, err := locks.Acquire(ctx, engine.LockKey{Backend: backend.Zmx, Scope: "s", Session: "build"}, 200*time.Millisecond)
	if err != nil {
		t.Fatalf("the same name on a different backend contended: %v", err)
	}
	_ = second.Release()
}

// Sanitizing a name makes it a safe path component but not a unique one. Two
// sessions whose names sanitize alike must still take different locks, or a
// caller writing to one gets a conflict raised by a session it never touched.
func TestNamesThatSanitizeAlikeStillTakeDifferentLocks(t *testing.T) {
	locks := newLocks(t)
	ctx := context.Background()

	first, err := locks.Acquire(ctx, key("my build"), time.Second)
	if err != nil {
		t.Fatalf("acquiring: %v", err)
	}
	defer first.Release()

	second, err := locks.Acquire(ctx, key("my_build"), 200*time.Millisecond)
	if err != nil {
		t.Fatalf("two names that sanitize alike shared a lock: %v", err)
	}
	_ = second.Release()
}

// A name is not a path. Anything that could escape the lock directory has to
// stop being a path component before it is used as one.
func TestAHostileNameCannotEscapeTheLockDirectory(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "locks")
	locks, err := engine.NewLocksIn(dir)
	if err != nil {
		t.Fatalf("NewLocksIn: %v", err)
	}

	held, err := locks.Acquire(context.Background(), key("../../escaped"), time.Second)
	if err != nil {
		t.Fatalf("acquiring: %v", err)
	}
	defer held.Release()

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("reading the lock directory: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("the lock directory holds %d entries, want 1 — the name escaped", len(entries))
	}
}

// The directory carries session names in its file names, which are not the
// operator's to share with every user on the machine.
func TestTheLockDirectoryIsPrivate(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "locks")
	if _, err := engine.NewLocksIn(dir); err != nil {
		t.Fatalf("NewLocksIn: %v", err)
	}
	info, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o700 {
		t.Errorf("the lock directory is mode %o, want 700", perm)
	}
}

// The point of the lock: concurrent writers observe one at a time.
func TestConcurrentWritersSerialize(t *testing.T) {
	locks := newLocks(t)
	ctx := context.Background()

	var mu sync.Mutex
	inside, maxInside := 0, 0

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := engine.WithLock(ctx, locks, key("build"), 5*time.Second, func() error {
				mu.Lock()
				inside++
				if inside > maxInside {
					maxInside = inside
				}
				mu.Unlock()

				time.Sleep(5 * time.Millisecond)

				mu.Lock()
				inside--
				mu.Unlock()
				return nil
			})
			if err != nil {
				t.Errorf("WithLock: %v", err)
			}
		}()
	}
	wg.Wait()

	if maxInside != 1 {
		t.Errorf("%d writers were inside the critical section at once, want 1", maxInside)
	}
}

// Opting out must be possible for a caller that already serializes its own
// writes — and must be an explicit choice, never a default.
func TestOptingOutRunsWithoutTakingTheLock(t *testing.T) {
	locks := newLocks(t)
	ctx := context.Background()

	held, err := locks.Acquire(ctx, key("build"), time.Second)
	if err != nil {
		t.Fatalf("acquiring: %v", err)
	}
	defer held.Release()

	ran := false
	// A nil manager is the opt-out, and it must not block on the held lock.
	if err := engine.WithLock(ctx, nil, key("build"), time.Millisecond, func() error {
		ran = true
		return nil
	}); err != nil {
		t.Fatalf("the unlocked path failed: %v", err)
	}
	if !ran {
		t.Error("the unlocked path did not run")
	}
}

func TestReleasingTwiceIsSafe(t *testing.T) {
	locks := newLocks(t)
	held, err := locks.Acquire(context.Background(), key("build"), time.Second)
	if err != nil {
		t.Fatalf("acquiring: %v", err)
	}
	if err := held.Release(); err != nil {
		t.Fatalf("first release: %v", err)
	}
	if err := held.Release(); err != nil {
		t.Errorf("second release: %v", err)
	}
}
