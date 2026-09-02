package olympus

import (
	"context"
	"strings"
	"testing"
	"time"

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

// §6.4 and §0.8: a detached poll on a backend that caps its read depth asks for
// the default window, which is above that cap — so the answer comes back
// shallower than the search it claims to have made, and that has to be
// disclosed.
//
// The disclosure existed but was computed from the RAW option, which is zero
// until the engine substitutes its default. On the path nobody passes a window
// on — the default one, which is every caller who does not use --lines — it
// therefore never fired.
func TestPollDisclosesTheDepthCapOnItsDefaultWindow(t *testing.T) {
	f := &fakeBackend{caps: backend.Capabilities{Backend: backend.Herdr}, text: "nothing to match here"}
	s := &Session{ol: fakeOlympus(f), name: "build"}

	got, err := s.Poll(context.Background(), "abc123")
	if err != nil {
		t.Fatalf("Poll: %v", err)
	}
	if !disclosesDepth(got.Warnings) {
		t.Errorf("warnings are %v, want one saying the window was reduced to the backend's cap", got.Warnings)
	}
}

// An explicit window below the cap is honoured in full, so saying anything
// about it would be noise — and untrue.
func TestPollSaysNothingWhenTheWindowFitsUnderTheCap(t *testing.T) {
	f := &fakeBackend{caps: backend.Capabilities{Backend: backend.Herdr}, text: "nothing to match here"}
	s := &Session{ol: fakeOlympus(f), name: "build"}

	got, err := s.Poll(context.Background(), "abc123", PollWindow(500))
	if err != nil {
		t.Fatalf("Poll: %v", err)
	}
	if disclosesDepth(got.Warnings) {
		t.Errorf("warnings are %v, want none: 500 lines is under the cap and comes back whole", got.Warnings)
	}
}

func disclosesDepth(warnings []Warning) bool {
	for _, w := range warnings {
		if strings.Contains(w.Message, "is the deepest this backend returns") {
			return true
		}
	}
	return false
}

// §6.2 and §6.4: a run that recovered its exit code without the start marker
// has an output that begins mid-run, and the caller has no way to see that from
// the payload — the exit code looks exactly like a whole one. It is disclosed.
func TestATruncatedRunDisclosesThatItsOutputIsPartial(t *testing.T) {
	f := &fakeBackend{caps: backend.Capabilities{Backend: backend.Herdr}}
	f.onType = func(f *fakeBackend, line string) {
		// Everything above the completion has scrolled past what this backend
		// returns, the run's own start marker included.
		f.text = "line 999\nOLY_D_" + runIDOf(line) + "_4_\n"
	}
	s := &Session{ol: fakeOlympus(f), name: "build"}

	got, err := s.Exec(context.Background(), "seq 1 100000", RunInterval(time.Millisecond))
	if err != nil {
		t.Fatalf("Exec: %v", err)
	}
	if got.ExitCode != 4 {
		t.Errorf("exit code %d, want the 4 the completion carried", got.ExitCode)
	}
	if !disclosesTruncation(got.Warnings) {
		t.Errorf("warnings are %v, want one saying the output is partial", got.Warnings)
	}
}

// A run that parsed both markers lost nothing, so it says nothing.
func TestAWholeRunDisclosesNoTruncation(t *testing.T) {
	f := &fakeBackend{caps: backend.Capabilities{Backend: backend.Herdr}}
	f.onType = func(f *fakeBackend, line string) {
		id := runIDOf(line)
		f.text = line + "\nOLY_S_" + id + "\nbuilt ok\nOLY_D_" + id + "_0_\n"
	}
	s := &Session{ol: fakeOlympus(f), name: "build"}

	got, err := s.Exec(context.Background(), "make build", RunInterval(time.Millisecond))
	if err != nil {
		t.Fatalf("Exec: %v", err)
	}
	if got.Output != "built ok" {
		t.Errorf("output %q, want the whole output", got.Output)
	}
	if disclosesTruncation(got.Warnings) {
		t.Errorf("warnings are %v, want none: nothing was lost", got.Warnings)
	}
}

// The detached path answers completed on the same evidence, and owes the same
// disclosure.
func TestATruncatedPollDisclosesThatItsOutputIsPartial(t *testing.T) {
	f := &fakeBackend{
		caps: backend.Capabilities{Backend: backend.Herdr},
		text: "line 999\nOLY_D_abc123_6_\n",
	}
	s := &Session{ol: fakeOlympus(f), name: "build"}

	got, err := s.Poll(context.Background(), "abc123")
	if err != nil {
		t.Fatalf("Poll: %v", err)
	}
	if got.State != "completed" {
		t.Fatalf("state %q, want completed", got.State)
	}
	if got.ExitCode == nil || *got.ExitCode != 6 {
		t.Errorf("exit code %v, want 6", got.ExitCode)
	}
	if !disclosesTruncation(got.Warnings) {
		t.Errorf("warnings are %v, want one saying the output is partial", got.Warnings)
	}
}

// runIDOf pulls the sentinel id out of an injected command line, which is the
// only place a test can read the id the engine generated.
func runIDOf(line string) string {
	const prefix = "OLY_S_"
	at := strings.Index(line, prefix)
	if at < 0 {
		return ""
	}
	rest := line[at+len(prefix):]
	if end := strings.IndexAny(rest, "; \t"); end >= 0 {
		return rest[:end]
	}
	return rest
}

func disclosesTruncation(warnings []Warning) bool {
	for _, w := range warnings {
		if strings.Contains(w.Message, "output begins partway through") {
			return true
		}
	}
	return false
}
