package engine

import (
	"context"
	"time"

	"github.com/husniadil/olympus/backend"
)

// Run windows (behavior §6.4). The timeout and poll interval a run takes when
// nothing asks for others are §17.3 defaults and are decided in the ergonomic
// layer, which is the one place that holds them.
const (
	// The capture window starts here and quadruples on every miss, capped —
	// long-running output can scroll the sentinel markers off-screen while the
	// command is still producing output above them (behavior §6.4).
	initialWindow = 200
	windowGrowth  = 4
	maxWindow     = 10000

	// DetachedWindow is the detached path's fixed one-shot request. It is not
	// a growing loop: if scrollback has pushed the marker beyond it, poll
	// reports pending and the remedy is re-polling with a larger window
	// (behavior §6.7).
	DetachedWindow = maxWindow
)

// A PollStatus is a detached run's answer.
type PollStatus string

const (
	PollPending   PollStatus = "pending"
	PollCompleted PollStatus = "completed"
	PollDied      PollStatus = "died"
)

// A PollResult is one poll of a detached run.
type PollResult struct {
	Status PollStatus
	// ExitCode is a pointer and is populated ONLY when completed, never a fake
	// zero a naive consumer could read as success. Consumers branch on Status
	// first (behavior §6.7).
	ExitCode *int
	Output   string
	Reason   string
	// Truncated says the completion was read without its start marker, so
	// Output begins mid-run. The exit code is exact.
	Truncated bool
}

// A Runner executes commands through the sentinel protocol.
type Runner struct {
	Backend  backend.Backend
	Locks    *Locks
	Key      LockKey
	LockWait time.Duration
	Timeout  time.Duration
	Poll     time.Duration
	// Window is the detached path's one-shot capture depth. Zero takes
	// DetachedWindow. It is a fixed request rather than a growing loop: if
	// scrollback has pushed the marker past it, poll reports pending and the
	// remedy is re-polling with a larger one (behavior §6.7).
	Window int
}

// Exec runs a command and waits for it to finish.
func (r Runner) Exec(ctx context.Context, target, command string) (Result, error) {
	markers, err := r.inject(ctx, target, command)
	if err != nil {
		return Result{}, err
	}

	// The lock is released before polling. Only the injection needs to be
	// atomic with respect to concurrent writers; the polling phase only reads,
	// and holding the lock across the whole wait would block every other writer
	// against this session for the full timeout, for no benefit (behavior
	// §11.2). This is the opposite of verified delivery's rule, and getting
	// either backwards is a real defect.
	window := initialWindow
	deadline := time.Now().Add(r.Timeout)
	for {
		capture, err := r.capture(ctx, target, window)
		if err != nil {
			return Result{}, err
		}
		if result, ok := markers.Parse(capture); ok {
			return result, nil
		}
		// Recorded before the window grows, so the timeout below reports the
		// deepest look actually taken rather than the next one it would have.
		unreachable := window >= maxWindow && !markers.Started(capture)
		if unreachable {
			// The window has stopped growing and the start marker is gone, so
			// no later capture can be better than this one. If the completion
			// is readable, that exit code is the whole answer the protocol
			// will ever produce — waiting out the timeout would discard it
			// (§6.2, §6.4).
			if result, ok := markers.ParseTruncated(capture); ok {
				return result, nil
			}
		}

		// While a deeper look is still available, both markers are required:
		// a window too small to hold them is indistinguishable from "still
		// running", which is why the window grows rather than the run failing.
		if window < maxWindow {
			window *= windowGrowth
			if window > maxWindow {
				window = maxWindow
			}
		}

		if time.Now().After(deadline) {
			// Two timeouts that read identically to a caller and have nothing
			// in common: one command is merely slow, the other cannot be
			// matched from this screen at all. What is reported is the
			// OBSERVATION, with both causes named — output past the backend's
			// read cap and a full-screen program covering the screen produce
			// the same capture, and the backend where the cap bites does not
			// track the alternate screen, so naming one would be a guess
			// (§6.4).
			if unreachable {
				return Result{}, backend.Errorf(backend.CodeTimeout,
					"the command did not complete on %s within %s, and its start marker is not on the deepest screen this backend returns, so either its output has scrolled past that depth or a full-screen program is covering it",
					target, r.Timeout)
			}
			return Result{}, backend.Errorf(backend.CodeTimeout,
				"the command did not complete on %s within %s", target, r.Timeout)
		}
		select {
		case <-ctx.Done():
			return Result{}, backend.Wrapf(backend.CodeTimeout, ctx.Err(), "running a command on %s", target)
		case <-time.After(r.Poll):
		}
	}
}

