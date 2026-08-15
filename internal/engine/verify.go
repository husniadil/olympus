package engine

import (
	"context"
	"time"

	"github.com/husniadil/olympus/backend"
)

// DefaultVerifyBudget is one attempt's polling window, and DefaultVerifyPoll
// the interval between captures (behavior §17.3). Worst-case wall time before
// a verified send fails is TWICE the budget, because of the resend.
const (
	DefaultVerifyBudget = 5 * time.Second
	DefaultVerifyPoll   = 100 * time.Millisecond
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
}

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
	return WithLock(ctx, d.Locks, d.Key, d.LockWait, func() error {
		if err := d.deliver(ctx, target, text); err != nil {
			return err
		}
		return d.Backend.Submit(ctx, target)
	})
}

// deliver sends and verifies, resending once (behavior §7.4).
//
// The failure guarded is a dropped or coalesced FIRST delivery, not a garbled
// second attempt: the same text is resent, and only a miss on the second,
// independent window fails.
func (d Delivery) deliver(ctx context.Context, target, text string) error {
	needle := Normalize(text)

	if err := d.Backend.Type(ctx, target, text); err != nil {
		return err
	}
	if d.observed(ctx, target, needle) {
		return nil
	}

	if err := d.Backend.Type(ctx, target, text); err != nil {
		return err
	}
	if d.observed(ctx, target, needle) {
		return nil
	}

	return backend.Errorf(backend.CodeTimeout,
		"text sent to %s was never observed on screen, after one resend", target)
}

// observed polls the screen for the needle within one attempt budget.
func (d Delivery) observed(ctx context.Context, target, needle string) bool {
	deadline := time.Now().Add(d.Budget)
	for {
		capture, err := d.Backend.Screen(ctx, target, backend.ScreenOpts{})
		// A capture failure is not a match, and not a reason to stop: the
		// budget is what bounds this, so a transient read failure costs one
		// poll rather than the whole attempt.
		if err == nil && ScreenContains(capture.Text, needle) {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		select {
		case <-ctx.Done():
			return false
		case <-time.After(d.Poll):
		}
	}
}
