package engine_test

import (
	"errors"
	"testing"

	"github.com/husniadil/olympus/backend"
	"github.com/husniadil/olympus/internal/engine"
)

func TestExitMarkerReadsTheCode(t *testing.T) {
	code, found, err := engine.ExitMarker("building\nTASK_COMPLETED:0\n", "TASK_COMPLETED:")
	if err != nil {
		t.Fatalf("ExitMarker: %v", err)
	}
	if !found {
		t.Fatal("the marker was not found")
	}
	if code != 0 {
		t.Errorf("exit code %d, want 0", code)
	}
}

// The failure this rule exists for: an exiting full-screen process never clears
// to end of line, so the wrapper's echo lands on a row still carrying leftover
// content. Requiring the whole remainder to parse classifies every such
// legitimate exit as malformed, the code stays unset forever, and a reaper that
// reads a missing marker as "still running" silently never fires.
func TestLeftoverScreenContentAfterTheCodeIsIgnored(t *testing.T) {
	code, found, err := engine.ExitMarker("TASK_COMPLETED:0 Esc to cancel\n", "TASK_COMPLETED:")
	if err != nil {
		t.Fatalf("ExitMarker: %v", err)
	}
	if !found {
		t.Fatal("leftover content to the right of the code hid a legitimate completion")
	}
	if code != 0 {
		t.Errorf("exit code %d, want 0", code)
	}
}

// The token itself stays strict. Salvaging a prefix out of it would report a
// completion the wrapper never wrote.
func TestAMalformedTokenIsNotACompletion(t *testing.T) {
	for _, line := range []string{"TASK_COMPLETED:0abc\n", "TASK_COMPLETED:abc\n", "TASK_COMPLETED:\n"} {
		if _, found, err := engine.ExitMarker(line, "TASK_COMPLETED:"); err != nil || found {
			t.Errorf("line %q reported a completion", line)
		}
	}
}

// A mid-line occurrence is ordinary output that happens to mention the marker —
// an echoed command, a log line quoting it — and matching it would report a
// completion that never happened.
func TestAMidLineOccurrenceIsNotACompletion(t *testing.T) {
	capture := "$ echo TASK_COMPLETED:0\nwaiting\n"
	if _, found, err := engine.ExitMarker(capture, "TASK_COMPLETED:"); err != nil || found {
		t.Error("a mid-line mention of the marker reported a completion")
	}
}

// Scrollback holds every run the session has done, so the newest completion is
// the answer.
func TestTheLastCompletionWins(t *testing.T) {
	capture := "TASK_COMPLETED:1\nrerunning\nTASK_COMPLETED:0\n"
	code, found, err := engine.ExitMarker(capture, "TASK_COMPLETED:")
	if err != nil {
		t.Fatalf("ExitMarker: %v", err)
	}
	if !found || code != 0 {
		t.Errorf("exit code %d found=%v, want 0 found=true", code, found)
	}
}

func TestNoMarkerOnScreenIsNotFound(t *testing.T) {
	if _, found, err := engine.ExitMarker("nothing here\n", "TASK_COMPLETED:"); err != nil || found {
		t.Error("a capture with no marker reported a completion")
	}
}

// There is deliberately no default marker: a fixed one would collide with
// ordinary program output or stale scrollback, and weaken exactly the
// caller-controlled uniqueness the design depends on.
func TestAnEmptyMarkerIsAUsageError(t *testing.T) {
	_, _, err := engine.ExitMarker("anything", "")
	if !errors.Is(err, backend.ErrUsage) {
		t.Errorf("error is %v, want a usage error", err)
	}
}

func TestNonZeroExitCodes(t *testing.T) {
	for _, want := range []int{1, 7, 42, 127, 255} {
		capture := "MARK:" + itoa(want) + "\n"
		code, found, err := engine.ExitMarker(capture, "MARK:")
		if err != nil || !found || code != want {
			t.Errorf("exit code %d found=%v err=%v, want %d", code, found, err, want)
		}
	}
}
