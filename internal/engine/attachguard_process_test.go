//go:build darwin || linux

package engine_test

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/husniadil/olympus/backend"
	"github.com/husniadil/olympus/internal/engine"
)

// §8.5, §11.3: the attach slot is arbitrated ACROSS processes, and every test
// until now held it within one.
//
// A single process proves the arithmetic and nothing about the mechanism: the
// flock is a kernel object whose whole point is that it is shared between
// unrelated processes and released when one dies, and neither half of that can
// be observed without a second process to die.
//
// The failure this guards is the worst one this file can have. If the pidfile's
// CONTENT were the exclusivity mechanism — which is what it looks like, and what
// a well-meaning simplification would make it — a holder killed abnormally would
// leave its pid behind and lock the operator out of their own session
// permanently, with no error naming the file they would have to delete.

const holderEnv = "OLYMPUS_TEST_ATTACH_HOLDER"

// TestMain lets the test binary re-exec itself as the holder. A holder has to be
// a real, separate process — a goroutine shares this process's flocks and would
// make the test pass by construction.
func TestMain(m *testing.M) {
	if dir := os.Getenv(holderEnv); dir != "" {
		holdTheSlotForever(dir)
		return
	}
	os.Exit(m.Run())
}

func holdTheSlotForever(dir string) {
	guard, err := engine.NewAttachGuard(dir)
	if err != nil {
		fmt.Println("guard:", err)
		os.Exit(1)
	}
	slot, err := guard.Acquire(context.Background(), holderKey(), false, time.Second, 10*time.Millisecond)
	if err != nil {
		fmt.Println("acquire:", err)
		os.Exit(1)
	}

	// A handler, because that is what a real attach client has (§8.6): it is
	// installed before the PTY is spawned, and releasing on it is the DESIGNED
	// path a steal takes.
	//
	// This holder used to install nothing and rely on SIGUSR1's POSIX default
	// disposition to terminate it. That works on macOS and in a plain container
	// and did not on the GitHub runner, where the steal case timed out at three
	// seconds and then at thirty. Chasing which of the two environments is
	// unusual would have been the wrong repair: §8.6 records default-disposition
	// termination as an ACCEPTED RISK, not a guarantee, so asserting it was
	// asserting something the spec deliberately does not promise.
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGUSR1)

	fmt.Println("held")
	_ = os.Stdout.Sync()

	<-signals
	_ = slot.Release()
	os.Exit(0)
}

func holderKey() engine.LockKey {
	return engine.LockKey{Backend: backend.Tmux, Scope: "attach-guard-test", Session: "build"}
}

// startHolder re-execs this test binary as a process that takes the slot and
// keeps it, and returns once the slot is confirmed held.
func startHolder(t *testing.T, dir string) *exec.Cmd {
	t.Helper()
	holder := exec.Command(os.Args[0])
	holder.Env = append(os.Environ(), holderEnv+"="+dir)
	out, err := holder.StdoutPipe()
	if err != nil {
		t.Fatalf("piping the holder's output: %v", err)
	}
	if err := holder.Start(); err != nil {
		t.Fatalf("starting the holder: %v", err)
	}
	t.Cleanup(func() {
		_ = holder.Process.Kill()
		_, _ = holder.Process.Wait()
	})

	buffer := make([]byte, 64)
	n, _ := out.Read(buffer)
	if !strings.Contains(string(buffer[:n]), "held") {
		t.Fatalf("the holder did not take the slot: %q", buffer[:n])
	}
	return holder
}

