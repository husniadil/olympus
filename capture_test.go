package olympus_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/husniadil/olympus"
)

// Capturing several targets in one call is a different operation from capturing
// one twice: it is one round trip, and the door's alt-screen rule needs to see
// them together.
func TestCaptureReadsSeveralTargetsAtOnce(t *testing.T) {
	t.Parallel()
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

			// Waited for, not assumed. A verified send proves the text LANDED,
			// not that the shell has run it — so capturing straight afterwards
			// races the expansion, which is the §16 trap: under load the
			// output has not been painted yet and the failure looks like a
			// capture bug rather than a test one.
			if _, err := first.WaitFor(ctx, `first-1`, olympus.WaitTimeout(15*time.Second)); err != nil {
				t.Fatalf("the first session never ran its command: %v", err)
			}
			if _, err := second.WaitFor(ctx, `second-2`, olympus.WaitTimeout(15*time.Second)); err != nil {
				t.Fatalf("the second session never ran its command: %v", err)
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

// Listing every pane is a question in its own right: a pane id is the only
// handle some callers hold, and resolving one means seeing them all.
func TestPanesListsEveryPaneOrJustOneSessions(t *testing.T) {
	t.Parallel()
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
