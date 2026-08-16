package engine_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/husniadil/olympus/backend"
	"github.com/husniadil/olympus/internal/engine"
)

func delivery(t *testing.T, f *fakeBackend, locks *engine.Locks) engine.Delivery {
	t.Helper()
	return engine.Delivery{
		Backend:  f,
		Locks:    locks,
		Key:      key("build"),
		LockWait: time.Second,
		Budget:   60 * time.Millisecond,
		Poll:     5 * time.Millisecond,
	}
}

func TestTextObservedOnScreenIsSubmittedOnce(t *testing.T) {
	f := &fakeBackend{
		// The pane echoes what was typed, which is what verification looks for.
		onType: func(f *fakeBackend, text string) { f.setScreen("$ " + text) },
	}
	if err := delivery(t, f, nil).VerifiedSubmit(context.Background(), "build", "make build"); err != nil {
		t.Fatalf("VerifiedSubmit: %v", err)
	}
	typed, submits := f.counts()
	if typed != 1 {
		t.Errorf("sent the text %d times, want 1", typed)
	}
	if submits != 1 {
		t.Errorf("submitted %d times, want 1", submits)
	}
}

// The failure guarded is a dropped or coalesced FIRST delivery. The same text
// is resent, and the second window is what decides.
// §4.4: a failed Enter after injection MUST be retried exactly once. Text left
// sitting in the input line is not a visible failure — the NEXT injection
// concatenates onto it, corrupting both.
func TestADroppedFirstDeliveryIsResentOnce(t *testing.T) {
	f := &fakeBackend{}
	f.onType = func(f *fakeBackend, text string) {
		// The first send vanishes, as a terminal under load can drop it.
		if typed, _ := f.counts(); typed >= 2 {
			f.setScreen("$ " + text)
		}
	}

	if err := delivery(t, f, nil).VerifiedSubmit(context.Background(), "build", "make build"); err != nil {
		t.Fatalf("VerifiedSubmit: %v", err)
	}
	typed, submits := f.counts()
	if typed != 2 {
		t.Errorf("sent the text %d times, want 2", typed)
	}
	if submits != 1 {
		t.Errorf("submitted %d times, want 1", submits)
	}
}

// Nothing is ever submitted that was not verified. Submitting unverified text
// is precisely the corruption the whole mechanism exists to prevent.
func TestTextNeverObservedIsNeverSubmitted(t *testing.T) {
	f := &fakeBackend{}
	err := delivery(t, f, nil).VerifiedSubmit(context.Background(), "build", "make build")

	if !errors.Is(err, backend.ErrTimeout) {
		t.Fatalf("error is %v, want a timeout", err)
	}
	typed, submits := f.counts()
	if typed != 2 {
		t.Errorf("sent the text %d times, want 2 — one send and one resend", typed)
	}
	if submits != 0 {
		t.Errorf("submitted %d times after never observing the text, want 0", submits)
	}
}

// §7.4 requires this elapsed time to be asserted, so a future change cannot
// silently return early on the first miss and quietly drop the resend.
func TestFailingTakesBothBudgets(t *testing.T) {
	f := &fakeBackend{}
	d := delivery(t, f, nil)

	started := time.Now()
	if err := d.VerifiedSubmit(context.Background(), "build", "make build"); err == nil {
		t.Fatal("the delivery succeeded with nothing on screen")
	}
	elapsed := time.Since(started)

	if elapsed < 2*d.Budget {
		t.Errorf("failed after %s, want at least twice the %s budget — the resend window was skipped", elapsed, d.Budget)
	}
}

// §11.2: the lock spans send, verify AND submit. Releasing between the
// verification and the terminator would let a competing writer clear the input
// line, so the terminator submits something other than what was verified.
func TestTheLockIsHeldThroughTheSubmit(t *testing.T) {
	locks := newLocks(t)
	ctx := context.Background()

	f := &fakeBackend{}
	f.onType = func(f *fakeBackend, text string) {
		f.setScreen("$ " + text)
	}
	// The moment verification has succeeded, a competing writer tries to take
	// the session. It must be refused: at this instant the terminator has not
	// been sent yet.
	//
	// Recorded once rather than signalled on a channel, since the poll loop
	// calls this repeatedly and a channel would be drained before the
	// assertion could read it.
	var once sync.Once
	var ran bool
	var attemptErr error
	f.onScreen = func(f *fakeBackend) {
		once.Do(func() {
			_, err := locks.Acquire(ctx, key("build"), 10*time.Millisecond)
			ran, attemptErr = true, err
		})
	}

	if err := delivery(t, f, locks).VerifiedSubmit(ctx, "build", "make build"); err != nil {
		t.Fatalf("VerifiedSubmit: %v", err)
	}

	if !ran {
		t.Fatal("the competing writer never ran, so this case proved nothing")
	}
	if !errors.Is(attemptErr, backend.ErrConflict) {
		t.Errorf("a competing writer got %v while the delivery was mid-flight, want a conflict", attemptErr)
	}
}

// A send failure is the caller's to see immediately: there is nothing on screen
// to verify and no point burning the budget.
func TestASendFailureSurfacesWithoutWaiting(t *testing.T) {
	boom := backend.Errorf(backend.CodeSessionNotFound, "no session build")
	f := &fakeBackend{typeErr: boom}

	started := time.Now()
	err := delivery(t, f, nil).VerifiedSubmit(context.Background(), "build", "make build")
	if !errors.Is(err, backend.ErrNotFound) {
		t.Errorf("error is %v, want the send's own failure", err)
	}
	if elapsed := time.Since(started); elapsed > 40*time.Millisecond {
		t.Errorf("a failed send took %s, so it waited on the verification budget", elapsed)
	}
}

// A transient capture failure costs one poll, not the whole attempt: the budget
// is what bounds this, and giving up on the first read error would fail a
// delivery that was about to succeed.
func TestATransientCaptureFailureDoesNotAbortTheAttempt(t *testing.T) {
	f := &fakeBackend{screenErr: errors.New("busy")}
	f.onType = func(f *fakeBackend, text string) { f.setScreen("$ " + text) }
	reads := 0
	f.onScreen = func(f *fakeBackend) {
		reads++
		if reads >= 3 {
			f.mu.Lock()
			f.screenErr = nil
			f.mu.Unlock()
		}
	}

	if err := delivery(t, f, nil).VerifiedSubmit(context.Background(), "build", "make build"); err != nil {
		t.Fatalf("a transient capture failure aborted the delivery: %v", err)
	}
	if typed, _ := f.counts(); typed != 1 {
		t.Errorf("sent the text %d times, want 1 — the attempt should have recovered without resending", typed)
	}
}