func TestAnotherProcessHoldingTheSlotIsAConflict(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "guard")
	startHolder(t, dir)

	guard, err := engine.NewAttachGuard(dir)
	if err != nil {
		t.Fatalf("NewAttachGuard: %v", err)
	}
	_, err = guard.Acquire(context.Background(), holderKey(), false, time.Second, 10*time.Millisecond)
	if err == nil {
		t.Fatal("the slot was acquired while another process held it")
	}
	if got := backend.CodeOf(err); got != backend.CodeConflict {
		t.Errorf("a held slot is %q, want %q", got, backend.CodeConflict)
	}
	// §8.5: the conflict names the holder, because the operator's next question
	// is which terminal has it — and the pid is the only answer available from
	// outside that process.
	if !strings.Contains(err.Error(), "pid") {
		t.Errorf("the conflict %q does not name the holding pid", err.Error())
	}
}

// The headline case: a holder killed with SIGKILL gets no chance to clean up.
// Its pidfile survives on disk, and must not matter.
func TestAKilledHoldersStalePidfileDoesNotBlockTheSlot(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "guard")
	holder := startHolder(t, dir)

	if err := holder.Process.Signal(syscall.SIGKILL); err != nil {
		t.Fatalf("killing the holder: %v", err)
	}
	if _, err := holder.Process.Wait(); err != nil {
		t.Fatalf("reaping the holder: %v", err)
	}

	// Stated as a precondition rather than assumed: if the file were gone, this
	// test would be proving something much weaker than it claims.
	files, _ := filepath.Glob(filepath.Join(dir, "*.pid"))
	if len(files) == 0 {
		t.Fatal("the killed holder left no pidfile, so a stale one is not what is being tested")
	}

	guard, err := engine.NewAttachGuard(dir)
	if err != nil {
		t.Fatalf("NewAttachGuard: %v", err)
	}
	// Crucially WITHOUT steal. Needing to steal from a process that no longer
	// exists would mean every crash left the operator a conflict to resolve by
	// hand, and stealing signals a pid that the OS may since have recycled onto
	// something else entirely (§8.6).
	slot, err := guard.Acquire(context.Background(), holderKey(), false, time.Second, 10*time.Millisecond)
	if err != nil {
		t.Fatalf("a stale pidfile blocked the slot: %v", err)
	}
	if err := slot.Release(); err != nil {
		t.Errorf("releasing: %v", err)
	}
}

// Stealing is what a live holder requires, and it must actually displace one:
// the holder is signalled, and the slot is free once it goes.
func TestStealingDisplacesALiveHolder(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "guard")
	startHolder(t, dir)

	guard, err := engine.NewAttachGuard(dir)
	if err != nil {
		t.Fatalf("NewAttachGuard: %v", err)
	}
	// The holder handles SIGUSR1 and releases, which is what a real attach
	// client does. What is asserted is that stealing displaces a live holder by
	// the designed route — not that an unhandled signal happens to kill one,
	// which §8.6 keeps as an accepted risk precisely because it is not
	// dependable.
	// A longer wait than the product default on purpose. What is under test is
	// that stealing DISPLACES a holder, not whether three seconds happens to be
	// enough on the machine running the test — and on a loaded CI runner it is
	// not: the first real CI run failed here with "did not release within 3s".
	// Keeping the default would make this case a load meter.
	slot, err := guard.Acquire(context.Background(), holderKey(), true,
		30*time.Second, engine.DefaultStealPoll)
	if err != nil {
		t.Fatalf("stealing from a live holder: %v", err)
	}
	if err := slot.Release(); err != nil {
		t.Errorf("releasing: %v", err)
	}
}

// Two different sessions are two different slots. A guard that keyed on the
// directory alone would serialize every attach on the machine.
func TestTheSlotIsPerSessionAcrossProcesses(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "guard")
	startHolder(t, dir)

	guard, err := engine.NewAttachGuard(dir)
	if err != nil {
		t.Fatalf("NewAttachGuard: %v", err)
	}
	other := holderKey()
	other.Session = "other"
	slot, err := guard.Acquire(context.Background(), other, false, time.Second, 10*time.Millisecond)
	if err != nil {
		t.Fatalf("a different session's slot was blocked: %v", err)
	}
	if err := slot.Release(); err != nil {
		t.Errorf("releasing: %v", err)
	}
}
