package engine

import (
	"context"
	"errors"
	"time"

	"github.com/husniadil/olympus/backend"
)

// A KillOutcome reports how a session ended. All three are successes.
type KillOutcome string

const (
	// KillGone means the session was ALREADY absent when asked, and no
	// interrupt was ever sent.
	KillGone KillOutcome = "gone"
	// KillGraceful means it stopped in response to the interrupt.
	KillGraceful KillOutcome = "graceful"
	// KillForced means the interrupt did not take and it was force-killed.
	KillForced KillOutcome = "killed"
)

// KillOps are the operations a graceful kill needs, injected so the decision
// logic can be tested exhaustively without a backend or a real clock
// (behavior §2.8).
type KillOps struct {
	Interrupt func(context.Context) error
	Probe     func(context.Context) backend.State
	Kill      func(context.Context) error
	Sleep     func(context.Context, time.Duration) error
}

// A KillPolicy is the escalation schedule. The timeout bounds the POLL phase
// only, so total wall time is presses*gap + timeout.
type KillPolicy struct {
	Presses int
	Gap     time.Duration
	Poll    time.Duration
	Timeout time.Duration
}

// DefaultKillPolicy is the schedule of behavior §17.3.
var DefaultKillPolicy = KillPolicy{
	Presses: 1,
	Gap:     150 * time.Millisecond,
	Poll:    150 * time.Millisecond,
	Timeout: 2 * time.Second,
}

// GracefulKill interrupts a session, waits for it to stop, and force-kills it
// if it does not.
func GracefulKill(ctx context.Context, ops KillOps, policy KillPolicy) (KillOutcome, error) {
	// Probe FIRST. This is what makes "gone" mean "was already gone" rather
	// than "died at some point": a session observed absent only AFTER
	// interrupts is graceful, and without the initial probe the two outcomes
	// are indistinguishable.
	switch ops.Probe(ctx) {
	case backend.StateAbsent:
		return KillGone, nil
	case backend.StateError:
		return "", backend.Errorf(backend.CodeBackendUnavailable, "cannot reach the backend to kill the session")
	}

	// Interrupts up front, with a gap BETWEEN presses: none before the first,
	// none after the last. A trailing gap would only delay the poll phase.
	for i := 0; i < policy.Presses; i++ {
		if i > 0 {
			if err := ops.Sleep(ctx, policy.Gap); err != nil {
				return "", err
			}
		}
		if err := ops.Interrupt(ctx); err != nil && !isAlreadyGone(err) {
			return "", err
		}
	}

	deadline := time.Duration(0)
	for {
		switch ops.Probe(ctx) {
		case backend.StateAbsent:
			return KillGraceful, nil
		case backend.StateError:
			// Deliberately NOT treated as gone. Concluding death from a
			// backend that could not be asked is finalizing on doubt, which is
			// the one thing the tri-state exists to prevent — so the poll
			// continues and, if nothing improves, the forced kill runs anyway.
		}
		if deadline >= policy.Timeout {
			break
		}
		if err := ops.Sleep(ctx, policy.Poll); err != nil {
			return "", err
		}
		deadline += policy.Poll
	}

	if err := ops.Kill(ctx); err != nil && !isAlreadyGone(err) {
		return "", err
	}
	return KillForced, nil
}

// isAlreadyGone reports whether an error means the desired state already holds.
//
// A session dying between the probe and the call is the outcome that was
// wanted, so both the interrupt and the forced kill treat not-found as
// success-shaped rather than as a failure to report.
func isAlreadyGone(err error) bool {
	return errors.Is(err, backend.ErrNotFound)
}
