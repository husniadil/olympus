package engine_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/husniadil/olympus/backend"
	"github.com/husniadil/olympus/internal/engine"
)

// A scripted stand-in for a session, so the escalation logic is tested against
// an exact sequence of observations rather than against a real clock.
type scriptedSession struct {
	states     []backend.State // consumed one per probe; the last repeats
	interrupts int
	kills      int
	sleeps     []time.Duration
	// diesAfterInterrupt makes the session start reporting absent once it has
	// been interrupted, which is the ordinary graceful path.
	diesAfterInterrupt bool
	interruptErr       error
	killErr            error
}

func (s *scriptedSession) ops() engine.KillOps {
	return engine.KillOps{
		Probe: func(context.Context) backend.State {
			if s.diesAfterInterrupt && s.interrupts > 0 {
				return backend.StateAbsent
			}
			if len(s.states) == 0 {
				return backend.StatePresent
			}
			state := s.states[0]
			if len(s.states) > 1 {
				s.states = s.states[1:]
			}
			return state
		},
		Interrupt: func(context.Context) error {
			s.interrupts++
			return s.interruptErr
		},
		Kill: func(context.Context) error {
			s.kills++
			return s.killErr
		},
		Sleep: func(_ context.Context, d time.Duration) error {
			s.sleeps = append(s.sleeps, d)
			return nil
		},
	}
}

var quickPolicy = engine.KillPolicy{Presses: 1, Gap: 10 * time.Millisecond, Poll: 10 * time.Millisecond, Timeout: 30 * time.Millisecond}

// Probing first is what makes "gone" mean "was already gone". A session
// observed absent only AFTER interrupts is graceful, and the two are
// indistinguishable without this.
func TestAnAlreadyAbsentSessionIsGoneAndIsNeverInterrupted(t *testing.T) {
	s := &scriptedSession{states: []backend.State{backend.StateAbsent}}
	got, err := engine.GracefulKill(context.Background(), s.ops(), quickPolicy)
	if err != nil {
		t.Fatalf("GracefulKill: %v", err)
	}
	if got != engine.KillGone {
		t.Errorf("outcome %q, want %q", got, engine.KillGone)
	}
	if s.interrupts != 0 {
		t.Errorf("sent %d interrupts to an already-absent session, want none", s.interrupts)
	}
	if s.kills != 0 {
		t.Errorf("force-killed an already-absent session")
	}
}

func TestASessionThatStopsOnTheInterruptIsGraceful(t *testing.T) {
	s := &scriptedSession{diesAfterInterrupt: true}
	got, err := engine.GracefulKill(context.Background(), s.ops(), quickPolicy)
	if err != nil {
		t.Fatalf("GracefulKill: %v", err)
	}
	if got != engine.KillGraceful {
		t.Errorf("outcome %q, want %q", got, engine.KillGraceful)
	}
	if s.interrupts != 1 {
		t.Errorf("sent %d interrupts, want 1", s.interrupts)
	}
	if s.kills != 0 {
		t.Errorf("force-killed a session that had already stopped")
	}
}

// The case the whole escalation exists for: a process that cannot be
// interrupted at all, which is a declared outcome on one backend and session
// shape (§2.8.1).
func TestASessionThatIgnoresTheInterruptIsForceKilled(t *testing.T) {
	s := &scriptedSession{}
	got, err := engine.GracefulKill(context.Background(), s.ops(), quickPolicy)
	if err != nil {
		t.Fatalf("GracefulKill: %v", err)
	}
	if got != engine.KillForced {
		t.Errorf("outcome %q, want %q", got, engine.KillForced)
	}
	if s.kills != 1 {
		t.Errorf("force-killed %d times, want 1", s.kills)
	}
}

// The gap is BETWEEN presses: none before the first, none after the last. A
// trailing gap would only delay the poll phase for nothing.
func TestGapsFallBetweenPressesOnly(t *testing.T) {
	s := &scriptedSession{diesAfterInterrupt: true}
	policy := quickPolicy
	policy.Presses = 3
	if _, err := engine.GracefulKill(context.Background(), s.ops(), policy); err != nil {
		t.Fatalf("GracefulKill: %v", err)
	}
	if s.interrupts != 3 {
		t.Fatalf("sent %d interrupts, want 3", s.interrupts)
	}

	gaps := 0
	for _, d := range s.sleeps {
		if d == policy.Gap {
			gaps++
		}
	}
	if gaps != 2 {
		t.Errorf("slept the gap %d times for 3 presses, want 2 — a gap before the first or after the last press", gaps)
	}
}

