package engine_test

import (
	"strings"
	"testing"

	"github.com/husniadil/olympus/internal/engine"
)

// The sentinel protocol's whole job is telling three things apart in one screen
// of text: the echo of the command line, the command's own output, and the
// completion marker. Every case here is one way that goes wrong.

const id = "abc123"

func markers() engine.Markers { return engine.NewMarkers(id) }

// pane reproduces what the pane actually shows once a run has started: the
// shell echoes the injected line verbatim — including BOTH marker strings —
// and only then does the first echo produce the real start marker on its own
// line.
//
// Both occurrences of each marker are therefore on screen. Quoting controls
// shell parsing, not terminal rendering, so nothing can hide them. What
// separates the echo from the real thing is EXPANSION: the echoed line shows a
// literal, unexpanded $?, and the echoed start marker sits inside a longer line
// that the real one does not.
func pane(command string) string {
	return markers().Line(command) + "\n" + "OLY_S_abc123" + "\n"
}

func TestParseFindsOutputAndExitCode(t *testing.T) {
	capture := pane("make build") +
		"compiling\ndone\n" +
		"OLY_D_abc123_0_\n"

	got, ok := markers().Parse(capture)
	if !ok {
		t.Fatalf("no completion found in:\n%s", capture)
	}
	if got.ExitCode != 0 {
		t.Errorf("exit code %d, want 0", got.ExitCode)
	}
	if got.Output != "compiling\ndone" {
		t.Errorf("output %q, want %q", got.Output, "compiling\ndone")
	}
}

// The echoed line contains the done marker followed by a literal "$?". Treating
// that as a completion would report success the instant the line is typed,
// before the command has run at all.
func TestTheEchoedCommandLineIsNotACompletion(t *testing.T) {
	if _, ok := markers().Parse(pane("sleep 10")); ok {
		t.Error("the echoed command line parsed as a completed run")
	}
}

// Without the trailing delimiter, a digit at the start of the NEXT captured
// line is absorbed into the exit-code digit run once newlines are stripped, and
// a clock in the prompt turns exit 0 into exit 12.
func TestAPromptOnTheNextLineDoesNotBecomeTheExitCode(t *testing.T) {
	capture := pane("true") + "OLY_D_abc123_0_\n12:34 $ "

	got, ok := markers().Parse(capture)
	if !ok {
		t.Fatal("no completion found")
	}
	if got.ExitCode != 0 {
		t.Errorf("exit code %d, want 0 — the next line's clock leaked into the digits", got.ExitCode)
	}
}

func TestExitCodesAcrossTheRange(t *testing.T) {
	for _, want := range []int{0, 1, 2, 7, 42, 127, 255} {
		capture := pane("cmd") + "out\n" + doneMarker(want)
		got, ok := markers().Parse(capture)
		if !ok {
			t.Errorf("exit code %d: no completion found", want)
			continue
		}
		if got.ExitCode != want {
			t.Errorf("exit code %d, want %d", got.ExitCode, want)
		}
	}
}

// The rule is one to three digits then the delimiter. A longer run is not a
// marker at all, and must not be silently truncated into one.
func TestAnOverlongDigitRunIsNotACompletion(t *testing.T) {
	capture := pane("cmd") + "OLY_D_abc123_1234_\n"
	if got, ok := markers().Parse(capture); ok {
		t.Errorf("a four-digit run parsed as exit code %d", got.ExitCode)
	}
}

// A capture window that caught the completion but scrolled past the start MUST
// parse as not-found rather than as a truncated match. The run then keeps
// polling until it times out, which is the correct failure mode: a too-small
// window is deliberately indistinguishable from "still running".
func TestACompletionWithoutItsStartIsNotFound(t *testing.T) {
	capture := "…earlier output that scrolled\nOLY_D_abc123_0_\n"
	if _, ok := markers().Parse(capture); ok {
		t.Error("a completion with no start marker parsed as a finished run")
	}
}

func TestAStartWithoutACompletionIsNotFound(t *testing.T) {
	capture := pane("sleep 10") + "still going\n"
	if _, ok := markers().Parse(capture); ok {
		t.Error("a run with no completion marker parsed as finished")
	}
}

