package tmux

import "strings"

// fieldSeparator is the byte Olympus asks tmux to put between format fields.
//
// A control character rather than a printable one because session names and
// paths are arbitrary text: any printable delimiter is a delimiter a caller can
// put in a session name, and the row then comes apart in the wrong places.
const fieldSeparator = "\x1f"

// escapedFieldSeparator is what some tmux versions print INSTEAD of that byte.
//
// tmux sanitizes non-printable characters in format output, and which ones
// survive differs by version. Measured: 3.7b emits the 0x1f byte, while 3.5a
// emits the four characters `\037` — and turns a tab into `_`, so no control
// character survives there at all. The floor is 3.3 (§0.5), so both spellings
// are inside the supported range and both must parse.
const escapedFieldSeparator = `\037`

// SplitFields splits one format row into its fields, in either spelling.
//
// Exported so the behaviour can be tested without a tmux of each version to
// hand: the difference is in what tmux PRINTS, and a fixed string reproduces
// that exactly while a live server only reproduces whichever version is
// installed.
//
// The escaped form is tried only when the byte is absent, so a row that really
// does contain the byte is never re-split on text that happens to look like an
// escape.
func SplitFields(line string) []string {
	if strings.Contains(line, fieldSeparator) {
		return strings.Split(line, fieldSeparator)
	}
	if strings.Contains(line, escapedFieldSeparator) {
		return strings.Split(line, escapedFieldSeparator)
	}
	return []string{line}
}
