package olympus

import (
	"context"
	"time"

	"github.com/husniadil/olympus/backend"
)

// SetStatus records an opaque label on the session, for a process inside it to
// leave for whoever is driving it from outside.
//
// Olympus never interprets the value, and deliberately defines no vocabulary of
// states. What counts as "busy" or "waiting" is a property of the program in
// the session, not of the terminal, and a fixed set would name the concerns of
// whatever is driving it rather than the thing being driven.
//
// UNSUPPORTED on a backend with nowhere to keep it. That refusal is the point:
// silently accepting a write nobody can ever read back would leave a caller
// waiting forever for a state that cannot arrive.
func (s *Session) SetStatus(ctx context.Context, status string) error {
	return s.ol.backend.SetStatus(ctx, s.name, status)
}

// Status reports the session's label, empty when it has never been given one.
//
// Empty is a real answer rather than an error, the same tri-state rule as
// presence (§3.5): a caller must be able to tell "has reported nothing" from
// "could not ask".
func (s *Session) Status(ctx context.Context) (string, error) {
	return s.ol.backend.Status(ctx, s.name)
}

// WaitForStatus blocks until the session reports exactly want.
//
// Exactly, not a pattern: the value is opaque to Olympus, so a partial match
// would be Olympus reading structure into a string it has promised not to
// interpret. A caller who wants looser matching owns the vocabulary and can
// poll Status themselves.
//
// It polls rather than subscribing because there is nothing to subscribe to:
// the status lives in the backend's own store, written by a process Olympus
// does not run, and no backend offers a change feed for it.
func (s *Session) WaitForStatus(ctx context.Context, want string, opts ...WaitOption) (string, error) {
	cfg := waitConfig{timeout: DefaultWaitTimeout, poll: DefaultWaitPoll}
	for _, opt := range opts {
		opt(&cfg)
	}

	deadline := time.Now().Add(cfg.timeout)
	for {
		got, err := s.Status(ctx)
		if err != nil {
			// An unsupported backend can never satisfy this, so polling on is
			// waiting for something that cannot happen. Every other failure is
			// returned too: a status that cannot be read is not a status that
			// has not arrived.
			return "", err
		}
		if got == want {
			return got, nil
		}
		if time.Now().After(deadline) {
			return got, backend.Errorf(backend.CodeTimeout,
				"%s did not report %q within %s", s.name, want, cfg.timeout)
		}
		select {
		case <-ctx.Done():
			return got, backend.Wrapf(backend.CodeTimeout, ctx.Err(), "waiting on %s", s.name)
		case <-time.After(cfg.poll):
		}
	}
}
