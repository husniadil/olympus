package engine_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/husniadil/olympus/backend"
	"github.com/husniadil/olympus/internal/engine"
)

func runner(f *fakeBackend, locks *engine.Locks) engine.Runner {
	return engine.Runner{
		Backend:  f,
		Locks:    locks,
		Key:      key("build"),
		LockWait: time.Second,
		Timeout:  400 * time.Millisecond,
		Poll:     10 * time.Millisecond,
	}
}

// completes makes the pane behave like a shell that ran the injected line: it
// echoes the line, then emits the start marker, the output, and the completion.
func completes(output string, code int) func(*fakeBackend, string) {
	return func(f *fakeBackend, line string) {
		id := idFromLine(line)
		f.setScreen(line + "\nOLY_S_" + id + "\n" + output + "\nOLY_D_" + id + "_" + itoa(code) + "_\n")
	}
}

func idFromLine(line string) string {
	const prefix = "OLY_S_"
	at := strings.Index(line, prefix)
	if at < 0 {
		return ""
	}
	rest := line[at+len(prefix):]
	end := strings.IndexAny(rest, ";  \t")
	if end < 0 {
		return rest
	}
	return rest[:end]
}

func TestExecReturnsTheOutputAndExitCode(t *testing.T) {
	f := &fakeBackend{onType: completes("built ok", 0)}
	got, err := runner(f, nil).Exec(context.Background(), "build", "make build")
	if err != nil {
		t.Fatalf("Exec: %v", err)
	}
	if got.Output != "built ok" {
		t.Errorf("output %q, want %q", got.Output, "built ok")
	}
	if got.ExitCode != 0 {
		t.Errorf("exit code %d, want 0", got.ExitCode)
	}
}

// A failing command is a normal result, not an infrastructure failure: the
// protocol worked, and what it reports is the command's own status.
func TestAFailingCommandIsNotAnError(t *testing.T) {
	f := &fakeBackend{onType: completes("boom", 7)}
	got, err := runner(f, nil).Exec(context.Background(), "build", "make build")
	if err != nil {
		t.Fatalf("a non-zero exit was reported as an error: %v", err)
	}
	if got.ExitCode != 7 {
		t.Errorf("exit code %d, want 7", got.ExitCode)
	}
}

// §6.3: both degradations are silent, so the check has to happen before any
// pane interaction — a rejected command must not have typed anything.
func TestADegradingCommandIsRejectedBeforeAnyInjection(t *testing.T) {
	f := &fakeBackend{}
	_, err := runner(f, nil).Exec(context.Background(), "build", "make build\nrm -rf /")
	if !errors.Is(err, backend.ErrUsage) {
		t.Fatalf("error is %v, want a usage error", err)
	}
	if typed, submits := f.counts(); typed != 0 || submits != 0 {
		t.Errorf("the pane was touched %d times before the command was rejected", typed+submits)
	}
}

func TestACommandThatNeverCompletesTimesOut(t *testing.T) {
	f := &fakeBackend{onType: func(f *fakeBackend, line string) {
		// The line echoes and the run starts, but nothing ever completes.
		f.setScreen(line + "\nOLY_S_" + idFromLine(line) + "\nstill working\n")
	}}
	_, err := runner(f, nil).Exec(context.Background(), "build", "sleep 100")
	if !errors.Is(err, backend.ErrTimeout) {
		t.Errorf("error is %v, want a timeout", err)
	}
}

// §11.2: a run releases the lock BEFORE polling. Holding it across the whole
// wait would block every other writer against the session for the full timeout,
// for no benefit — the polling phase only reads.
func TestTheLockIsReleasedBeforePolling(t *testing.T) {
	locks := newLocks(t)
	f := &fakeBackend{onType: func(f *fakeBackend, line string) {
		f.setScreen(line + "\nOLY_S_" + idFromLine(line) + "\nworking\n")
	}}

	// Recorded once, not signalled on a channel: the poll loop calls this many
	// times, and a channel would be drained by a later call before the
	// assertion could read it.
	var once sync.Once
	var ran bool
	var attemptErr error
	f.onScreen = func(f *fakeBackend) {
		once.Do(func() {
			// Mid-poll, a competing writer must be able to take the session.
			lock, err := locks.Acquire(context.Background(), key("build"), 50*time.Millisecond)
			if err == nil {
				_ = lock.Release()
			}
			ran, attemptErr = true, err
		})
	}

	_, _ = runner(f, locks).Exec(context.Background(), "build", "sleep 100")

	if !ran {
		t.Fatal("the competing writer never ran, so this case proved nothing")
	}
	if attemptErr != nil {
		t.Errorf("a competing writer was blocked during the poll phase: %v", attemptErr)
	}
}

