package engine

import (
	"context"
	"time"

	"github.com/husniadil/olympus/backend"
)

// A Delivery performs verified sends against one session.
type Delivery struct {
	Backend backend.Backend
	// Locks may be nil, which is how a caller that already serializes its own
	// writes opts out. That must be an explicit choice at the door.
	Locks    *Locks
	Key      LockKey
	LockWait time.Duration
	Budget   time.Duration
	Poll     time.Duration
	// Inspect, when set, runs inside the lock before anything is typed
	// (behavior §7.5). An error refuses the delivery with nothing typed. A
	// Watch it returns replaces the whole-screen match for this delivery; a
	// nil one keeps it.
	Inspect func(ctx context.Context) (Watch, error)
}

// A Watch reads one capture and says whether the delivered text shows where
// input lands (behavior §7.6). head and tail are the normalized needles
// (§7.1). An error stops the delivery at once: no resend and no terminator,
// since both would type into whatever the watch saw.
type Watch func(screen, head, tail string) (bool, error)

// VerifiedSubmit sends text, waits until it is observed on screen, and only
// then submits it.
//
// The lock is held across send, verify and submit as ONE critical section, and
// is deliberately not released and reacquired between verification succeeding
// and the terminator being sent. A competing writer landing in that gap —
// another send clearing the input line, a resize reflowing it — invalidates
// exactly what verification just confirmed, so the terminator would submit
// something other than what was verified (behavior §11.2).
//
// This is the opposite of a run's rule, which releases before polling. The
// distinguishing question is whether the phase after the lock gates a
// subsequent write whose correctness depends on the observed state still
// holding. Here it does.
func (d Delivery) VerifiedSubmit(ctx context.Context, target, text string) error {
	return d.Verified(ctx, target, text, true)
}

// Verified delivers and confirms text, submitting only if asked.
//
// Verifying WITHOUT submitting is a real use: filling an input line and leaving
// it for a human, or for a later terminator whose timing the caller controls.
// The lock still spans the whole call — with submit it must, and without it
// there is nothing to gain by releasing early.
func (d Delivery) Verified(ctx context.Context, target, text string, submit bool) error {
	return WithLock(ctx, d.Locks, d.Key, d.LockWait, func() error {
		var watch Watch
		if d.Inspect != nil {
			w, err := d.Inspect(ctx)
			if err != nil {
				return err
			}
			watch = w
		}
		if err := d.deliver(ctx, target, text, watch); err != nil {
			return err
		}
		if !submit {
			return nil
		}
		return SubmitOnce(ctx, d.Backend, target)
	})
}

// SubmitOnce sends the terminator and retries it exactly once (behavior §4.4).
//
// Once text sits in the input line, a failed Enter does not merely fail visibly
// — it leaves that text there, where the NEXT injection silently concatenates
// onto it and corrupts both. Every composed inject-then-submit path goes
// through here rather than calling Submit itself, because a retry written out
// per call site is one that ends up present on some paths and missing on the
// rest, which is exactly the state this repository was in.
//
// It does NOT take the lock: the caller already holds it, and the whole point
// is that nothing lands between the injection and its terminator.
func SubmitOnce(ctx context.Context, b backend.Backend, target string) error {
	if err := b.Submit(ctx, target); err == nil {
		return nil
	}
	if err := b.Submit(ctx, target); err != nil {
		return backend.Wrapf(backend.CodeTimeout, err,
			"text was delivered to %s but not submitted, and is still sitting in the input line", target)
	}
	return nil
}

// deliver sends and verifies, resending once (behavior §7.4).
//
// The failure guarded is a dropped or coalesced FIRST delivery, not a garbled
// second attempt: the same text is resent, and only a miss on the second,
// independent window fails.
//
// Either end of the text counts as seen (behavior §7.1). A resend of text
// that did land is not harmless: it doubles the input line and leaves it
// unsubmitted, so a long text whose head has scrolled out of its input box
// must not read as dropped.
func (d Delivery) deliver(ctx context.Context, target, text string, watch Watch) error {
	head, tail := Normalize(text), NormalizeTail(text)

	if err := d.Backend.Type(ctx, target, text); err != nil {
		return err
	}
	if seen, err := d.observed(ctx, target, head, tail, watch); seen || err != nil {
		return err
	}

	if err := d.Backend.Type(ctx, target, text); err != nil {
		return err
	}
	if seen, err := d.observed(ctx, target, head, tail, watch); seen || err != nil {
		return err
	}

	return backend.Errorf(backend.CodeTimeout,
		"text sent to %s was never observed on screen, after one resend", target)
}

// observed polls the screen for either needle within one attempt budget, by
// the watch where there is one and over the whole screen where there is not.
// Only a watch's own error stops it early.
func (d Delivery) observed(ctx context.Context, target, head, tail string, watch Watch) (bool, error) {
	deadline := time.Now().Add(d.Budget)
	for {
		capture, err := d.Backend.Screen(ctx, target, backend.ScreenOpts{})
		// A capture failure is not a match, and not a reason to stop: the
		// budget is what bounds this, so a transient read failure costs one
		// poll rather than the whole attempt.
		if err == nil {
			if watch == nil {
				if ScreenContains(capture.Text, head) || ScreenContains(capture.Text, tail) {
					return true, nil
				}
			} else if seen, werr := watch(capture.Text, head, tail); seen || werr != nil {
				return seen && werr == nil, werr
			}
		}
		if time.Now().After(deadline) {
			return false, nil
		}
		select {
		case <-ctx.Done():
			return false, nil
		case <-time.After(d.Poll):
		}
	}
}
