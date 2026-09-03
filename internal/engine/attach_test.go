//go:build darwin || linux

package engine_test

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/husniadil/olympus/backend"
	"github.com/husniadil/olympus/internal/engine"
)

// A caller whose stdin is a pipe has no window and therefore no resize signal,
// so the size has to travel in the stream itself.
func TestTheResizeControlIsParsedAndStripped(t *testing.T) {
	cols, rows, rest, found := engine.ParseResizeControl("before\x1b]olympus;resize;120;40\x07after")
	if !found {
		t.Fatal("the resize control was not recognised")
	}
	if cols != 120 || rows != 40 {
		t.Errorf("parsed %dx%d, want 120x40", cols, rows)
	}
	// Never written into the session: it was addressed to Olympus, and the
	// session would render it as junk.
	if rest != "beforeafter" {
		t.Errorf("the surrounding input came back as %q, want %q", rest, "beforeafter")
	}
}

// A bad control sequence must not kill the session. It is ignored — but still
// stripped, because it was addressed to us either way.
func TestAMalformedResizeControlIsIgnoredButStillStripped(t *testing.T) {
	cases := map[string]string{
		"not enough fields":  "a\x1b]olympus;resize;120\x07b",
		"non-numeric":        "a\x1b]olympus;resize;wide;tall\x07b",
		"zero size":          "a\x1b]olympus;resize;0;0\x07b",
		"too many fields":    "a\x1b]olympus;resize;1;2;3\x07b",
		"negative dimension": "a\x1b]olympus;resize;-1;40\x07b",
	}
	for what, input := range cases {
		_, _, rest, found := engine.ParseResizeControl(input)
		if found {
			t.Errorf("%s: was accepted as a valid resize", what)
		}
		if strings.Contains(rest, "olympus;resize") {
			t.Errorf("%s: the control leaked through to the session: %q", what, rest)
		}
	}
}

// An unterminated control is not a control yet — the rest of it may still be
// coming — so the input passes through untouched rather than being eaten.
func TestAnUnterminatedResizeControlIsLeftAlone(t *testing.T) {
	input := "keys\x1b]olympus;resize;120;40"
	_, _, rest, found := engine.ParseResizeControl(input)
	if found {
		t.Error("an unterminated control was accepted")
	}
	if rest != input {
		t.Errorf("input came back as %q, want it untouched", rest)
	}
}

func TestOrdinaryInputIsUntouched(t *testing.T) {
	input := "ls -la\r"
	_, _, rest, found := engine.ParseResizeControl(input)
	if found {
		t.Error("ordinary keystrokes were read as a resize control")
	}
	if rest != input {
		t.Errorf("input came back as %q, want it untouched", rest)
	}
}

func newGuard(t *testing.T) *engine.AttachGuard {
	t.Helper()
	guard, err := engine.NewAttachGuard(filepath.Join(t.TempDir(), "guard"))
	if err != nil {
		t.Fatalf("NewAttachGuard: %v", err)
	}
	return guard
}

func attachKey(session string) engine.LockKey {
	return engine.LockKey{Backend: backend.Zmx, Scope: "dir", Session: session}
}

func TestOneAttachSlotPerSession(t *testing.T) {
	guard := newGuard(t)
	ctx := context.Background()

	held, err := guard.Acquire(ctx, attachKey("build"), false, time.Second, 10*time.Millisecond)
	if err != nil {
		t.Fatalf("acquiring: %v", err)
	}
	defer held.Release()

	// Without steal, a live holder is an immediate conflict — reported BEFORE
	// any terminal is built, since there is no point constructing one only to
	// throw it away.
	started := time.Now()
	_, err = guard.Acquire(ctx, attachKey("build"), false, time.Second, 10*time.Millisecond)
	if !errors.Is(err, backend.ErrConflict) {
		t.Errorf("a second attach got %v, want a conflict", err)
	}
	if elapsed := time.Since(started); elapsed > 200*time.Millisecond {
		t.Errorf("the conflict took %s, so it waited instead of failing immediately", elapsed)
	}
	// The stealer's side genuinely knows the holder's pid, so it may say so.
	if err != nil && !strings.Contains(err.Error(), "build") {
		t.Errorf("the conflict does not name the session: %v", err)
	}
}

