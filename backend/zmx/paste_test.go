package zmx

import "testing"

// §4.6 The mode is read off the state zmx serializes before the screen's
// content: present while the program has it set, absent once it reset it.
// Measured on zmx 0.6.0, the sequence also survived 20,000 lines of output
// that pushed the set out of the history.
func TestBracketedPasteIsReadOffTheSerializedState(t *testing.T) {
	cases := []struct {
		vt   string
		want bool
	}{
		{"\x1b[?2004h\x1b[?1049h\x1b[Hprompt", true},
		{"\x1b[?1049h\x1b[Hprompt", false},
		{"", false},
		{"text that says ?2004h", false},
	}
	for _, c := range cases {
		if got := bracketedPaste(c.vt); got != c.want {
			t.Errorf("bracketedPaste(%q) = %v, want %v", c.vt, got, c.want)
		}
	}
}
