package meja

import (
	"testing"

	"github.com/husniadil/olympus/backend"
)

// The names meja's send-keys takes for the keys a phone key bar has beyond the
// original vocabulary. Not tmux's in every case: "DC" is typed as letters.
func TestWidenedKeysAreSpelledInSendKeysNames(t *testing.T) {
	for key, want := range map[backend.Key]string{
		"delete":  "Delete",
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

// meja drops the shift from "S-Tab", so back-tab goes as its bytes instead.
func TestBackTabIsWrittenAsItsBytes(t *testing.T) {
	if got := keyLiterals[backend.KeyShiftTab]; got != "\x1b[Z" {
		t.Errorf("keyLiterals[s-tab] = %q, want %q", got, "\x1b[Z")
	}
	if _, ok := keyName(backend.KeyShiftTab); ok {
		t.Errorf("s-tab has a send-keys name, but meja does not deliver it")
	}
}
