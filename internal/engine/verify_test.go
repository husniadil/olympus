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

// §7.1: an input box that scrolls shows the END of long text, where the
// cursor is, and its beginning has gone above the box's top edge. Looking for
// the head alone read that as a dropped delivery and typed the text a second
// time, leaving it doubled in the input line and unsubmitted (measured: a
// ~1,500-character prompt into a 52-column pane).
func TestLongTextWhoseStartScrolledOutOfTheInputIsObserved(t *testing.T) {
	text := "From the agent on this host, round two. " +
		strings.Repeat("Several sentences of context that wrap across many rows. ", 20) +
		"Pick one and say why."
	f := &fakeBackend{
		onType: func(f *fakeBackend, text string) {
			// A 52-column box that keeps its last four rows in view.
			var rows []string
			for i := 0; i < len(text); i += 52 {
				rows = append(rows, text[i:min(i+52, len(text))])
			}
			f.setScreen("> " + strings.Join(rows[len(rows)-4:], "\n  "))
		},
	}
	if err := delivery(t, f, nil).VerifiedSubmit(context.Background(), "build", text); err != nil {
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

// §4.4: a verified send is a composed inject-then-submit, so its terminator is
// retried exactly once. Before this, only PasteAndSubmit had the retry: a
// dropped Enter here failed the call and left the verified text sitting in the
// input line, where the next injection concatenates onto it.
func TestAVerifiedSendRetriesADroppedTerminatorOnce(t *testing.T) {
	f := &fakeBackend{
		submitFailures: 1,
		onType:         func(f *fakeBackend, text string) { f.setScreen("$ " + text) },
	}

	if err := delivery(t, f, nil).VerifiedSubmit(context.Background(), "build", "make build"); err != nil {
		t.Fatalf("VerifiedSubmit: %v", err)
	}
	if _, submits := f.counts(); submits != 1 {
		t.Errorf("the terminator landed %d times, want 1 — the retry did not run", submits)
	}
}

// Exactly once, not until it works. A terminator that keeps failing surfaces an
// error naming the text left behind, because a caller that does not learn about
// it will corrupt its next injection.
func TestATerminatorThatKeepsFailingIsSurfaced(t *testing.T) {
	f := &fakeBackend{
		submitFailures: 2,
		onType:         func(f *fakeBackend, text string) { f.setScreen("$ " + text) },
	}

	err := delivery(t, f, nil).VerifiedSubmit(context.Background(), "build", "make build")
	if !errors.Is(err, backend.ErrTimeout) {
		t.Fatalf("error is %v, want a timeout naming the unsubmitted text", err)
	}
	if !strings.Contains(err.Error(), "still sitting in the input line") {
		t.Errorf("error is %q, want it to say the text is still in the input line", err)
	}
	if f.submitFailures != 0 {
		t.Errorf("%d scripted failures were never reached, so the terminator was tried fewer than twice", f.submitFailures)
	}
}

// §7.5: an inspection that refuses is the refusal, and nothing reaches the
// pane. A prompt waiting on a person takes every keystroke typed at it, so a
// refusal that typed first and complained afterwards would already be an answer.
func TestAnInspectionThatRefusesTypesNothing(t *testing.T) {
	f := &fakeBackend{onType: func(f *fakeBackend, text string) { f.setScreen("$ " + text) }}
	d := delivery(t, f, nil)
	d.Inspect = func(context.Context, string) (engine.Watch, error) {
		return nil, backend.Errorf(backend.CodeAgentBlocked, "the agent in build is waiting on a person")
	}

	err := d.VerifiedSubmit(context.Background(), "build", "make build")
	if !errors.Is(err, backend.ErrBlocked) {
		t.Fatalf("error is %v, want the inspection's refusal", err)
	}
	if typed, submits := f.counts(); typed != 0 || submits != 0 {
		t.Errorf("typed %d and submitted %d after a refusal, want nothing", typed, submits)
	}
}

// §7.5: the inspection happens under the same lock as the delivery, so no
// other writer can change what it read between the reading and the typing.
func TestTheInspectionRunsInsideTheLock(t *testing.T) {
	locks := newLocks(t)
	ctx := context.Background()
	f := &fakeBackend{onType: func(f *fakeBackend, text string) { f.setScreen("$ " + text) }}

	var attemptErr error
	d := delivery(t, f, locks)
	d.Inspect = func(context.Context, string) (engine.Watch, error) {
		_, attemptErr = locks.Acquire(ctx, key("build"), 10*time.Millisecond)
		return nil, nil
	}
	if err := d.VerifiedSubmit(ctx, "build", "make build"); err != nil {
		t.Fatalf("VerifiedSubmit: %v", err)
	}
	if !errors.Is(attemptErr, backend.ErrConflict) {
		t.Errorf("a competing writer got %v during the inspection, want a conflict", attemptErr)
	}
}

// §7.6: a watch replaces the whole-screen match. The same text already on the
// screen from an earlier turn is not the echo of this one, and a watch that
// knows where input lands does not count it.
func TestAWatchDecidesWhatCountsAsObserved(t *testing.T) {
	inBox := func(screen, head, tail string) (bool, error) {
		for _, line := range strings.Split(screen, "\n") {
			if strings.HasPrefix(line, "> ") && engine.ScreenContains(line, head) {
				return true, nil
			}
		}
		return false, nil
	}

	t.Run("the echo in the input counts", func(t *testing.T) {
		f := &fakeBackend{screen: "earlier: make build"}
		f.onType = func(f *fakeBackend, text string) { f.setScreen("earlier: make build\n> " + text) }
		d := delivery(t, f, nil)
		d.Inspect = func(context.Context, string) (engine.Watch, error) { return inBox, nil }
		if err := d.VerifiedSubmit(context.Background(), "build", "make build"); err != nil {
			t.Fatalf("VerifiedSubmit: %v", err)
		}
		if _, submits := f.counts(); submits != 1 {
			t.Errorf("submitted %d times, want 1", submits)
		}
	})

	t.Run("the same text elsewhere does not", func(t *testing.T) {
		f := &fakeBackend{screen: "earlier: make build"}
		d := delivery(t, f, nil)
		d.Inspect = func(context.Context, string) (engine.Watch, error) { return inBox, nil }
		err := d.VerifiedSubmit(context.Background(), "build", "make build")
		if !errors.Is(err, backend.ErrTimeout) {
			t.Fatalf("error is %v, want a timeout", err)
		}
		if _, submits := f.counts(); submits != 0 {
			t.Errorf("submitted %d times on text the watch never saw in the input, want 0", submits)
		}
	})
}

// §7.5: a watch that sees the agent start waiting on a person mid-delivery
// stops there. A resend would type the text into the prompt a second time,
// and the terminator would answer it.
func TestAWatchThatRefusesStopsWithoutResendOrSubmit(t *testing.T) {
	f := &fakeBackend{onType: func(f *fakeBackend, text string) { f.setScreen("Do you want to proceed?") }}
	d := delivery(t, f, nil)
	d.Inspect = func(context.Context, string) (engine.Watch, error) {
		return func(screen, _, _ string) (bool, error) {
			if strings.Contains(screen, "proceed?") {
				return false, backend.Errorf(backend.CodeAgentBlocked, "the agent in build is waiting on a person")
			}
			return false, nil
		}, nil
	}

	started := time.Now()
	err := d.VerifiedSubmit(context.Background(), "build", "make build")
	if !errors.Is(err, backend.ErrBlocked) {
		t.Fatalf("error is %v, want the watch's refusal", err)
	}
	typed, submits := f.counts()
	if typed != 1 || submits != 0 {
		t.Errorf("typed %d and submitted %d, want one send and no terminator", typed, submits)
	}
	if elapsed := time.Since(started); elapsed >= d.Budget {
		t.Errorf("the refusal took %s, so it waited on the verification budget", elapsed)
	}
}