// Start injects a command and returns its id without waiting.
//
// Nothing durable is written. The id IS the state: it is baked into the
// sentinel markers themselves, and a caller resumes solely by re-presenting
// (target, id) and having the poll re-scan scrollback (behavior §6.7).
func (r Runner) Start(ctx context.Context, target, command string) (string, error) {
	markers, err := r.inject(ctx, target, command)
	if err != nil {
		return "", err
	}
	return markers.ID(), nil
}

// inject validates, builds the markers, and writes the line under the lock.
func (r Runner) inject(ctx context.Context, target, command string) (Markers, error) {
	if err := ValidateCommand(command); err != nil {
		return Markers{}, err
	}
	markers := NewMarkers(NewID())
	line := markers.Line(command)

	err := WithLock(ctx, r.Locks, r.Key, r.LockWait, func() error {
		if err := r.Backend.Type(ctx, target, line); err != nil {
			return err
		}
		select {
		case <-ctx.Done():
			return backend.Wrapf(backend.CodeTimeout, ctx.Err(), "injecting a command into %s", target)
		case <-time.After(backend.SubmitSettle):
		}
		return SubmitOnce(ctx, r.Backend, target)
	})
	if err != nil {
		return Markers{}, err
	}
	return markers, nil
}

// PollRun answers whether a detached run has finished. It is lock-free: a read
// that blocks on a writer's lock turns observation into contention, and
// observing a busy session is the case that matters most (behavior §11.1).
func (r Runner) PollRun(ctx context.Context, target, id string) (PollResult, error) {
	markers := NewMarkers(id)

	capture, err := r.capture(ctx, target, r.PollWindow())
	if err == nil {
		if result, ok := markers.Parse(capture); ok {
			code := result.ExitCode
			return PollResult{Status: PollCompleted, ExitCode: &code, Output: result.Output}, nil
		}
		// A poll never grows its window — it asks for the deepest one on every
		// call — so a start marker missing here is missing for good, and
		// reporting pending forever would withhold an exit code the capture
		// already carries (§6.2, §6.4).
		if !markers.Started(capture) {
			if result, ok := markers.ParseTruncated(capture); ok {
				code := result.ExitCode
				return PollResult{Status: PollCompleted, ExitCode: &code, Output: result.Output, Truncated: true}, nil
			}
		}
	}

	// No completion on screen. Whether that is "still running" or "died" is a
	// question about the SESSION, and poll answers it from the listing.
	//
	// Poll deliberately never reports backend plumbing. A target that never
	// existed answers died, not not-found: poll's question is about the state
	// of a command, and from a read-only vantage point "the target vanished"
	// and "the target was never real" are indistinguishable. A backend that
	// disappeared answers died too, because listing maps "nothing running" to
	// an empty list (behavior §6.8).
	row, found, listErr := findSession(ctx, r.Backend, target)
	if listErr != nil || !found {
		return PollResult{Status: PollDied, Reason: "the session is no longer present"}, nil
	}
	if row.Dead {
		// A corpse pane keeps the session listed, so session-level death
		// detection alone would report pending forever even though the command
		// has died (behavior §6.9).
		return PollResult{Status: PollDied, Reason: "the session's command exited"}, nil
	}
	return PollResult{Status: PollPending}, nil
}

// PollWindow is the depth a detached poll actually searches.
//
// Exported because the ergonomic layer has to disclose the SAME number it
// discloses a cap against, and reading the zero-value field there would report
// on a window nobody uses: the substitution happens here, so the answer has to
// come from here too (§6.4, §0.8).
func (r Runner) PollWindow() int {
	if r.Window <= 0 {
		return DetachedWindow
	}
	return r.Window
}

// capture reads the screen with a window appropriate to the backend.
//
// The window is a request, not a guarantee. A backend that returns its own
// scrollback natively governs the depth itself: the request is ignored, and a
// command producing enough output to scroll its sentinel past whatever depth
// that backend retains can still be missed, with no workaround (behavior §6.4).
func (r Runner) capture(ctx context.Context, target string, window int) (string, error) {
	opts := backend.ScreenOpts{}
	if !r.Backend.Capabilities().NativeScrollback {
		opts.HistoryLines = window
	}
	capture, err := r.Backend.Screen(ctx, target, opts)
	if err != nil {
		return "", err
	}
	return capture.Text, nil
}