func TestReleasingFreesTheAttachSlot(t *testing.T) {
	guard := newGuard(t)
	ctx := context.Background()

	held, err := guard.Acquire(ctx, attachKey("build"), false, time.Second, 10*time.Millisecond)
	if err != nil {
		t.Fatalf("acquiring: %v", err)
	}
	if err := held.Release(); err != nil {
		t.Fatalf("releasing: %v", err)
	}

	next, err := guard.Acquire(ctx, attachKey("build"), false, time.Second, 10*time.Millisecond)
	if err != nil {
		t.Fatalf("acquiring after release: %v", err)
	}
	_ = next.Release()
}

// The flock, not the pidfile's content, is the exclusivity mechanism. A file
// left behind by a holder that crashed between writing it and exiting is
// reclaimed silently rather than blocking the slot forever.
func TestAStalePidfileIsReclaimedSilently(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "guard")
	guard, err := engine.NewAttachGuard(dir)
	if err != nil {
		t.Fatalf("NewAttachGuard: %v", err)
	}
	ctx := context.Background()

	// A first holder writes the file and goes away.
	first, err := guard.Acquire(ctx, attachKey("build"), false, time.Second, 10*time.Millisecond)
	if err != nil {
		t.Fatalf("acquiring: %v", err)
	}
	_ = first.Release()

	// The file is still there, naming a pid that no longer holds anything.
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) == 0 {
		t.Fatalf("expected the pidfile to remain: %v", err)
	}

	second, err := guard.Acquire(ctx, attachKey("build"), false, time.Second, 10*time.Millisecond)
	if err != nil {
		t.Fatalf("a stale pidfile blocked the slot: %v", err)
	}
	_ = second.Release()
}

// Different sessions never contend for one another's terminal.
func TestDifferentSessionsHaveDifferentAttachSlots(t *testing.T) {
	guard := newGuard(t)
	ctx := context.Background()

	first, err := guard.Acquire(ctx, attachKey("build"), false, time.Second, 10*time.Millisecond)
	if err != nil {
		t.Fatalf("acquiring: %v", err)
	}
	defer first.Release()

	second, err := guard.Acquire(ctx, attachKey("deploy"), false, 200*time.Millisecond, 10*time.Millisecond)
	if err != nil {
		t.Fatalf("a different session contended for the slot: %v", err)
	}
	_ = second.Release()
}

// Stealing from a holder that never releases must not block forever: it gives
// up with the same conflict shape, so the caller sees one failure mode rather
// than a hang.
func TestStealingFromAHungHolderGivesUpAsAConflict(t *testing.T) {
	guard := newGuard(t)
	ctx := context.Background()

	held, err := guard.Acquire(ctx, attachKey("build"), false, time.Second, 10*time.Millisecond)
	if err != nil {
		t.Fatalf("acquiring: %v", err)
	}
	defer held.Release()

	// The holder here is this very process, which has no handler installed for
	// the steal signal in this test, so nothing releases.
	_, err = guard.Acquire(ctx, attachKey("build"), true, 150*time.Millisecond, 20*time.Millisecond)
	if !errors.Is(err, backend.ErrConflict) {
		t.Errorf("stealing from a hung holder got %v, want a conflict", err)
	}
}

