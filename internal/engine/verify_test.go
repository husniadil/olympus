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

// §7.2: what confirms a delivery is the text the typing put on screen, not text
// that was there before anything was typed. A shell that already shows the same
// command, from the last time it ran, confirmed a delivery that never landed,
// and the terminator then submitted whatever the input line held.
func TestTextAlreadyOnScreenDoesNotConfirmADroppedDelivery(t *testing.T) {
	for _, c := range []struct{ name, text, screen string }{
		{"words", "make build", "$ make build\nbuilt ok\n$ "},
		{"punctuation", ";;", "$ ;;\n$ "},
	} {
		t.Run(c.name, func(t *testing.T) {
			// Every send vanishes, and the screen keeps its old copy.
			f := &fakeBackend{screen: c.screen}
			err := delivery(t, f, nil).VerifiedSubmit(context.Background(), "build", c.text)
			if !errors.Is(err, backend.ErrTimeout) {
				t.Fatalf("error is %v, want a timeout: the text already on screen is not this delivery's", err)
			}
			if _, submits := f.counts(); submits != 0 {
				t.Errorf("submitted %d times on an old copy of the text, want 0", submits)
			}

			// The same screen, and this time the typing lands beside it.
			f = &fakeBackend{screen: c.screen, onType: func(f *fakeBackend, text string) { f.setScreen(c.screen + text) }}
			if err := delivery(t, f, nil).VerifiedSubmit(context.Background(), "build", c.text); err != nil {
				t.Fatalf("a delivery beside an old copy was not observed: %v", err)
			}
			if typed, _ := f.counts(); typed != 1 {
				t.Errorf("sent the text %d times, want 1", typed)
			}
		})
	}
}

// §7.1: text with no letters or digits normalizes to nothing, and an empty
// needle matches any screen. Verification then checked nothing, so such text
// is looked for as typed instead.
func TestPunctuationOnlyTextIsStillVerified(t *testing.T) {
	f := &fakeBackend{screen: "$ "}
	err := delivery(t, f, nil).VerifiedSubmit(context.Background(), "build", ";;")
	if !errors.Is(err, backend.ErrTimeout) {
		t.Fatalf("error is %v, want a timeout: the text never reached the screen", err)
	}
	if _, submits := f.counts(); submits != 0 {
		t.Errorf("submitted %d times without seeing the text, want 0", submits)
	}

	f = &fakeBackend{onType: func(f *fakeBackend, text string) { f.setScreen("$ " + text) }}
	if err := delivery(t, f, nil).VerifiedSubmit(context.Background(), "build", ";;"); err != nil {
		t.Fatalf("punctuation that did reach the screen was not observed: %v", err)
	}
}

// §7.4: a full-screen program can act on keys without showing them, so text
// that went unseen there may have done its work, and a resend would do it
// twice. The delivery fails after one window instead.
func TestAPaneOnTheAlternateScreenIsNotSentTheTextTwice(t *testing.T) {
	f := &fakeBackend{meta: backend.ScreenMeta{AltScreen: true}}
	d := delivery(t, f, nil)

	started := time.Now()
	err := d.VerifiedSubmit(context.Background(), "build", "q")
	elapsed := time.Since(started)

	if !errors.Is(err, backend.ErrTimeout) {
		t.Fatalf("error is %v, want a timeout", err)
	}
	if !strings.Contains(err.Error(), "not sent again") {
		t.Errorf("error %q does not say the text was not sent again", err)
	}
	typed, submits := f.counts()
	if typed != 1 {
		t.Errorf("sent the text %d times, want 1", typed)
	}
	if submits != 0 {
		t.Errorf("submitted %d times after never observing the text, want 0", submits)
	}
	if elapsed >= 2*d.Budget {
		t.Errorf("failed after %s, want one %s window and no resend window", elapsed, d.Budget)
	}
}

