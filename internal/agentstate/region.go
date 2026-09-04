package agentstate

import (
	"strconv"
	"strings"
	"unicode"
)

// Input is what a manifest is evaluated against: the pane's screen and, where
// the backend reports them, the strings the agent set through OSC sequences.
// Empty OSC fields are the honest value where nothing reports them; the OSC
// rules then simply never match.
type Input struct {
	// Screen is the visible screen, rows joined by newlines — the same tail
	// herdr reads, which is the pane's viewport and no scrollback: a prompt
	// already answered above the viewport must not read as a blocker.
	Screen string
	// OSCTitle is the terminal title the agent last set (OSC 0/2), where
	// the backend tracks it.
	OSCTitle string
	// OSCProgress is the progress state the agent last set (OSC 9;4), where
	// the backend tracks it. No supported backend does today.
	OSCProgress string
}

// region names, as the manifests spell them.
const (
	regionWholeRecent                = "whole_recent"
	regionAfterLastPromptMarker      = "after_last_prompt_marker"
	regionBeforeCurrentPromptMarker  = "before_current_prompt_marker"
	regionWithoutCurrentPromptMarker = "whole_recent_without_current_prompt_marker"
	regionCurrentPromptBlockMarker   = "current_prompt_block_marker"
	regionAfterCurrentPromptBlock    = "after_current_prompt_block_marker"
	regionPromptBoxBody              = "prompt_box_body"
	regionAbovePromptBox             = "above_prompt_box"
	regionLastNonEmptyAbovePromptBox = "last_non_empty_above_prompt_box"
	regionAfterLastHorizontalRule    = "after_last_horizontal_rule"
	regionOSCTitle                   = "osc_title"
	regionOSCProgress                = "osc_progress"
	regionBottomLines                = "bottom_lines"
	regionBottomNonEmptyLines        = "bottom_non_empty_lines"
	regionTopNonEmptyLines           = "top_non_empty_lines"
)

// validRegion reports whether a rule's region is one the engine computes.
func validRegion(spec string) bool {
	spec = strings.TrimSpace(spec)
	switch spec {
	case regionWholeRecent, regionAfterLastPromptMarker, regionBeforeCurrentPromptMarker,
		regionWithoutCurrentPromptMarker, regionCurrentPromptBlockMarker, regionAfterCurrentPromptBlock,
		regionPromptBoxBody, regionAbovePromptBox, regionLastNonEmptyAbovePromptBox,
		regionAfterLastHorizontalRule, regionOSCTitle, regionOSCProgress:
		return true
	}
	if _, ok := regionCount(spec, regionBottomLines); ok {
		return true
	}
	if _, ok := regionCount(spec, regionBottomNonEmptyLines); ok {
		return true
	}
	_, ok := topRegionCount(spec)
	return ok
}

// region slices the input the way a rule asks. The OSC regions read their
// own fields; everything else reads the screen.
func region(in Input, spec string) string {
	spec = strings.TrimSpace(spec)
	switch spec {
	case regionOSCTitle:
		return in.OSCTitle
	case regionOSCProgress:
		return in.OSCProgress
	}
	content := in.Screen
	switch spec {
	case regionWholeRecent:
		return content
	case regionAfterLastPromptMarker:
		return afterLastPromptMarker(content)
	case regionBeforeCurrentPromptMarker:
		return beforeCurrentPromptMarker(content)
	case regionWithoutCurrentPromptMarker:
		if _, ok := currentPromptIndex(splitLines(content)); ok {
			return ""
		}
		return content
	case regionCurrentPromptBlockMarker:
		return currentPromptBlockMarker(content)
	case regionAfterCurrentPromptBlock:
		return afterCurrentPromptBlockMarker(content)
	case regionPromptBoxBody:
		return promptBoxBody(content)
	case regionAbovePromptBox:
		return abovePromptBox(content)
	case regionLastNonEmptyAbovePromptBox:
		return lastNonEmptyLine(abovePromptBox(content))
	case regionAfterLastHorizontalRule:
		return afterLastHorizontalRule(content)
	}
	if n, ok := regionCount(spec, regionBottomLines); ok {
		return bottomLines(content, n)
	}
	if n, ok := regionCount(spec, regionBottomNonEmptyLines); ok {
		return bottomNonEmptyLines(content, n)
	}
	if n, ok := topRegionCount(spec); ok {
		return topNonEmptyLines(content, n)
	}
	return ""
}

