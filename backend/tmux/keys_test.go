package tmux

import (
	"testing"

	"github.com/husniadil/olympus/backend"
)

// The names send-keys takes for the keys a phone key bar has beyond the
// original vocabulary.
func TestWidenedKeysAreSpelledInSendKeysNames(t *testing.T) {
	for key, want := range map[backend.Key]string{
		"delete":  "DC",
		"s-tab":   "BTab",
		"c-up":    "C-Up",
		"c-down":  "C-Down",
		"c-right": "C-Right",
		"c-left":  "C-Left",
		"m-enter": "M-Enter",
		"m-a":     "M-a",
		"m-Z":     "M-z",
	} {
		got, ok := keyName(key)
		if !ok || got != want {
			t.Errorf("keyName(%q) = %q, %v; want %q", key, got, ok, want)
		}
	}
	for _, key := range []backend.Key{"m-", "meta-a", "m-ab", "c-home"} {
		if got, ok := keyName(key); ok {
			t.Errorf("keyName(%q) = %q, want unknown", key, got)
		}
	}
}

// Shift and alt with an arrow, and alt with a digit or a symbol, in send-keys
// names. A trailing ";" is escaped, since tmux takes it as a command
// separator even inside a key name.
func TestModifiedArrowsAndMetaSymbolsAreSpelledInSendKeysNames(t *testing.T) {
	for key, want := range map[backend.Key]string{
		"s-up":    "S-Up",
		"s-down":  "S-Down",
		"s-right": "S-Right",
		"s-left":  "S-Left",
		"m-up":    "M-Up",
		"m-down":  "M-Down",
		"m-right": "M-Right",
		"m-left":  "M-Left",
		"m-0":     "M-0",
		"m-/":     "M-/",
		"m-\\":    "M-\\",
		"m-~":     "M-~",
		"m-;":     `M-\;`,
	} {
		got, ok := keyName(key)
		if !ok || got != want {
			t.Errorf("keyName(%q) = %q, %v; want %q", key, got, ok, want)
		}
	}
	for _, key := range []backend.Key{"s-a", "s-home", "m- ", "m-\t", "m-\x7f", "m-é", "m-12"} {
		if got, ok := keyName(key); ok {
			t.Errorf("keyName(%q) = %q, want unknown", key, got)
		}
	}
}