func TestStartReturnsAnIDWithoutWaiting(t *testing.T) {
	f := &fakeBackend{}
	id, err := runner(f, nil).Start(context.Background(), "build", "make deploy")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if id == "" {
		t.Fatal("Start returned no id")
	}
	if typed, submits := f.counts(); typed != 1 || submits != 1 {
		t.Errorf("injected %d times and submitted %d, want 1 and 1", typed, submits)
	}
}

func TestPollReportsCompletionWithItsExitCode(t *testing.T) {
	f := &fakeBackend{onType: completes("deployed", 0), state: backend.StatePresent}
	r := runner(f, nil)

	id, err := r.Start(context.Background(), "build", "make deploy")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	got, err := r.PollRun(context.Background(), "build", id)
	if err != nil {
		t.Fatalf("PollRun: %v", err)
	}
	if got.Status != engine.PollCompleted {
		t.Fatalf("status %q, want %q", got.Status, engine.PollCompleted)
	}
	if got.ExitCode == nil || *got.ExitCode != 0 {
		t.Errorf("exit code %v, want 0", got.ExitCode)
	}
	if got.Output != "deployed" {
		t.Errorf("output %q, want %q", got.Output, "deployed")
	}
}

// The exit code is a pointer so pending and died can leave it unset. A fake
// zero would read as success to a consumer that forgot to branch on status.
func TestPendingAndDiedCarryNoExitCode(t *testing.T) {
	f := &fakeBackend{state: backend.StatePresent}
	f.sessions = []backend.Session{{Name: "build", Liveness: backend.LivenessPresent}}
	f.onType = func(f *fakeBackend, line string) {
		f.setScreen(line + "\nOLY_S_" + idFromLine(line) + "\nworking\n")
	}
	r := runner(f, nil)

	id, err := r.Start(context.Background(), "build", "sleep 100")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	pending, err := r.PollRun(context.Background(), "build", id)
	if err != nil {
		t.Fatalf("PollRun: %v", err)
	}
	if pending.Status != engine.PollPending {
		t.Fatalf("status %q, want %q", pending.Status, engine.PollPending)
	}
	if pending.ExitCode != nil {
		t.Errorf("a pending run carries exit code %d, want none", *pending.ExitCode)
	}

	// The session goes away underneath the run.
	f.sessions = nil
	died, err := r.PollRun(context.Background(), "build", id)
	if err != nil {
		t.Fatalf("PollRun: %v", err)
	}
	if died.Status != engine.PollDied {
		t.Errorf("status %q, want %q", died.Status, engine.PollDied)
	}
	if died.ExitCode != nil {
		t.Errorf("a died run carries exit code %d, want none", *died.ExitCode)
	}
}

// §6.8: poll answers about the command, never about the backend. A target that
// never existed is indistinguishable from one that vanished, from a read-only
// vantage point, so both collapse to died rather than to not-found.
func TestPollingATargetThatNeverExistedIsDiedNotNotFound(t *testing.T) {
	f := &fakeBackend{}
	got, err := runner(f, nil).PollRun(context.Background(), "never-existed", "someid")
	if err != nil {
		t.Fatalf("polling a target that never existed failed: %v", err)
	}
	if got.Status != engine.PollDied {
		t.Errorf("status %q, want %q", got.Status, engine.PollDied)
	}
}

// §6.9: a corpse keeps the session listed, so session-level death detection
// alone reports pending forever even though the command has died.
func TestACorpsePaneIsDiedNotPending(t *testing.T) {
	f := &fakeBackend{
		sessions: []backend.Session{{Name: "build", Dead: true, Liveness: backend.LivenessPresent}},
	}
	got, err := runner(f, nil).PollRun(context.Background(), "build", "someid")
	if err != nil {
		t.Fatalf("PollRun: %v", err)
	}
	if got.Status != engine.PollDied {
		t.Errorf("status %q for a listed session whose pane is dead, want %q", got.Status, engine.PollDied)
	}
}

