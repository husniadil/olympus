package backend_test

import (
	"strconv"
	"testing"

	"github.com/husniadil/olympus/backend"
)

// The vocabulary is open on purpose. A closed list of a few control letters is
// the obvious design and it means a caller simply cannot press Ctrl-X — which
// is how you leave nano, and how most full-screen programs are driven.

func TestTheWholeControlRangeIsRecognised(t *testing.T) {
	for letter := byte('a'); letter <= 'z'; letter++ {
		key := backend.Key("c-" + string(letter))
		if got := backend.ControlLetter(key); got != letter {
			t.Errorf("ControlLetter(%q) = %q, want %q", key, got, letter)
		}
	}
	// Case is a spelling, not a different key.
	if got := backend.ControlLetter("c-X"); got != 'x' {
		t.Errorf("ControlLetter(\"c-X\") = %q, want 'x'", got)
	}
}

func TestNonControlKeysAreNotMistakenForOne(t *testing.T) {
	for _, key := range []backend.Key{"enter", "c-", "c-1", "c-ab", "ctrl-x", "x", "", "c--"} {
		if got := backend.ControlLetter(key); got != 0 {
			t.Errorf("ControlLetter(%q) = %q, want none", key, got)
		}
	}
}

func TestFunctionKeysAreRecognisedWithinTheirRange(t *testing.T) {
	for n := 1; n <= 12; n++ {
		key := backend.Key("f" + strconv.Itoa(n))
		if got := backend.FunctionNumber(key); got != n {
			t.Errorf("FunctionNumber(%q) = %d, want %d", key, got, n)
		}
	}
	// Beyond 12 terminals disagree about the encoding, so accepting one would
	// mean promising a keypress Olympus cannot faithfully deliver.
	for _, key := range []backend.Key{"f0", "f13", "f99", "f", "fx", "enter"} {
		if got := backend.FunctionNumber(key); got != 0 {
			t.Errorf("FunctionNumber(%q) = %d, want none", key, got)
		}
	}
}
