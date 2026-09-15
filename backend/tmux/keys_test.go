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
	for _, key := range []backend.Key{"m-1", "m-", "meta-a", "m-ab", "c-home"} {
		if got, ok := keyName(key); ok {
			t.Errorf("keyName(%q) = %q, want unknown", key, got)
		}
	}
}