// §6.7: an unknown id and a still-pending command are indistinguishable, and
// that is deliberate — distinguishing them would need persistent state. It is
// the caller's job to bound how long it waits.
func TestAnUnknownIDReadsAsPending(t *testing.T) {
	f := &fakeBackend{sessions: []backend.Session{{Name: "build", Liveness: backend.LivenessPresent}}}
	got, err := runner(f, nil).PollRun(context.Background(), "build", "an-id-nobody-issued")
	if err != nil {
		t.Fatalf("PollRun: %v", err)
	}
	if got.Status != engine.PollPending {
		t.Errorf("status %q for an unknown id, want %q", got.Status, engine.PollPending)
	}
}

// §11.1: polling must not take the lock. A read that blocks on a writer turns
// observation into contention, and observing a busy session is the case that
// matters most.
func TestPollingDoesNotTakeTheLock(t *testing.T) {
	locks := newLocks(t)
	held, err := locks.Acquire(context.Background(), key("build"), time.Second)
	if err != nil {
		t.Fatalf("acquiring: %v", err)
	}
	defer held.Release()

	f := &fakeBackend{sessions: []backend.Session{{Name: "build", Liveness: backend.LivenessPresent}}}
	done := make(chan struct{})
	go func() {
		defer close(done)
		if _, err := runner(f, locks).PollRun(context.Background(), "build", "someid"); err != nil {
			t.Errorf("PollRun: %v", err)
		}
	}()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Error("polling blocked on the write lock")
	}
}

// §6.4: the window grows on a backend that needs an explicit depth, and is not
// requested at all on one that returns its own scrollback.
func TestTheCaptureWindowIsOnlyRequestedWhereItMeansSomething(t *testing.T) {
	// §6.4: on a backend that returns its own scrollback, the window is ignored
	// — so asking for one is a request the backend will not honour, and sending
	// it anyway hides that the depth is really the backend's to govern.
	//
	// This case asserted only that Exec returned no error, on either backend.
	// It could not have failed: the fake discarded the ScreenOpts, so the
	// property in the name was not observable. Found by looking for tests whose
	// only assertion is an error check.
	native := &fakeBackend{caps: backend.Capabilities{NativeScrollback: true}, onType: completes("ok", 0)}
	if _, err := runner(native, nil).Exec(context.Background(), "build", "cmd"); err != nil {
		t.Fatalf("Exec: %v", err)
	}
	if len(native.screenOpts) == 0 {
		t.Fatal("nothing was captured, so the request cannot be judged")
	}
	for i, opts := range native.screenOpts {
		if opts.HistoryLines != 0 {
			t.Errorf("capture %d asked a native-scrollback backend for %d history lines, want none",
				i, opts.HistoryLines)
		}
	}

	windowed := &fakeBackend{caps: backend.Capabilities{NativeScrollback: false}, onType: completes("ok", 0)}
	if _, err := runner(windowed, nil).Exec(context.Background(), "build", "cmd"); err != nil {
		t.Fatalf("Exec: %v", err)
	}
	if len(windowed.screenOpts) == 0 {
		t.Fatal("nothing was captured, so the request cannot be judged")
	}
	for i, opts := range windowed.screenOpts {
		if opts.HistoryLines <= 0 {
			t.Errorf("capture %d asked a windowed backend for %d history lines, want a window",
				i, opts.HistoryLines)
		}
	}
}

// §4.4: injecting a run's sentinel line and pressing Enter is a composed
// inject-then-submit, so the terminator is retried exactly once. A dropped
// Enter used to fail the start outright and leave the sentinel line — markers
// and all — sitting in the input line for the next injection to concatenate
// onto.
func TestAnInjectedRunRetriesADroppedTerminatorOnce(t *testing.T) {
	f := &fakeBackend{submitFailures: 1}

	if _, err := runner(f, nil).Start(context.Background(), "build", "make build"); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if _, submits := f.counts(); submits != 1 {
		t.Errorf("the terminator landed %d times, want 1 — the retry did not run", submits)
	}
}

// §6.4: a run that times out with no start marker on its deepest capture has
// hit something a plain timeout cannot express, and the caller has no way to
// tell it from a slow command. The message reports what was OBSERVED and names
// both causes: measured on herdr, output scrolling past the server's read cap
// and a full-screen program covering the screen produce the same capture, and
// that backend does not track the alternate screen, so nothing here can tell
// them apart. Naming one would be a guess presented as a diagnosis.
func TestATimeoutSaysWhenTheStartMarkerIsNotOnTheDeepestCapture(t *testing.T) {
	f := &fakeBackend{onType: func(f *fakeBackend, line string) {
		// No markers at all: the command is still running and the start marker
		// is out of reach, so there is no completion to recover either.
		f.setScreen("line 998\nline 999\nline 1000\n")
	}}
	_, err := runner(f, nil).Exec(context.Background(), "build", "seq 1 100000")
	if !errors.Is(err, backend.ErrTimeout) {
		t.Fatalf("error is %v, want a timeout", err)
	}
	if !strings.Contains(err.Error(), "is not on the deepest screen this backend returns") {
		t.Errorf("timeout said %q, want it to report the missing start marker", err.Error())
	}
	if !strings.Contains(err.Error(), "full-screen program") {
		t.Errorf("timeout said %q, want it to name the cause it cannot rule out", err.Error())
	}
}