// The timeout bounds the poll phase only, so total wall time is
// presses*gap + timeout rather than the timeout alone.
func TestTheTimeoutBoundsThePollPhaseOnly(t *testing.T) {
	s := &scriptedSession{}
	policy := engine.KillPolicy{Presses: 2, Gap: 10 * time.Millisecond, Poll: 10 * time.Millisecond, Timeout: 50 * time.Millisecond}
	if _, err := engine.GracefulKill(context.Background(), s.ops(), policy); err != nil {
		t.Fatalf("GracefulKill: %v", err)
	}

	var total time.Duration
	for _, d := range s.sleeps {
		total += d
	}
	want := time.Duration(policy.Presses-1)*policy.Gap + policy.Timeout
	if total != want {
		t.Errorf("slept %s in total, want %s (presses*gap plus the poll timeout)", total, want)
	}
}

// A session dying between the probe and the call already means the desired
// state holds, so both operations treat not-found as success-shaped.
func TestNotFoundFromTheInterruptOrTheKillIsNotAFailure(t *testing.T) {
	s := &scriptedSession{interruptErr: backend.Errorf(backend.CodeSessionNotFound, "gone"), killErr: backend.Errorf(backend.CodeSessionNotFound, "gone")}
	got, err := engine.GracefulKill(context.Background(), s.ops(), quickPolicy)
	if err != nil {
		t.Fatalf("a not-found from an operation failed the kill: %v", err)
	}
	if got != engine.KillForced {
		t.Errorf("outcome %q, want %q", got, engine.KillForced)
	}
}

// A transport error is not an outcome. It propagates as an ordinary error
// rather than being reported as one of the three successes.
func TestATransportErrorPropagates(t *testing.T) {
	boom := errors.New("the socket went away")
	s := &scriptedSession{interruptErr: boom}
	if _, err := engine.GracefulKill(context.Background(), s.ops(), quickPolicy); !errors.Is(err, boom) {
		t.Errorf("error is %v, want the underlying failure", err)
	}
}

// A backend that cannot be reached at all is not a session that is gone.
func TestAnUnreachableBackendIsAnErrorNotAnOutcome(t *testing.T) {
	s := &scriptedSession{states: []backend.State{backend.StateError}}
	got, err := engine.GracefulKill(context.Background(), s.ops(), quickPolicy)
	if err == nil {
		t.Fatalf("an unreachable backend reported outcome %q instead of failing", got)
	}
	if !errors.Is(err, backend.ErrUnavailable) {
		t.Errorf("error is %q, want %q", backend.CodeOf(err), backend.CodeBackendUnavailable)
	}
	if s.interrupts != 0 {
		t.Errorf("interrupted a session it could not observe")
	}
}

// Concluding death from a backend that could not be asked is finalizing on
// doubt. A probe that errors mid-poll must not become "graceful"; the kill
// escalates instead.
func TestAProbeErrorDuringThePollIsNotTreatedAsDeath(t *testing.T) {
	s := &scriptedSession{states: []backend.State{backend.StatePresent, backend.StateError}}
	got, err := engine.GracefulKill(context.Background(), s.ops(), quickPolicy)
	if err != nil {
		t.Fatalf("GracefulKill: %v", err)
	}
	if got == engine.KillGraceful {
		t.Error("a probe error during the poll was read as the session having stopped")
	}
	if got != engine.KillForced || s.kills != 1 {
		t.Errorf("outcome %q with %d kills, want %q with 1", got, s.kills, engine.KillForced)
	}
}

// The shipped schedule is the one §17.3 specifies, decided once.
func TestTheDefaultPolicyIsTheSpecifiedOne(t *testing.T) {
	want := engine.KillPolicy{Presses: 1, Gap: 150 * time.Millisecond, Poll: 150 * time.Millisecond, Timeout: 2 * time.Second}
	if engine.DefaultKillPolicy != want {
		t.Errorf("default policy is %+v, want %+v", engine.DefaultKillPolicy, want)
	}
}
