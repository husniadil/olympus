package herdr

import (
	"testing"

	"github.com/husniadil/olympus/backend"
)

// The bytes a terminal sends for the keys a phone key bar has beyond the
// original vocabulary. Spelled here rather than trusted to a delivery test,
// since a backend that does not deliver control keys has no such test.
func TestWidenedKeysAreSpelledAsTerminalBytes(t *testing.T) {
	for key, want := range map[backend.Key]string{
		"delete":  "\x1b[3~",
		"s-tab":   "\x1b[Z",
		"c-up":    "\x1b[1;5A",
		"c-down":  "\x1b[1;5B",
		"c-right": "\x1b[1;5C",
		"c-left":  "\x1b[1;5D",
		"m-enter": "\x1b\r",
		"m-a":     "\x1ba",
		"m-Z":     "\x1bz",
	} {
		got, ok := keySequence(key)
		if !ok || got != want {
			t.Errorf("keySequence(%q) = %q, %v; want %q", key, got, ok, want)
		}
	}
	for _, key := range []backend.Key{"m-", "meta-a", "m-ab", "c-home"} {
		if got, ok := keySequence(key); ok {
			t.Errorf("keySequence(%q) = %q, want unknown", key, got)
		}
	}
}

// Shift and alt with an arrow are the xterm modified form, parameter 2 for
// shift and 3 for alt; alt with a digit or a symbol is ESC and the character.
func TestModifiedArrowsAndMetaSymbolsAreSpelledAsTerminalBytes(t *testing.T) {
	for key, want := range map[backend.Key]string{
		"s-up":    "\x1b[1;2A",
		"s-down":  "\x1b[1;2B",
		"s-right": "\x1b[1;2C",
		"s-left":  "\x1b[1;2D",
		"m-up":    "\x1b[1;3A",
		"m-down":  "\x1b[1;3B",
		"m-right": "\x1b[1;3C",
		"m-left":  "\x1b[1;3D",
		"m-0":     "\x1b0",
		"m-/":     "\x1b/",
		"m-;":     "\x1b;",
		"m-\\":    "\x1b\\",
		"m-~":     "\x1b~",
	} {
		got, ok := keySequence(key)
		if !ok || got != want {
			t.Errorf("keySequence(%q) = %q, %v; want %q", key, got, ok, want)
		}
	}
	for _, key := range []backend.Key{"s-a", "s-home", "m- ", "m-\t", "m-\x7f", "m-é", "m-12"} {
		if got, ok := keySequence(key); ok {
			t.Errorf("keySequence(%q) = %q, want unknown", key, got)
		}
	}
}