// regionCount reads the count out of `name(N)`.
func regionCount(spec, name string) (int, bool) {
	rest, ok := strings.CutPrefix(spec, name)
	if !ok {
		return 0, false
	}
	rest, ok = strings.CutPrefix(rest, "(")
	if !ok {
		return 0, false
	}
	rest, ok = strings.CutSuffix(rest, ")")
	if !ok {
		return 0, false
	}
	n, err := strconv.Atoi(rest)
	if err != nil || n < 0 {
		return 0, false
	}
	return n, true
}

// topRegionCount is stricter, as upstream is: a canonical positive decimal,
// no leading zero, bounded.
func topRegionCount(spec string) (int, bool) {
	rest, ok := strings.CutPrefix(spec, regionTopNonEmptyLines)
	if !ok {
		return 0, false
	}
	rest, ok = strings.CutPrefix(rest, "(")
	if !ok {
		return 0, false
	}
	rest, ok = strings.CutSuffix(rest, ")")
	if !ok || rest == "" || rest[0] == '0' {
		return 0, false
	}
	for _, c := range rest {
		if c < '0' || c > '9' {
			return 0, false
		}
	}
	n, err := strconv.Atoi(rest)
	if err != nil || n > 65535 {
		return 0, false
	}
	return n, true
}

// splitLines splits the way Rust's `str::lines` does: on newlines, with a
// trailing newline ending the last line rather than starting an empty one.
func splitLines(content string) []string {
	if content == "" {
		return nil
	}
	content = strings.TrimSuffix(content, "\n")
	return strings.Split(content, "\n")
}

// lineStartOffset is the byte offset at which line index begins, capped at
// the end of the content. Every line but the last is followed by one
// newline byte, which is how the offsets are summed.
func lineStartOffset(content string, lines []string, index int) int {
	if index > len(lines) {
		index = len(lines)
	}
	offset := 0
	for _, line := range lines[:index] {
		offset += len(line) + 1
	}
	if offset > len(content) {
		offset = len(content)
	}
	return offset
}

func sliceFromLine(content string, lines []string, index int) string {
	return content[lineStartOffset(content, lines, index):]
}

func bottomLines(content string, count int) string {
	lines := splitLines(content)
	start := len(lines) - count
	if start < 0 {
		start = 0
	}
	return sliceFromLine(content, lines, start)
}

// bottomNonEmptyLines is the content from the count-th non-blank line from
// the bottom onward — blank lines between and after them included.
func bottomNonEmptyLines(content string, count int) string {
	lines := splitLines(content)
	seen := 0
	for i := len(lines) - 1; i >= 0; i-- {
		if strings.TrimSpace(lines[i]) == "" {
			continue
		}
		seen++
		if seen == count {
			return sliceFromLine(content, lines, i)
		}
	}
	if seen == 0 || count == 0 {
		return ""
	}
	// Fewer non-blank lines than asked: everything from the first of them.
	for i, line := range lines {
		if strings.TrimSpace(line) != "" {
			return sliceFromLine(content, lines, i)
		}
	}
	return ""
}

// topNonEmptyLines is the content up to and including the count-th non-blank
// line from the top.
func topNonEmptyLines(content string, count int) string {
	lines := splitLines(content)
	seen, end := 0, -1
	for i, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		seen++
		end = i
		if seen == count {
			break
		}
	}
	if end < 0 || count == 0 {
		return ""
	}
	return content[:lineStartOffset(content, lines, end+1)]
}

// The prompt marker and block markers are codex's: `›` opens its composer,
// and a response block leads with one of `•`, `■`, `✗`, `✓`.
func promptLine(line string) bool { return line == "›" || strings.HasPrefix(line, "› ") }

func blockMarkerLine(line string) bool {
	return strings.HasPrefix(line, "•") || strings.HasPrefix(line, "■") ||
		strings.HasPrefix(line, "✗") || strings.HasPrefix(line, "✓")
}

