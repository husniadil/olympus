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