// A steal succeeds once the holder lets go, which is what the signal is for.
func TestStealingSucceedsWhenTheHolderReleases(t *testing.T) {
	guard := newGuard(t)
	ctx := context.Background()

	held, err := guard.Acquire(ctx, attachKey("build"), false, time.Second, 10*time.Millisecond)
	if err != nil {
		t.Fatalf("acquiring: %v", err)
	}

	var once sync.Once
	go func() {
		time.Sleep(80 * time.Millisecond)
		once.Do(func() { _ = held.Release() })
	}()

	stolen, err := guard.Acquire(ctx, attachKey("build"), true, 3*time.Second, 20*time.Millisecond)
	if err != nil {
		t.Fatalf("stealing from a holder that released: %v", err)
	}
	_ = stolen.Release()
	once.Do(func() { _ = held.Release() })
}

// The guard is a SEPARATE mechanism from the write lock. Reusing the write lock
// would let a caller waiting to type displace someone's live terminal, which is
// a different contention problem entirely.
func TestTheAttachGuardDoesNotContendWithTheWriteLock(t *testing.T) {
	dir := t.TempDir()
	guard, err := engine.NewAttachGuard(filepath.Join(dir, "guard"))
	if err != nil {
		t.Fatalf("NewAttachGuard: %v", err)
	}
	locks, err := engine.NewLocksIn(filepath.Join(dir, "locks"))
	if err != nil {
		t.Fatalf("NewLocksIn: %v", err)
	}
	ctx := context.Background()

	attached, err := guard.Acquire(ctx, attachKey("build"), false, time.Second, 10*time.Millisecond)
	if err != nil {
		t.Fatalf("acquiring the attach slot: %v", err)
	}
	defer attached.Release()

	writing, err := locks.Acquire(ctx, attachKey("build"), 200*time.Millisecond)
	if err != nil {
		t.Fatalf("an attached session blocked a writer: %v", err)
	}
	_ = writing.Release()
}

// discard opens the null device, rather than fabricating a File around fd 0.
//
// `os.NewFile(0, os.DevNull)` does not open anything: it wraps the descriptor
// it is GIVEN and merely NAMES it after the null device. Two consequences, and
// the second is the one that cost a red CI run.
//
// The name is a lie — the File points at whatever fd 0 happens to be — which
// already broke a test elsewhere in this repository (see attach_resize_test.go).
//
// Worse, os.NewFile arms a finalizer, so the collector CLOSES FD 0 once the
// File goes unreachable. The descriptor is then free, and the next os.Pipe —
// the one os/exec makes whenever Stdout is not a File, which is every captured
// backend call — is handed it. A second finalizer, or any later close, then
// pulls that descriptor out from under a live reader. Measured: on Linux the
// copy fails with `read |0: bad file descriptor`, which is exactly how this
// arrived, as an unrelated zmx call failing on a docs-only commit. On macOS it
// is not an error at all but a fatal `kevent on fd 0 failed`.
//
// So: open it, and close it when the test is done.
func discard(t *testing.T) *os.File {
	t.Helper()
	f, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		t.Fatalf("opening %s: %v", os.DevNull, err)
	}
	t.Cleanup(func() { _ = f.Close() })
	return f
}

// The engine hands back the CLIENT's exit code: once the presence gate has
// passed it hands off, so the status belongs to whatever it ran.
func TestAttachReturnsTheClientsOwnExitCode(t *testing.T) {
	attachment := backend.Attachment{Cmd: exec.Command("sh", "-c", "exit 6")}

	code, err := engine.Attach(context.Background(), attachment,
		engine.AttachIO{Out: discard(t)}, backend.AttachSpec{Role: backend.RoleController}, nil)
	if err != nil {
		t.Fatalf("Attach: %v", err)
	}
	if code != 6 {
		t.Errorf("exit code %d, want the client's own 6", code)
	}
}

// §8.8: a client that exits on its own must still reap whatever the backend
// created for the attach. Cleanup that only runs on the tidy path leaks forever.
func TestASpontaneousExitStillReaps(t *testing.T) {
	reaped := false
	attachment := backend.Attachment{
		Cmd:     exec.Command("sh", "-c", "exit 0"),
		Cleanup: func() error { reaped = true; return nil },
	}

	if _, err := engine.Attach(context.Background(), attachment,
		engine.AttachIO{Out: discard(t)}, backend.AttachSpec{Role: backend.RoleController}, nil); err != nil {
		t.Fatalf("Attach: %v", err)
	}
	if !reaped {
		t.Error("the attach exited without reaping what the backend created for it")
	}
}