func lastIndex(lines []string, pred func(string) bool) (int, bool) {
	for i := len(lines) - 1; i >= 0; i-- {
		if pred(lines[i]) {
			return i, true
		}
	}
	return 0, false
}

func afterLastPromptMarker(content string) string {
	lines := splitLines(content)
	i, ok := lastIndex(lines, promptLine)
	if !ok {
		return content
	}
	return sliceFromLine(content, lines, i+1)
}

// currentPromptIndex is the last prompt marker, provided no response block
// has started below it — otherwise the prompt is a past one, not current.
func currentPromptIndex(lines []string) (int, bool) {
	i, ok := lastIndex(lines, promptLine)
	if !ok {
		return 0, false
	}
	for _, line := range lines[i+1:] {
		if blockMarkerLine(line) {
			return 0, false
		}
	}
	return i, true
}

func beforeCurrentPromptMarker(content string) string {
	lines := splitLines(content)
	i, ok := currentPromptIndex(lines)
	if !ok {
		return content
	}
	return content[:lineStartOffset(content, lines, i)]
}

func currentPromptBlockMarker(content string) string {
	lines := splitLines(content)
	prompt, ok := currentPromptIndex(lines)
	if !ok {
		return ""
	}
	i, ok := lastIndex(lines[:prompt], blockMarkerLine)
	if !ok {
		return ""
	}
	return lines[i]
}

func afterCurrentPromptBlockMarker(content string) string {
	lines := splitLines(content)
	prompt, ok := currentPromptIndex(lines)
	if !ok {
		return ""
	}
	i, ok := lastIndex(lines[:prompt], blockMarkerLine)
	if !ok {
		return ""
	}
	return sliceFromLine(content, lines, i)
}

// promptBoxTopBorderIndex finds the box claude draws around its composer: the
// second horizontal rule from the bottom is its top border.
func promptBoxTopBorderIndex(lines []string) (int, bool) {
	borders := 0
	for i := len(lines) - 1; i >= 0; i-- {
		if isHorizontalRule(lines[i]) {
			borders++
			if borders == 2 {
				return i, true
			}
		}
	}
	return 0, false
}

func promptBoxBody(content string) string {
	lines := splitLines(content)
	top, ok := promptBoxTopBorderIndex(lines)
	if !ok {
		return ""
	}
	start := lineStartOffset(content, lines, top+1)
	endIndex := len(lines)
	for i := top + 1; i < len(lines); i++ {
		if isHorizontalRule(lines[i]) {
			endIndex = i
			break
		}
	}
	end := lineStartOffset(content, lines, endIndex)
	if start > end {
		start = end
	}
	return content[start:end]
}

func abovePromptBox(content string) string {
	lines := splitLines(content)
	top, ok := promptBoxTopBorderIndex(lines)
	if !ok {
		return content
	}
	return content[:lineStartOffset(content, lines, top)]
}

func afterLastHorizontalRule(content string) string {
	lastRuleEnd, offset := 0, 0
	for _, line := range splitLines(content) {
		next := offset + len(line) + 1
		if isHorizontalRule(line) {
			lastRuleEnd = next
			if lastRuleEnd > len(content) {
				lastRuleEnd = len(content)
			}
		}
		offset = next
	}
	return content[lastRuleEnd:]
}

func lastNonEmptyLine(content string) string {
	lines := splitLines(content)
	for i := len(lines) - 1; i >= 0; i-- {
		if strings.TrimSpace(lines[i]) != "" {
			return lines[i]
		}
	}
	return ""
}

// isHorizontalRule recognises a line of box-drawing dashes: any run of `─`
// alone on the line, or three or more with a label after them, the way
// claude captions its composer border.
func isHorizontalRule(line string) bool {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" {
		return false
	}
	rest := trimmed
	dashes := 0
	for strings.HasPrefix(rest, "─") {
		rest = strings.TrimPrefix(rest, "─")
		dashes++
	}
	if dashes == 0 {
		return false
	}
	suffix := strings.TrimLeftFunc(rest, unicode.IsSpace)
	return suffix == "" || dashes >= 3
}
