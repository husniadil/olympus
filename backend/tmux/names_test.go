package tmux

import "testing"

// §2.11 Whether a session name may carry a dot depends on the tmux running it:
// 3.3 through 3.6b store a dot as an underscore, 3.7 refuses it, and 3.7a
// onwards keeps it. An unreadable version is not held against the caller.
func TestWhichTmuxKeepsADotInASessionName(t *testing.T) {
	cases := map[string]bool{
		"3.3":      false,
		"3.3a":     false,
		"3.4":      false,
		"3.5a":     false,
		"3.6b":     false,
		"3.7":      false,
		"3.7a":     true,
		"3.7c":     true,
		"3.8-rc":   true,
		"next-3.8": true,
		"4.0":      true,
		"master":   true,
		"":         true,
	}
	for version, want := range cases {
		if got := keepsDotInSessionName(version); got != want {
			t.Errorf("keepsDotInSessionName(%q) = %v, want %v", version, got, want)
		}
	}
}