// The same timeout on a command that is merely slow must NOT claim the depth is
// at fault: its start marker is right there on screen. Diagnosing every timeout
// this way would be worse than diagnosing none.
func TestASlowCommandsTimeoutDoesNotBlameTheDepth(t *testing.T) {
	f := &fakeBackend{onType: func(f *fakeBackend, line string) {
		f.setScreen(line + "\nOLY_S_" + idFromLine(line) + "\nstill working\n")
	}}
	_, err := runner(f, nil).Exec(context.Background(), "build", "sleep 100")
	if !errors.Is(err, backend.ErrTimeout) {
		t.Fatalf("error is %v, want a timeout", err)
	}
	if strings.Contains(err.Error(), "is not on the deepest screen this backend returns") {
		t.Errorf("timeout said %q, but the start marker was on screen the whole time", err.Error())
	}
}

// §6.2 and §6.4: once the window has stopped growing, a completion whose start
// marker is out of reach is the best answer that will ever exist — waiting
// longer cannot bring it back, and the exit code is sitting on the capture. The
// run reports it, marked truncated, instead of timing out on an answer it can
// already read.
func TestARunTakesTheExitCodeItCanStillReadOnceTheWindowStopsGrowing(t *testing.T) {
	f := &fakeBackend{onType: func(f *fakeBackend, line string) {
		// The start marker and the command-line echo have both scrolled past
		// what this backend returns. The completion is still on screen.
		f.setScreen("line 998\nline 999\nOLY_D_" + idFromLine(line) + "_3_\n")
	}}
	got, err := runner(f, nil).Exec(context.Background(), "build", "seq 1 100000")
	if err != nil {
		t.Fatalf("Exec: %v", err)
	}
	if got.ExitCode != 3 {
		t.Errorf("exit code %d, want the 3 the completion carried", got.ExitCode)
	}
	if !got.Truncated {
		t.Error("the result was not marked truncated, so the caller cannot tell its output is partial")
	}
	// The relaxation waits for the window to stop growing. Taking it on the
	// first capture would apply it while a deeper look was still available,
	// which is exactly the case §6.2 refuses.
	if n := len(f.screenOpts); n < 4 {
		t.Errorf("the run relaxed after %d captures, want it to grow the window to its maximum first", n)
	}
	if last := f.screenOpts[len(f.screenOpts)-1].HistoryLines; last != 10000 {
		t.Errorf("the last capture asked for %d lines, want the maximum window", last)
	}
}

// A complete run is never marked truncated. The flag drives a disclosure, and a
// disclosure on an answer that lost nothing is noise.
func TestACompleteRunIsNotMarkedTruncated(t *testing.T) {
	f := &fakeBackend{onType: completes("built ok", 0)}
	got, err := runner(f, nil).Exec(context.Background(), "build", "make build")
	if err != nil {
		t.Fatalf("Exec: %v", err)
	}
	if got.Truncated {
		t.Error("a run that parsed both markers was marked truncated")
	}
}

// The detached path searches the maximum window on every poll, so the same
// relaxation applies from the first one: pending forever is the wrong answer
// when the exit code is readable.
func TestAPollTakesTheExitCodeItCanStillRead(t *testing.T) {
	f := &fakeBackend{screen: "line 998\nline 999\nOLY_D_abc123_5_\n"}
	f.sessions = []backend.Session{{Name: "build", Liveness: backend.LivenessPresent}}
	got, err := runner(f, nil).PollRun(context.Background(), "build", "abc123")
	if err != nil {
		t.Fatalf("PollRun: %v", err)
	}
	if got.Status != engine.PollCompleted {
		t.Fatalf("status %q, want completed", got.Status)
	}
	if got.ExitCode == nil || *got.ExitCode != 5 {
		t.Errorf("exit code %v, want 5", got.ExitCode)
	}
	if !got.Truncated {
		t.Error("the poll result was not marked truncated")
	}
}
