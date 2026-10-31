package tmux

import "testing"

// §4.1 From 3.7 paste-buffer passes a buffer through vis(3) unless given -S,
// which earlier versions refuse as an unknown flag. An unreadable version is
// taken to be a recent one.
func TestWhichTmuxSanitizesAPaste(t *testing.T) {
	cases := map[string]bool{
		"3.3":      false,
		"3.4":      false,
		"3.6b":     false,
		"3.7":      true,
		"3.7a":     true,
		"3.8":      true,
		"3.8-rc":   true,
		"next-3.8": true,
		"4.0":      true,
		"master":   true,
		"":         true,
	}
	for version, want := range cases {
		if got := sanitizesPastes(version); got != want {
			t.Errorf("sanitizesPastes(%q) = %v, want %v", version, got, want)
		}
	}
}

// §4.1 Only a text vis(3) would rewrite needs -S, so only such a text costs the
// version lookup: a control byte other than a tab or newline, a DEL, or a byte
// that is not UTF-8.
func TestWhichTextVisWouldRewrite(t *testing.T) {
	cases := map[string]bool{
		"echo hello":  false,
		"a\tb\nc":     false,
		"café ✓":      false,
		"x\x1b[1my":   true,
		"x\x7f":       true,
		"x\ry":        true,
		"x\x01":       true,
		"x\xffy":      true,
		"x\u0085y":    true,
		"":            false,
		"plain; text": false,
	}
	for text, want := range cases {
		if got := visRewrites(text); got != want {
			t.Errorf("visRewrites(%q) = %v, want %v", text, got, want)
		}
	}
}
