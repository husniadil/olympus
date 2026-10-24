package engine

import (
	"context"
	"strings"
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
	// (behavior §7.5). An error refuses the delivery with nothing typed.
	Inspect func(ctx context.Context, text string) (Inspection, error)
}

// An Inspection is what a delivery learned about its target before typing.
type Inspection struct {
	// Watch, when set, replaces the whole-screen match for this delivery
	// (behavior §7.6). A nil one keeps it.
	Watch Watch
	// Paste delivers the text as one paste rather than as typing (§7.7). A
	// paste-aware program reads a long burst of typing as several pastes,
	// split where the terminal's reads fell.
	Paste bool
}

// A Watch reads one capture and says how much of the delivered text shows
// where input lands (behavior §7.6). An error stops the delivery at once: no
// resend and no terminator, since both would type into whatever the watch saw.
type Watch func(screen string, n Needles) (Echo, error)

// Needles are a text normalized for matching (§7.1): the head and tail
// needles, and the whole text uncapped.
type Needles struct {
	Head, Tail, Whole string
}

// An Echo is how much of a delivered text a capture shows.
type Echo int

const (
	// EchoNone shows nothing of the text.
	EchoNone Echo = iota
	// EchoPart shows some of it and not all: a delivery that lost a part, or
	// one still arriving.
	EchoPart
	// EchoWhole shows all of it.
	EchoWhole
)

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
		var in Inspection
		if d.Inspect != nil {
			var err error
			if in, err = d.Inspect(ctx, text); err != nil {
				return err
			}
		}
		if err := d.deliver(ctx, target, text, in); err != nil {
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
// A terminator whose delivery is unknown is the exception: it may already have
// submitted, and a second one would submit twice. A second failure that already
// carries its own code keeps it, so it answers to one sentinel, not two.
//
// It does NOT take the lock: the caller already holds it, and the whole point
// is that nothing lands between the injection and its terminator.
func SubmitOnce(ctx context.Context, b backend.Backend, target string) error {
	err := b.Submit(ctx, target)
	if err == nil {
		return nil
	}
	if backend.UncertainOf(err) {
		return err
	}
	if err := b.Submit(ctx, target); err != nil {
		if backend.UncertainOf(err) || backend.CodeOf(err) != backend.CodeUnexpected {
			return err
		}
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
//
// A pane on the alternate screen is never sent the text twice: a full-screen
// program can act on keys without showing them, so unseen text there may
// already have done its work. The flag is read before typing, since the text
// can end the program (a pager's q) and leave the shell the resend would land
// in. A flag that cannot be read keeps the resend.
//
// A watch that still sees only part of the text when a window closes fails
// the delivery, marked typed (§7.6). A resend would put a second copy beside
// that part, and the terminator would submit a text missing the rest.
func (d Delivery) deliver(ctx context.Context, target, text string, in Inspection) error {
	n := Needles{Head: Normalize(text), Tail: NormalizeTail(text), Whole: NormalizeWhole(text)}
	// Text with no letters or digits normalizes to nothing, and an empty needle
	// matches any screen. Such text is looked for as typed instead, trailing
	// whitespace aside since some backends drop it (§7.1).
	raw := ""
	if n.Head == "" {
		raw = strings.TrimSpace(text)
	}
	meta, err := d.Backend.ScreenMeta(ctx, target)
	fullScreen := err == nil && meta.AltScreen
	write := d.Backend.Type
	if in.Paste {
		write = d.Backend.Paste
	}
	// Where the whole screen is matched, what was on it before typing is
	// counted first, so a copy of the text already there, the last time the
	// same command ran, confirms nothing (§7.2). A watch reads its own input
	// box and needs no count.
	var before copies
	if in.Watch == nil {
		before = d.copiesOnScreen(ctx, target, n, raw)
	}

	if err := write(ctx, target, text); err != nil {
		return err
	}
	echo, err := d.observed(ctx, target, n, raw, in.Watch, before)
	if done, err := settled(target, echo, err); done {
		return err
	}
	if fullScreen {
		return backend.Errorf(backend.CodeTimeout,
			"text sent to %s was never observed on screen, and was not sent again: the pane is on the alternate screen, where a full-screen program can act on keys without showing them, so read the screen to see what they did", target)
	}

	if err := write(ctx, target, text); err != nil {
		return err
	}
	// Against the count from before the first send: one copy of either send
	// on screen is the delivery.
	echo, err = d.observed(ctx, target, n, raw, in.Watch, before)
	if done, err := settled(target, echo, err); done {
		return err
	}

	return backend.Errorf(backend.CodeTimeout,
		"text sent to %s was never observed on screen, after one resend", target)
}

// copies counts where a screen shows a text: each needle by ScreenCount, and
// the text as typed where it has no letter or digit (§7.1).
type copies struct{ head, tail, raw int }

func countCopies(screen string, n Needles, raw string) copies {
	c := copies{head: ScreenCount(screen, n.Head), tail: ScreenCount(screen, n.Tail)}
	if raw != "" {
		c.raw = strings.Count(screen, raw)
	}
	return c
}

// more says whether c shows the text in more places than before did: the
// text as typed where it is looked for that way, since its needles are then
// empty, and either needle otherwise.
func (c copies) more(before copies, raw string) bool {
	if raw != "" {
		return c.raw > before.raw
	}
	return c.head > before.head || c.tail > before.tail
}

// copiesOnScreen counts the copies on screen before anything is typed. A
// capture that fails counts none, which is the rule without the count: any
// copy then confirms.
func (d Delivery) copiesOnScreen(ctx context.Context, target string, n Needles, raw string) copies {
	capture, err := d.Backend.Screen(ctx, target, backend.ScreenOpts{})
	if err != nil {
		return copies{}
	}
	return countCopies(capture.Text, n, raw)
}

// settled says whether one window's outcome ends the delivery, and how. Only
// an echo of nothing leaves it open, for the resend.
func settled(target string, echo Echo, err error) (bool, error) {
	switch {
	case err != nil:
		return true, err
	case echo == EchoWhole:
		return true, nil
	case echo == EchoPart:
		incomplete := backend.Errorf(backend.CodeTimeout,
			"text sent to %s was typed but is incomplete: its input box shows only part of it, so it was not submitted and not sent again; read the screen before sending again, since what did arrive is still in the box", target)
		incomplete.Typed = true
		return true, incomplete
	}
	return false, nil
}

// observed polls the screen within one attempt budget, by the watch where
// there is one and for either needle over the whole screen where there is
// not, and answers with the last capture's echo. Only a watch's own error
// stops it early. A non-empty raw needle must also appear verbatim on the
// screen. Over the whole screen a needle counts only where it shows in more
// places than before did, the screen before anything was typed.
func (d Delivery) observed(ctx context.Context, target string, n Needles, raw string, watch Watch, before copies) (Echo, error) {
	deadline := time.Now().Add(d.Budget)
	last := EchoNone
	for {
		capture, err := d.Backend.Screen(ctx, target, backend.ScreenOpts{})
		// A capture failure is not a match, and not a reason to stop: the
		// budget is what bounds this, so a transient read failure costs one
		// poll rather than the whole attempt.
		if err == nil {
			rawSeen := raw == "" || strings.Contains(capture.Text, raw)
			if watch == nil {
				if countCopies(capture.Text, n, raw).more(before, raw) {
					return EchoWhole, nil
				}
			} else if echo, werr := watch(capture.Text, n); werr != nil {
				return EchoNone, werr
			} else if echo == EchoWhole && rawSeen {
				return EchoWhole, nil
			} else if echo == EchoWhole {
				// The needles were empty, so the watch matched nothing.
				last = EchoNone
			} else {
				last = echo
			}
		}
		if time.Now().After(deadline) {
			return last, nil
		}
		select {
		case <-ctx.Done():
			return last, nil
		case <-time.After(d.Poll):
		}
	}
}
