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

// Alt is a prefix, not a modifier bit: a terminal sends ESC before the letter.
// It is a shape like c-<letter> for the same reason — a readline binding such
// as M-b or M-f is whatever letter the program chose.
func TestTheWholeMetaRangeIsRecognised(t *testing.T) {
	for letter := byte('a'); letter <= 'z'; letter++ {
		key := backend.Key("m-" + string(letter))
		if got := backend.MetaLetter(key); got != letter {
			t.Errorf("MetaLetter(%q) = %q, want %q", key, got, letter)
		}
	}
	if got := backend.MetaLetter("m-B"); got != 'b' {
		t.Errorf("MetaLetter(\"m-B\") = %q, want 'b'", got)
	}
}

func TestNonMetaKeysAreNotMistakenForOne(t *testing.T) {
	for _, key := range []backend.Key{"m-1", "m-", "meta-a", "m-ab", "m--", "m-enter", "c-a", "a", ""} {
		if got := backend.MetaLetter(key); got != 0 {
			t.Errorf("MetaLetter(%q) = %q, want none", key, got)
		}
	}
}

// A key bar sends alt with a symbol or a digit the same way it sends alt with a
// letter: ESC and then the character. Every printable ASCII character that is
// not a letter or a space is its own key, spelled as itself.
func TestEveryPrintableNonLetterIsAMetaSymbol(t *testing.T) {
	for c := byte('!'); c <= '~'; c++ {
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') {
			continue
		}
		key := backend.Key("m-" + string(c))
		if got := backend.MetaSymbol(key); got != c {
			t.Errorf("MetaSymbol(%q) = %q, want %q", key, got, c)
		}
	}
}

func TestNonSymbolMetaKeysAreNotMistakenForOne(t *testing.T) {
	for _, key := range []backend.Key{
		"m-a", "m-Z", "m-", "m- ", "m-\t", "m-\x7f", "m-\x1b", "m-é", "m-ab", "m-12",
		"m-enter", "c-1", "s-1", "1", "", "meta-1",
	} {
		if got := backend.MetaSymbol(key); got != 0 {
			t.Errorf("MetaSymbol(%q) = %q, want none", key, got)
		}
	}
}
