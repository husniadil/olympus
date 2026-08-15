package engine

import (
	"strings"
	"unicode"
)

// normalizedLimit caps a NEEDLE: how much of the typed text has to be seen
// again for the echo to count as observed (behavior §7.1).
//
// It bounds the needle and nothing else. Capping the line being SEARCHED is what
// makes the answer disappear: bash's default prompt puts `user@host:/dir$ ` on
// the same line as the command and normalizes to more characters than this whole
// cap, so a capped line ends before the typed text begins and a verified send
// fails with its own echo on screen in front of it.
const normalizedLimit = 24

// Normalize makes text comparable across terminal rendering noise: lowercase,
// keep only letters and digits, and cap the result.
//
// Punctuation, whitespace, box-drawing and prompt glyphs all drop, so a shell
// that redraws its prompt or a TUI that paints a border cannot make identical
// text compare unequal.
func Normalize(s string) string {
	return normalize(s, normalizedLimit)
}

// normalizeLine is Normalize with no cap, for the text being SEARCHED.
//
// The haystack must not be truncated. What the cap is for — keeping a needle
// short enough to stay a needle — has nothing to do with how much of a line is
// worth looking at, and applying it to both is what let a prompt hide the very
// echo the search was for.
func normalizeLine(s string) string {
	return normalize(s, -1)
}

// normalize lowercases and keeps only letters and digits, optionally capped.
func normalize(s string, limit int) string {
	capacity := limit
	if capacity < 0 {
		capacity = len(s)
	}
	out := make([]rune, 0, capacity)
	for _, r := range s {
		if limit >= 0 && len(out) >= limit {
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
		if strings.Contains(normalizeLine(line), needle) {
			return true
		}
	}
	for i := 0; i+1 < len(lines); i++ {
		if strings.Contains(normalizeLine(lines[i]+lines[i+1]), needle) {
			return true
		}
	}
	return false
}