// Scrollback holds every run the session has ever done. The newest completion
// is the answer, and the start marker paired with it must be the last one
// BEFORE it — not the last one overall, which belongs to a later echo.
func TestTheNewestRunWins(t *testing.T) {
	capture := pane("first") + "old output\n" + doneMarker(1) +
		pane("second") + "new output\n" + doneMarker(0)

	got, ok := markers().Parse(capture)
	if !ok {
		t.Fatal("no completion found")
	}
	if got.ExitCode != 0 {
		t.Errorf("exit code %d, want 0 — an older run was matched", got.ExitCode)
	}
	if got.Output != "new output" {
		t.Errorf("output %q, want %q", got.Output, "new output")
	}
}

// A marker wrapped by the pane's width comes back split by a newline that
// cannot be distinguished from a real one. Parsing runs against a copy with
// newlines stripped, so the split marker still matches.
func TestAMarkerSplitByAWrapStillParses(t *testing.T) {
	capture := pane("cmd") + "output\n" + "OLY_D_abc1\n23_0_\n"

	got, ok := markers().Parse(capture)
	if !ok {
		t.Fatalf("a wrapped completion marker was not found in:\n%s", capture)
	}
	if got.ExitCode != 0 {
		t.Errorf("exit code %d, want 0", got.ExitCode)
	}
}

func TestACommandThatPrintedNothingHasEmptyOutput(t *testing.T) {
	capture := pane("true") + doneMarker(0)
	got, ok := markers().Parse(capture)
	if !ok {
		t.Fatal("no completion found")
	}
	if got.Output != "" {
		t.Errorf("output %q, want empty", got.Output)
	}
}

// Two runs in flight against one session must not read each other's markers.
func TestMarkersOfAnotherRunAreIgnored(t *testing.T) {
	other := engine.NewMarkers("zzz999")
	capture := pane("mine") + "mine output\n" +
		other.Line("theirs") + "\ntheirs output\nOLY_D_zzz999_3_\n"

	if _, ok := markers().Parse(capture); ok {
		t.Error("another run's completion was matched")
	}
	if _, ok := other.Parse(capture); !ok {
		t.Error("the other run's own completion was not matched")
	}
}

// §6.1: the id must be unique across processes, goroutines and time, or two
// runs collide in exactly the way the case above is about.
func TestGeneratedIDsAreUnique(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 1000; i++ {
		got := engine.NewID()
		if seen[got] {
			t.Fatalf("generated id %q twice", got)
		}
		seen[got] = true
	}
}

// §17.1 reserves the sentinel shapes. They land in the session's own visible
// output, so they are as much a namespace as a session name is.
func TestMarkerShapesAreTheReservedOnes(t *testing.T) {
	line := markers().Line("cmd")
	if !strings.Contains(line, "OLY_S_abc123") {
		t.Errorf("the injected line does not carry the reserved start marker: %s", line)
	}
	if !strings.Contains(line, "OLY_D_abc123_") {
		t.Errorf("the injected line does not carry the reserved done marker: %s", line)
	}
	if !strings.Contains(line, "$?") {
		t.Errorf("the injected line does not capture the command's exit code: %s", line)
	}
}

// §6.3: both degradations are silent, which is why an explicit check is needed.
// A newline makes the shell run the fragments separately — both markers still
// echo, the run SUCCEEDS, and it reports the exit code of the last fragment. An
// empty command is shell-dependent: one shell hard-errors into a genuine
// timeout, another tolerates it and reports success.
func TestCommandsThatWouldDegradeSilentlyAreRejected(t *testing.T) {
	for _, command := range []string{"", "   ", "make build\nrm -rf /", "one\rtwo"} {
		if err := engine.ValidateCommand(command); err == nil {
			t.Errorf("command %q was accepted", command)
		}
	}
	if err := engine.ValidateCommand("make build"); err != nil {
		t.Errorf("an ordinary command was rejected: %v", err)
	}
}

func doneMarker(code int) string {
	return "OLY_D_abc123_" + itoa(code) + "_\n"
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var out []byte
	for n > 0 {
		out = append([]byte{byte('0' + n%10)}, out...)
		n /= 10
	}
	return string(out)
}
