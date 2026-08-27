package olympus_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/husniadil/olympus"
	"github.com/husniadil/olympus/backend"
)

// Create means "this must not already exist". Ensure cannot express that, and
// checking afterwards by reading the outcome is a race rather than a check.
func TestCreateFailsWhenTheNameIsTaken(t *testing.T) {
	t.Parallel()
	for _, l := range legs(t) {
		t.Run(l.name, func(t *testing.T) {
			ol := l.open(t)
			ctx := context.Background()
			name := fmt.Sprintf("oly-o%d", counter.Add(1))

			first, err := ol.Create(ctx, name, olympus.In(t.TempDir()))
			if err != nil {
				t.Fatalf("Create: %v", err)
			}
			t.Cleanup(func() { _, _ = first.Stop(ctx, olympus.Force()) })

			if _, err := ol.Create(ctx, name); !errors.Is(err, olympus.ErrConflict) {
				t.Errorf("creating an existing name gave %v, want a conflict", err)
			}
			// Ensure still reuses it, which is the whole difference.
			reused, err := ol.Session(ctx, name)
			if err != nil {
				t.Fatalf("Session: %v", err)
			}
			if reused.Outcome() != backend.OutcomeReused {
				t.Errorf("ensure reported %q for an existing session, want reused", reused.Outcome())
			}
		})
	}
}

// §6.10: a run with no target gets a session of its own, killed afterwards.
func TestRunOnceUsesAThrowawaySessionAndCleansUp(t *testing.T) {
	t.Parallel()
	for _, l := range legs(t) {
		t.Run(l.name, func(t *testing.T) {
			ol := l.open(t)
			ctx := context.Background()

			before, err := ol.Sessions(ctx)
			if err != nil {
				t.Fatalf("Sessions: %v", err)
			}

			result, warnings, err := ol.RunOnce(ctx, `printf 'throwaway-%d\n' 3`, nil)
			if err != nil {
				t.Fatalf("RunOnce: %v", err)
			}
			if !strings.Contains(result.Output, "throwaway-3") {
				t.Errorf("output %q does not contain the command's output", result.Output)
			}
			for _, w := range warnings {
				t.Errorf("cleanup reported a problem: %s", w.Message)
			}

			// Killed afterwards: the session count must come back to where it
			// started, or every run leaks one.
			deadline := time.Now().Add(10 * time.Second)
			for {
				after, err := ol.Sessions(ctx)
				if err != nil {
					t.Fatalf("Sessions: %v", err)
				}
				if len(after) == len(before) {
					return
				}
				if time.Now().After(deadline) {
					var names []string
					for _, s := range after {
						names = append(names, s.Name)
					}
					t.Fatalf("the throwaway session was not cleaned up; sessions are now %v", names)
				}
				time.Sleep(100 * time.Millisecond)
			}
		})
	}
}

// A failing throwaway still cleans up: on failure and timeout alike, or the
// leak happens exactly when nobody is watching.
func TestAFailingRunOnceStillCleansUp(t *testing.T) {
	t.Parallel()
	for _, l := range legs(t) {
		t.Run(l.name, func(t *testing.T) {
			ol := l.open(t)
			ctx := context.Background()

			before, err := ol.Sessions(ctx)
			if err != nil {
				t.Fatalf("Sessions: %v", err)
			}
			result, _, err := ol.RunOnce(ctx, "sh -c 'exit 9'", nil)
			if err != nil {
				t.Fatalf("RunOnce: %v", err)
			}
			if result.ExitCode != 9 {
				t.Errorf("exit code %d, want 9", result.ExitCode)
			}

			deadline := time.Now().Add(10 * time.Second)
			for {
				after, err := ol.Sessions(ctx)
				if err != nil {
					t.Fatalf("Sessions: %v", err)
				}
				if len(after) == len(before) {
					return
				}
				if time.Now().After(deadline) {
					t.Fatal("a failing throwaway run leaked its session")
				}
				time.Sleep(100 * time.Millisecond)
			}
		})
	}
}

// §6.10 with §17.3: the run's own timeout applies to a throwaway too. A
// throwaway that dropped it would run every command on the default budget
// while reporting the caller's — and the shape of the bug is only visible
// when the two differ, so this asks for one the default would never produce.
func TestRunOnceHonorsItsRunTimeout(t *testing.T) {
	t.Parallel()
	for _, l := range legs(t) {
		t.Run(l.name, func(t *testing.T) {
			ol := l.open(t)
			ctx := context.Background()

			started := time.Now()
			_, _, err := ol.RunOnce(ctx, "sleep 120", []olympus.RunOption{olympus.RunTimeout(2 * time.Second)})
			elapsed := time.Since(started)

			if !errors.Is(err, olympus.ErrTimeout) {
				t.Fatalf("RunOnce gave %v, want a timeout", err)
			}
			// The timeout that fired must be the one asked for, not the
			// default one arriving early for some other reason.
			if !strings.Contains(err.Error(), "2s") {
				t.Errorf("the timeout error %q does not name the 2s budget it was given", err)
			}
			if elapsed > 30*time.Second {
				t.Errorf("the run took %s, so it waited on something other than its 2s budget", elapsed)
			}
		})
	}
}
