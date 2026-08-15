package engine

import (
	"strings"
	"unicode"
)

// normalizedLimit caps a normalized string. It is meant to bound a single
// echoed LINE, never a whole pane (behavior §7.1).
const normalizedLimit = 24

// Normalize makes text comparable across terminal rendering noise: lowercase,
// keep only letters and digits, and cap the result.
//
// Punctuation, whitespace, box-drawing and prompt glyphs all drop, so a shell
// that redraws its prompt or a TUI that paints a border cannot make identical
// text compare unequal.
func Normalize(s string) string {
	out := make([]rune, 0, normalizedLimit)
	for _, r := range s {
		if len(out) >= normalizedLimit {
			break
		}
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			out = append(out, unicode.ToLower(r))
		}
	}
	return string(out)
}

// ScreenContains reports whether an already-normalized needle appears on a
// screen, matching per line and per adjacent pair (behavior §7.2, §7.3).
//
// Never against the whole screen as one blob. The cap bounds one line, and a
// real terminal's prompt banner or status line routinely burns through it
// BEFORE the pane even reaches the line the text is on — so a whole-screen
// normalization truncates at the banner, discards the line the text is actually
// on, and produces a false timeout with the answer sitting one line down.
//
// Per-line matching alone regresses wrap tolerance: on a backend with no
// rejoin, a needle whose echo straddles the PTY's width comes back split by a
// literal newline indistinguishable from a real one. A needle can straddle at
// most one boundary, so each adjacent pair is checked concatenated as well —
// covering every split point without reintroducing whole-screen truncation.
//
// The pair check covers a single wrap boundary only. Splitting across two would
// need a pane narrower than the normalized needle itself, and sub-24-column
// panes are not a supported target.
func ScreenContains(screen, needle string) bool {
	if needle == "" {
		return true
	}
	lines := strings.Split(screen, "\n")
	for _, line := range lines {
		if strings.Contains(Normalize(line), needle) {
			return true
		}
	}
	for i := 0; i+1 < len(lines); i++ {
		if strings.Contains(Normalize(lines[i]+lines[i+1]), needle) {
			return true
		}
	}
	return false
}
