package agentstate

import (
	"regexp"
	"strings"
)

// Line is the one line of its own output an agent row can stand for: what the
// agent last said, or, where it is waiting on a person, the question it is
// waiting on. It answers what `status` cannot — blocked says somebody is
// needed, not what for — and it is read from the same screen and the same
// regions the manifests are written against, so it moves when they do.
//
// This is a reading, not a protocol. No backend reports it: herdr exposes the
// state and the OSC title and nothing else, and the rest report neither. What
// makes it defensible is that the regions it uses are the manifests' own
// (`above_prompt_box`, `after_last_horizontal_rule`), proved against these
// agents' drawing rather than invented here.
//
// Empty is the honest answer for a screen that shows nothing to say, and for
// an agent with no manifest: the row is still an agent.
func Line(in Input, state State) string {
	if state == Blocked {
		return clean(blockedLine(in.Screen))
	}
	// What the agent last drew above its composer, which is also what drops
	// the status line the composer sits on.
	return clean(lastOf(content(splitLines(abovePromptBox(in.Screen)))))
}

// recentLines is how far back the question of a dialog is looked for: a
// permission prompt and its options are drawn together and near the bottom,
// and a question further up than this is a past one.
const recentLines = 30

// blockedLine is the dialog's question over the option under its cursor. The
// options are what the screen shows most of, and they say nothing about what
// is being decided.
func blockedLine(screen string) string {
	recent := content(splitLines(bottomLines(screen, recentLines)))
	for i := len(recent) - 1; i >= 0; i-- {
		if strings.HasSuffix(recent[i], "?") || strings.HasSuffix(recent[i], "？") {
			return recent[i]
		}
	}
	// No question mark: a dialog states its case rather than asking. Its own
	// last line is the nearest thing to the point.
	if tail := content(splitLines(afterLastHorizontalRule(screen))); len(tail) > 0 {
		return lastOf(tail)
	}
	return lastOf(recent)
}

// chrome is what a TUI draws to explain its own keys. It is never what the
// agent is saying.
var chrome = []*regexp.Regexp{
	regexp.MustCompile(`(?i)enter to select`),
	regexp.MustCompile(`(?i)enter to confirm`),
	regexp.MustCompile(`(?i)esc to cancel`),
	regexp.MustCompile(`(?i)\? for shortcuts`),
}

// herdrOverlay is the client's own notice on a pane's last line, not the
// agent's output.
var herdrOverlay = regexp.MustCompile(`\s*\d+ new messages? \(click\)\s*↓?\s*$`)

var whitespace = regexp.MustCompile(`\s+`)

// content is the lines that carry something the agent said: trimmed, with the
// blanks, the box borders and the key hints dropped.
func content(lines []string) []string {
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || isHorizontalRule(trimmed) {
			continue
		}
		if matchesChrome(trimmed) {
			continue
		}
		out = append(out, trimmed)
	}
	return out
}

func matchesChrome(line string) bool {
	for _, re := range chrome {
		if re.MatchString(line) {
			return true
		}
	}
	return false
}

func lastOf(lines []string) string {
	if len(lines) == 0 {
		return ""
	}
	return lines[len(lines)-1]
}

func clean(line string) string {
	return strings.TrimSpace(whitespace.ReplaceAllString(herdrOverlay.ReplaceAllString(line, ""), " "))
}