// The flag is read before typing. A pager's q ends the pager, and a capture
// after it shows the shell underneath, which is exactly where a resend lands.
func TestTheAlternateScreenIsReadBeforeTheTextIsTyped(t *testing.T) {
	f := &fakeBackend{
		meta: backend.ScreenMeta{AltScreen: true},
		onType: func(f *fakeBackend, _ string) {
			f.mu.Lock()
			f.meta = backend.ScreenMeta{}
			f.mu.Unlock()
			f.setScreen("$ ")
		},
	}
	err := delivery(t, f, nil).Verified(context.Background(), "build", "q", false)

	if !errors.Is(err, backend.ErrTimeout) {
		t.Fatalf("error is %v, want a timeout", err)
	}
	if typed, _ := f.counts(); typed != 1 {
		t.Errorf("sent the text %d times, want 1: the resend went to the shell the pager left", typed)
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
// it will corrupt its next injection. It is marked typed (api §2), as every
// failure that leaves the text in the input line is.
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
	if !backend.TypedOf(err) {
		t.Errorf("error %v is not marked typed, though the text is still in the input line", err)
	}
	if f.submitFailures != 0 {
		t.Errorf("%d scripted failures were never reached, so the terminator was tried fewer than twice", f.submitFailures)
	}
}

// §4.4's exception: a terminator whose delivery is unknown is not retried. The
// backend accepted it and then lost the client carrying it, so it may already
// have submitted; a second Enter would then submit twice.
func TestATerminatorOfUnknownDeliveryIsNotRetried(t *testing.T) {
	lost := &backend.Error{Code: backend.CodeUnexpected, Msg: "client went away mid-command", Uncertain: true}
	f := &fakeBackend{submitErr: lost}

	err := engine.SubmitOnce(context.Background(), f, "build")
	if err != lost {
		t.Fatalf("error is %v, want the backend's own uncertain-delivery error unchanged", err)
	}
	if f.submitCalls != 1 {
		t.Errorf("the terminator was tried %d times, want 1 — an uncertain Enter must not be resent", f.submitCalls)
	}
}

// A second failure that already carries its own classification keeps it. Were
// it rewrapped as TIMEOUT, errors.Is would answer to two sentinels at once. A
// session that is gone has no input line, so it is not marked typed.
func TestAClassifiedSecondTerminatorFailureKeepsItsCode(t *testing.T) {
	gone := backend.Errorf(backend.CodeSessionNotFound, "session build is gone")
	f := &fakeBackend{submitErr: gone}

	err := engine.SubmitOnce(context.Background(), f, "build")
	if backend.CodeOf(err) != backend.CodeSessionNotFound {
		t.Fatalf("code is %s, want SESSION_NOT_FOUND", backend.CodeOf(err))
	}
	if errors.Is(err, backend.ErrTimeout) {
		t.Errorf("error %v also answers to ErrTimeout", err)
	}
	if backend.TypedOf(err) {
		t.Errorf("error %v is marked typed, though its session is gone", err)
	}
	if f.submitCalls != 2 {
		t.Errorf("the terminator was tried %d times, want 2", f.submitCalls)
	}
}

// §4.4: any other classified second failure keeps its code and is marked
// typed, since the text is still in the input line.
func TestAClassifiedSecondTerminatorFailureIsTyped(t *testing.T) {
	down := backend.Errorf(backend.CodeBackendUnavailable, "the server is not answering")
	f := &fakeBackend{submitErr: down}

	err := engine.SubmitOnce(context.Background(), f, "build")
	if backend.CodeOf(err) != backend.CodeBackendUnavailable {
		t.Fatalf("code is %s, want BACKEND_UNAVAILABLE", backend.CodeOf(err))
	}
	if !backend.TypedOf(err) {
		t.Errorf("error %v is not marked typed, though the text is still in the input line", err)
	}
}

// §7.5: an inspection that refuses is the refusal, and nothing reaches the
// pane. A prompt waiting on a person takes every keystroke typed at it, so a
// refusal that typed first and complained afterwards would already be an answer.
func TestAnInspectionThatRefusesTypesNothing(t *testing.T) {
	f := &fakeBackend{onType: func(f *fakeBackend, text string) { f.setScreen("$ " + text) }}
	d := delivery(t, f, nil)
	d.Inspect = func(context.Context, string) (engine.Inspection, error) {
		return engine.Inspection{}, backend.Errorf(backend.CodeAgentBlocked, "the agent in build is waiting on a person")
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
	d.Inspect = func(context.Context, string) (engine.Inspection, error) {
		_, attemptErr = locks.Acquire(ctx, key("build"), 10*time.Millisecond)
		return engine.Inspection{}, nil
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
	inBox := func(screen string, n engine.Needles) (engine.Echo, error) {
		for _, line := range strings.Split(screen, "\n") {
			if strings.HasPrefix(line, "> ") && engine.ScreenContains(line, n.Head) {
				return engine.EchoWhole, nil
			}
		}
		return engine.EchoNone, nil
	}

	t.Run("the echo in the input counts", func(t *testing.T) {
		f := &fakeBackend{screen: "earlier: make build"}
		f.onType = func(f *fakeBackend, text string) { f.setScreen("earlier: make build\n> " + text) }
		d := delivery(t, f, nil)
		d.Inspect = func(context.Context, string) (engine.Inspection, error) { return engine.Inspection{Watch: inBox}, nil }
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
		d.Inspect = func(context.Context, string) (engine.Inspection, error) { return engine.Inspection{Watch: inBox}, nil }
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
	d.Inspect = func(context.Context, string) (engine.Inspection, error) {
		return engine.Inspection{Watch: func(screen string, _ engine.Needles) (engine.Echo, error) {
			if strings.Contains(screen, "proceed?") {
				return engine.EchoNone, backend.Errorf(backend.CodeAgentBlocked, "the agent in build is waiting on a person")
			}
			return engine.EchoNone, nil
		}}, nil
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

// §7.7: a target the inspection asks to have pasted gets the text as one
// paste. A paste-aware program reads a long burst of typing as several
// pastes split where the terminal's reads fell, and keeps only the last.
func TestAnInspectionThatAsksForAPasteGetsOne(t *testing.T) {
	f := &fakeBackend{onPaste: func(f *fakeBackend, text string) { f.setScreen("> " + text) }}
	d := delivery(t, f, nil)
	d.Inspect = func(context.Context, string) (engine.Inspection, error) {
		return engine.Inspection{Paste: true}, nil
	}

	if err := d.VerifiedSubmit(context.Background(), "build", "make build"); err != nil {
		t.Fatalf("VerifiedSubmit: %v", err)
	}
	f.mu.Lock()
	pasted := len(f.pasted)
	f.mu.Unlock()
	typed, submits := f.counts()
	if pasted != 1 || typed != 0 || submits != 1 {
		t.Errorf("pasted %d, typed %d and submitted %d, want one paste, no typing and one terminator", pasted, typed, submits)
	}
}

// §7.6: a watch that sees only part of the text in the input box fails the
// send as typed. A resend would put a second copy beside the part that is
// there, and the terminator would submit a text with its head missing.
func TestAPartialEchoFailsTypedWithoutResendOrSubmit(t *testing.T) {
	f := &fakeBackend{onType: func(f *fakeBackend, text string) { f.setScreen("> " + text[len(text)/2:]) }}
	d := delivery(t, f, nil)
	d.Inspect = func(context.Context, string) (engine.Inspection, error) {
		return engine.Inspection{Watch: func(screen string, n engine.Needles) (engine.Echo, error) {
			switch {
			case engine.ScreenContains(screen, n.Whole):
				return engine.EchoWhole, nil
			case engine.ScreenContains(screen, n.Tail):
				return engine.EchoPart, nil
			}
			return engine.EchoNone, nil
		}}, nil
	}

	started := time.Now()
	err := d.VerifiedSubmit(context.Background(), "build", "make build && make test && make install && make release && make announce")
	if !errors.Is(err, backend.ErrTimeout) {
		t.Fatalf("error is %v, want a timeout", err)
	}
	if !backend.TypedOf(err) {
		t.Errorf("error %v is not marked typed, so a caller would send the text again beside the part in the box", err)
	}
	typed, submits := f.counts()
	if typed != 1 || submits != 0 {
		t.Errorf("typed %d and submitted %d, want one send and no terminator", typed, submits)
	}
	if elapsed := time.Since(started); elapsed >= 2*d.Budget {
		t.Errorf("the failure took %s, so it waited on a second window", elapsed)
	}
}

// §7.1: text with no letter or digit has to appear as typed, inside a watch
// as on the whole screen. Its needles are empty and match any box, so a watch
// that reads the box as whole has not seen it.
func TestAWatchsWholeEchoStillNeedsTheTextAsTyped(t *testing.T) {
	f := &fakeBackend{screen: "> "}
	d := delivery(t, f, nil)
	d.Inspect = func(context.Context, string) (engine.Inspection, error) {
		return engine.Inspection{Watch: func(string, engine.Needles) (engine.Echo, error) {
			return engine.EchoWhole, nil
		}}, nil
	}

	err := d.VerifiedSubmit(context.Background(), "build", "!!!")
	if !errors.Is(err, backend.ErrTimeout) {
		t.Fatalf("error is %v, want a timeout", err)
	}
	if _, submits := f.counts(); submits != 0 {
		t.Errorf("submitted %d times on text that never showed, want 0", submits)
	}
}
