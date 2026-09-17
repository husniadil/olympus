package agentstate

import (
	"regexp"
	"strings"
)

// Composer reads the input box an agent draws, where its manifest names one
// (behavior §7.6): the text in that box, and whether a box was drawn at all.
//
// drawn is false for an agent with no manifest, for one whose manifest names
// no box, and for a screen with no box on it. A drawn box is not proof that
// input lands there: a question can fill the same region, which is why the
// caller reads Detect first (§7.5).
func Composer(agent string, in Input) (text string, drawn bool) {
	m, ok := Lookup(agent)
	if !ok || !m.namesComposer() {
		return "", false
	}
	body := promptBoxBody(strings.ReplaceAll(in.Screen, "\r\n", "\n"))
	if strings.TrimSpace(body) == "" {
		return "", false
	}
	return body, true
}

// namesComposer reports whether any of the manifest's rules reads the box an
// agent draws around its input. That is the manifest's own statement that the
// agent draws one.
func (m *Manifest) namesComposer() bool {
	for _, r := range m.Rules {
		if r.Region == regionPromptBoxBody {
			return true
		}
	}
	return false
}

// pastePlaceholder is what Claude Code draws in its box in place of pasted
// text too long to show: "[Pasted text #3]", numbered across the session.
var pastePlaceholder = regexp.MustCompile(`\[Pasted text #\d+`)

// Pastes counts the paste placeholders in a box (behavior §7.6). A text the
// agent collapsed into one cannot be matched, so a placeholder that was not
// there before typing is the only sign it arrived.
func Pastes(box string) int {
	return len(pastePlaceholder.FindAllStringIndex(box, -1))
}
