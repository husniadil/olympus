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

// Capabilities that exist because a caller needs the question answered
// directly, not as a side effect of asking something else.

// Capturing several targets in one call is a different operation from capturing
// one twice: it is one round trip, and the door's alt-screen rule needs to see
// them together.
func TestCaptureReadsSeveralTargetsAtOnce(t *testing.T) {
	for _, l := range legs(t) {
		t.Run(l.name, func(t *testing.T) {
			ol := l.open(t)
			first := session(t, ol)
			second := session(t, ol)
			ctx := context.Background()

			if err := first.Send(ctx, `printf 'first-%d\n' 1`); err != nil {
				t.Fatalf("Send: %v", err)
			}
			if err := second.Send(ctx, `printf 'second-%d\n' 2`); err != nil {
				t.Fatalf("Send: %v", err)
			}

			captured, err := ol.Capture(ctx, []string{first.Name(), second.Name()})
			if err != nil {
				t.Fatalf("Capture: %v", err)
			}
			if len(captured.Screens) != 2 {
				t.Fatalf("captured %d screens, want 2", len(captured.Screens))
			}
			// Each target's own screen, not one screen under two keys.
			if !strings.Contains(captured.Screens[first.Name()], "first-1") {
				t.Errorf("the first target's screen is missing its own output")
			}
			if !strings.Contains(captured.Screens[second.Name()], "second-2") {
				t.Errorf("the second target's screen is missing its own output")
			}
			if _, ok := captured.Meta[first.Name()]; !ok {
				t.Errorf("no metadata for the first target, so a caller cannot tell an empty screen from a skipped one")
			}
		})
	}
}

// §5.3's door rule: a pane on the alternate screen is SKIPPED rather than
// captured, and the flag beside the empty screen is what says so.
func TestAnAltScreenTargetIsSkippedRatherThanCaptured(t *testing.T) {
	for _, l := range legs(t) {
		t.Run(l.name, func(t *testing.T) {
			ol := l.open(t)
			ctx := context.Background()
			name := fmt.Sprintf("oly-o%d", counter.Add(1))

			s, err := ol.Session(ctx, name, olympus.In(t.TempDir()),
				olympus.Command("sh", "-c", `printf '\033[?1049h'; sleep 30`))
			if err != nil {
				t.Fatalf("Session: %v", err)
			}
			t.Cleanup(func() { _, _ = s.Stop(ctx, olympus.Force()) })

			if !ol.Capabilities().TracksAltScreen {
				// A backend that does not track it cannot skip on it, and says
				// so through the capability rather than by guessing.
				captured, err := ol.Capture(ctx, []string{name})
				if err != nil {
					t.Fatalf("Capture: %v", err)
				}
				if captured.Meta[name].AltScreen {
					t.Errorf("a backend that does not track alt-screen reported the flag set")
				}
				return
			}

			deadline := time.Now().Add(10 * time.Second)
			for {
				captured, err := ol.Capture(ctx, []string{name})
				if err != nil {
					t.Fatalf("Capture: %v", err)
				}
				if captured.Meta[name].AltScreen {
					if captured.Screens[name] != "" {
						t.Errorf("an alt-screen target was captured rather than skipped: %q", captured.Screens[name])
					}
					return
				}
				if time.Now().After(deadline) {
					t.Fatal("the pane never reported alt-screen")
				}
				time.Sleep(100 * time.Millisecond)
			}
		})
	}
}

// Listing every pane is a question in its own right: a pane id is the only
// handle some callers hold, and resolving one means seeing them all.
func TestPanesListsEveryPaneOrJustOneSessions(t *testing.T) {
	for _, l := range legs(t) {
		t.Run(l.name, func(t *testing.T) {
			ol := l.open(t)
			first := session(t, ol)
			second := session(t, ol)
			ctx := context.Background()

			all, err := ol.Panes(ctx, "")
			if err != nil {
				t.Fatalf("Panes: %v", err)
			}
			if len(all) < 2 {
				t.Errorf("listing every pane returned %d, want at least the two sessions", len(all))
			}

			one, err := ol.Panes(ctx, first.Name())
			if err != nil {
				t.Fatalf("Panes: %v", err)
			}
			if len(one) == 0 {
				t.Fatal("listing one session's panes returned none")
			}
			for _, p := range one {
				if p.SessionName != first.Name() {
					t.Errorf("a pane of %s appeared while listing %s", p.SessionName, first.Name())
				}
			}
			_ = second
		})
	}
}

// Create means "this must not already exist". Ensure cannot express that, and
// checking afterwards by reading the outcome is a race rather than a check.
func TestCreateFailsWhenTheNameIsTaken(t *testing.T) {
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
	for _, l := range legs(t) {
		t.Run(l.name, func(t *testing.T) {
			ol := l.open(t)
			ctx := context.Background()

			before, err := ol.Sessions(ctx)
			if err != nil {
				t.Fatalf("Sessions: %v", err)
			}

			result, warnings, err := ol.RunOnce(ctx, `printf 'throwaway-%d\n' 3`)
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
	for _, l := range legs(t) {
		t.Run(l.name, func(t *testing.T) {
			ol := l.open(t)
			ctx := context.Background()

			before, err := ol.Sessions(ctx)
			if err != nil {
				t.Fatalf("Sessions: %v", err)
			}
			result, _, err := ol.RunOnce(ctx, "sh -c 'exit 9'")
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

// WaitFor reports the line that matched, so a caller does not have to run the
// match again to find out which line it was.
func TestWaitForReportsTheMatchedLine(t *testing.T) {
	for _, l := range legs(t) {
		t.Run(l.name, func(t *testing.T) {
			s := session(t, l.open(t))
			ctx := context.Background()

			if err := s.Send(ctx, `printf 'matched-%d\n' 7`); err != nil {
				t.Fatalf("Send: %v", err)
			}
			got, err := s.WaitFor(ctx, `matched-7`, olympus.WaitTimeout(10*time.Second))
			if err != nil {
				t.Fatalf("WaitFor: %v", err)
			}
			if !got.Matched {
				t.Error("the result does not report that it matched")
			}
			if !strings.Contains(got.Line, "matched-7") {
				t.Errorf("the matched line is %q, which does not contain the pattern", got.Line)
			}
			// The line is one line, not the whole screen.
			if strings.Count(got.Line, "\n") != 0 {
				t.Errorf("the matched line spans several lines: %q", got.Line)
			}
		})
	}
}

// Pasting and submitting is one operation with the retry discipline, not two
// calls a caller has to sequence themselves.
func TestPasteAndSubmitExecutesTheFinalLine(t *testing.T) {
	for _, l := range legs(t) {
		t.Run(l.name, func(t *testing.T) {
			s := session(t, l.open(t))
			ctx := context.Background()

			if err := s.PasteAndSubmit(ctx, `printf 'pasted-%d\n' 5`); err != nil {
				t.Fatalf("PasteAndSubmit: %v", err)
			}
			if _, err := s.WaitFor(ctx, `pasted-5`, olympus.WaitTimeout(10*time.Second)); err != nil {
				t.Errorf("the pasted line was never executed: %v", err)
			}
		})
	}
}
