package olympus

import (
	"regexp"
	"strings"
	"testing"
)

// behavior §7.3.1: a caller's pattern must not assume a prompt starts a line,
// and the trap is invisible from the failure.
//
// A timeout says the pattern did not appear. That is true and useless when the
// text IS on screen with something in front of it — which is what a
// cursor-addressing program produces when it paints over the row the shell's
// echo is on. The caller sees their prompt in the capture, sees `^>>>\s*$`
// timing out against it, and has no way to tell that the anchor is the reason.
//
// It cost four rounds of a full suite under load to work out once. The failure
// should carry the answer rather than making everyone rediscover it.
func TestATimeoutExplainsWhenOnlyTheLineAnchorPreventedTheMatch(t *testing.T) {
	screen := "$ /usr/bin/python3 -q>>> \n"
	hint := anchorHint(regexp.MustCompile(`^>>>\s*$`), screen)

	if hint == "" {
		t.Fatal("a pattern that matches except for its anchors produced no hint")
	}
	if !strings.Contains(hint, "anchor") {
		t.Errorf("the hint does not name the cause: %q", hint)
	}
}

// The hint must stay rare. Offering it when the text is genuinely absent turns
// every ordinary timeout into a wrong guess about the caller's pattern, which is
// worse than saying nothing: it sends them to edit a pattern that was correct.
func TestNoHintWhenTheTextIsSimplyNotThere(t *testing.T) {
	if hint := anchorHint(regexp.MustCompile(`^>>>\s*$`), "$ ls\nfile.txt\n"); hint != "" {
		t.Errorf("a genuinely absent pattern produced a hint: %q", hint)
	}
}

// An unanchored pattern that fails has nothing to do with anchoring, so there is
// nothing to say.
func TestNoHintForAPatternThatIsNotAnchored(t *testing.T) {
	if hint := anchorHint(regexp.MustCompile(`never-appears`), "$ ls\n"); hint != "" {
		t.Errorf("an unanchored pattern produced an anchor hint: %q", hint)
	}
}

// A pattern anchored at only one end is still anchored, and hits the same trap:
// `^>>>` fails against a prompt with a prefix exactly as `^>>>\s*$` does.
func TestAHintForAPatternAnchoredAtOneEndOnly(t *testing.T) {
	if hint := anchorHint(regexp.MustCompile(`^>>>`), "$ python3 -q>>> \n"); hint == "" {
		t.Error("a start-anchored pattern produced no hint")
	}
}

// The anchor precondition has to be load-bearing, and this is the case that
// makes it so: a pattern with NO anchors can still match the screen as one
// string while matching no single line, because a match that spans a newline is
// a match against the blob and never against a line.
//
// Without the precondition, that timeout would be blamed on anchors the pattern
// does not have — sending the caller to remove something that is not there. It
// was found by mutation: deleting the precondition left every other case green.
func TestNoAnchorHintWhenTheMatchMerelySpansLines(t *testing.T) {
	screen := "$ ls\nfile.txt\n"
	spanning := regexp.MustCompile(`ls\nfile`)

	if !spanning.MatchString(screen) {
		t.Fatal("the fixture does not span lines, so it cannot test what it claims")
	}
	if hint := anchorHint(spanning, screen); hint != "" {
		t.Errorf("an unanchored, line-spanning pattern was blamed on anchors: %q", hint)
	}
}
