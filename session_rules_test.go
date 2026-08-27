package olympus

import (
	"context"
	"strings"
	"testing"

	"github.com/husniadil/olympus/backend"
)

// §5.3: the door layer gathers metadata for every target FIRST and, where the
// alt-screen flag is true, drops the history request and discloses that it did.
//
// The multi-target capture had this; the single-target one did not, and it is
// the one WaitFor and ExitStatus read through — so on a full-screen program
// every one of them asked for scrollback that does not exist and said nothing
// about it.
func TestASingleTargetCaptureDropsHistoryOnTheAlternateScreen(t *testing.T) {
	f := &fakeBackend{caps: backend.Capabilities{Backend: backend.Tmux}, meta: backend.ScreenMeta{AltScreen: true}, text: "a full-screen grid"}
	s := &Session{ol: fakeOlympus(f), name: "editor"}

	screen, err := s.Screen(context.Background(), WithHistory(500))
	if err != nil {
		t.Fatalf("Screen: %v", err)
	}

	if len(f.screenOpts) != 1 {
		t.Fatalf("the backend was captured %d times, want 1", len(f.screenOpts))
	}
	if got := f.screenOpts[0].HistoryLines; got != 0 {
		t.Errorf("the capture asked for %d lines of history on an alt-screen pane, want 0", got)
	}
	// The capture itself is never skipped: the visible grid is the only way to
	// observe a full-screen program at all.
	if screen.Text != "a full-screen grid" {
		t.Errorf("captured %q, want the visible grid", screen.Text)
	}
	if !screen.Meta.AltScreen {
		t.Error("the alt-screen flag did not travel back with the capture")
	}
	if !disclosesAltScreen(screen.Warnings) {
		t.Errorf("warnings are %v, want one saying the history request was dropped", screen.Warnings)
	}
}

// The same call on an ordinary pane asks for exactly what it was told to, and
// says nothing extra. Dropping history unconditionally would be the same defect
// pointing the other way.
func TestAnOrdinaryCaptureKeepsItsHistoryRequest(t *testing.T) {
	f := &fakeBackend{caps: backend.Capabilities{Backend: backend.Tmux}, text: "$ "}
	s := &Session{ol: fakeOlympus(f), name: "build"}

	screen, err := s.Screen(context.Background(), WithHistory(500))
	if err != nil {
		t.Fatalf("Screen: %v", err)
	}
	if got := f.screenOpts[0].HistoryLines; got != 500 {
		t.Errorf("the capture asked for %d lines of history, want the 500 requested", got)
	}
	if disclosesAltScreen(screen.Warnings) {
		t.Errorf("warnings are %v, want no alt-screen disclosure on an ordinary pane", screen.Warnings)
	}
}

func disclosesAltScreen(warnings []Warning) bool {
	for _, w := range warnings {
		if strings.Contains(w.Message, "alternate screen") {
			return true
		}
	}
	return false
}

// §4.4: a door that injects and then submits retries the terminator exactly
// once. `olympus type --submit` and the MCP type_text tool both compose it, and
// both used to press Enter once and surface the failure with the text left
// sitting in the input line.
func TestTypeAndSubmitRetriesADroppedTerminatorOnce(t *testing.T) {
	f := &fakeBackend{caps: backend.Capabilities{Backend: backend.Tmux}, submitFailures: 1}
	s := &Session{ol: fakeOlympus(f), name: "build"}

	if err := s.TypeAndSubmit(context.Background(), "make build"); err != nil {
		t.Fatalf("TypeAndSubmit: %v", err)
	}
	if len(f.typed) != 1 {
		t.Errorf("the text was typed %d times, want 1 — a retry must not re-type it", len(f.typed))
	}
	if f.submits != 1 {
		t.Errorf("the terminator landed %d times, want 1 — the retry did not run", f.submits)
	}
}

// §10: every target method resolves its target in the one shared place, so a
// caller holding a pane id can address a view the way they address anything
// else. ScrollView was the one method that handed its argument straight to the
// backend.
func TestScrollViewResolvesItsTarget(t *testing.T) {
	f := &fakeBackend{
		caps:  backend.Capabilities{Backend: backend.Tmux},
		panes: []backend.Pane{{ID: "%7", SessionName: "olympus-view-build-abcd"}},
	}

	if err := fakeOlympus(f).ScrollView(context.Background(), "%7", -5); err != nil {
		t.Fatalf("ScrollView: %v", err)
	}
	if len(f.scrolled) != 1 || f.scrolled[0] != "olympus-view-build-abcd" {
		t.Errorf("the backend was asked to scroll %v, want the session the pane id resolves to", f.scrolled)
	}
}