// §8.10 The target watch closes on the first absent answer, and on nothing
// else: an error answer is a server that could not be asked, not a target
// that is gone, and is skipped.
func TestWatchTargetClosesOnAbsentAndSkipsErrors(t *testing.T) {
	answers := []backend.State{backend.StatePresent, backend.StateError, backend.StateError, backend.StateAbsent}
	var mu sync.Mutex
	asked := 0
	probe := func(context.Context) backend.State {
		mu.Lock()
		defer mu.Unlock()
		if asked < len(answers) {
			asked++
			return answers[asked-1]
		}
		return backend.StateAbsent
	}

	gone := engine.WatchTarget(context.Background(), probe, time.Millisecond)
	select {
	case <-gone:
	case <-time.After(5 * time.Second):
		t.Fatal("the watch never closed on an absent target")
	}
	mu.Lock()
	defer mu.Unlock()
	if asked != len(answers) {
		t.Errorf("the watch closed after %d answers, want %d: an error answer must not count as gone", asked, len(answers))
	}
}

// §8.10 A watch whose context ends stops without closing: a cancelled attach
// is not a target that went away.
func TestWatchTargetStopsWithoutClosingWhenCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	gone := engine.WatchTarget(ctx, func(context.Context) backend.State { return backend.StatePresent }, time.Millisecond)
	cancel()
	select {
	case <-gone:
		t.Fatal("a cancelled watch closed as if the target were gone")
	case <-time.After(50 * time.Millisecond):
	}
}

// §8.10 An attach whose Probe answers absent ends: the client is terminated,
// the attach returns 0 with a notice, and the process is gone.
func TestAttachEndsWhenTheTargetIsGone(t *testing.T) {
	var mu sync.Mutex
	present := true
	attachment := backend.Attachment{
		Cmd: exec.Command("sleep", "60"),
		Probe: func(context.Context) backend.State {
			mu.Lock()
			defer mu.Unlock()
			if present {
				return backend.StatePresent
			}
			return backend.StateAbsent
		},
	}
	errOut := &strings.Builder{}

	type result struct {
		code int
		err  error
	}
	results := make(chan result, 1)
	go func() {
		code, err := engine.Attach(context.Background(), attachment,
			engine.AttachIO{Out: discard(t), Err: errOut}, backend.AttachSpec{Role: backend.RoleController}, nil)
		results <- result{code, err}
	}()

	// Still attached while the target is present.
	select {
	case r := <-results:
		t.Fatalf("the attach ended while the target was present: %d, %v", r.code, r.err)
	case <-time.After(3 * engine.TargetPollInterval):
	}
	mu.Lock()
	present = false
	mu.Unlock()

	var r result
	select {
	case r = <-results:
	case <-time.After(10 * time.Second):
		t.Fatal("the attach did not end within 10s of the target going absent")
	}
	if r.err != nil || r.code != 0 {
		t.Errorf("an attach ended by its target exits %d, %v; want 0 with no error", r.code, r.err)
	}
	if !strings.Contains(errOut.String(), "detached: the target is gone") {
		t.Errorf("no notice about the target on the narration channel: %q", errOut.String())
	}
	if attachment.Cmd.ProcessState == nil || !attachment.Cmd.ProcessState.Exited() && attachment.Cmd.ProcessState.String() == "" {
		t.Errorf("the client was not reaped: %v", attachment.Cmd.ProcessState)
	}
	if err := attachment.Cmd.Process.Signal(syscall.Signal(0)); err == nil {
		t.Error("the client process is still alive after the attach ended")
	}
}
