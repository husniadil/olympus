package engine_test

import (
	"strings"
	"testing"

	"github.com/husniadil/olympus/internal/engine"
)

func TestNormalizeAbsorbsRenderingNoise(t *testing.T) {
	cases := []struct{ in, want string }{
		{"make build", "makebuild"},
		{"MAKE Build", "makebuild"},
		{"$ make build", "makebuild"},
		{"│ make build │", "makebuild"},
		{"  make\tbuild  ", "makebuild"},
		{"abcdefghijklmnopqrstuvwxyz0123456789", "abcdefghijklmnopqrstuvwx"},
		{"", ""},
	}
	for _, c := range cases {
		if got := engine.Normalize(c.in); got != c.want {
			t.Errorf("Normalize(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestScreenContainsFindsTextOnItsOwnLine(t *testing.T) {
	screen := "$ ls\nfile.txt\n$ make build\n"
	if !engine.ScreenContains(screen, engine.Normalize("make build")) {
		t.Error("the text was not found on the line it is on")
	}
}

// The failure this guards: a prompt banner burns through the cap before the
// pane reaches the line the text is on. Normalizing the whole screen as one
// blob truncates at the banner and discards that line entirely, timing out with
// the answer visible one line down.
func TestABannerAboveTheTextDoesNotHideIt(t *testing.T) {
	banner := "wwwwwwwwwwwwwwwwwwwwwwwwwwwwwwwwwwwwwwwwwwww"
	screen := banner + "\n$ make build\n"

	if !engine.ScreenContains(screen, engine.Normalize("make build")) {
		t.Error("a long banner above the text hid it, so matching is not per line")
	}
	// The same content as one line genuinely is out of reach, which is what
	// makes the per-line rule load-bearing rather than incidental.
	if engine.ScreenContains(banner+"$ make build", engine.Normalize("make build")) {
		t.Error("the cap is not being applied at all")
	}
}

// A needle whose echo straddles the pane's width comes back split by a newline
// that cannot be told from a real one, on a backend with no rejoin.
func TestTextSplitAcrossAWrapIsStillFound(t *testing.T) {
	screen := "$ make bu\nild now\n"
	if !engine.ScreenContains(screen, engine.Normalize("make build")) {
		t.Error("text split across a wrap boundary was not found")
	}
}

func TestUnrelatedScreenDoesNotMatch(t *testing.T) {
	if engine.ScreenContains("$ ls\nfile.txt\n", engine.Normalize("make build")) {
		t.Error("unrelated text matched")
	}
}

// An empty needle has nothing to look for, and must not make callers special-
// case it into a false negative.
func TestAnEmptyNeedleMatches(t *testing.T) {
	if !engine.ScreenContains("anything", "") {
		t.Error("an empty needle did not match")
	}
}

// Two boundaries would need a pane narrower than the normalized needle, and
// sub-24-column panes are not a supported target. Recorded so the limit is a
// known one rather than a surprise.
func TestSplittingAcrossTwoBoundariesIsNotCovered(t *testing.T) {
	screen := "$ make\n bui\nld now\n"
	if engine.ScreenContains(screen, engine.Normalize("make build")) {
		t.Error("a two-boundary split matched, which the pair check cannot actually cover — the scope caveat is wrong")
	}
}

func TestNormalizedNeedleIsWhatIsSearchedFor(t *testing.T) {
	// The caller normalizes once and reuses it, so a raw needle must not
	// accidentally work — that would mean the cap was never applied to it.
	raw := "MAKE BUILD"
	if engine.ScreenContains("$ make build", raw) {
		t.Error("an un-normalized needle matched, so normalization is being applied inside the search")
	}
	if !engine.ScreenContains("$ make build", engine.Normalize(raw)) {
		t.Error("the normalized needle did not match")
	}
	if strings.ContainsAny(engine.Normalize(raw), " ") {
		t.Error("normalization left whitespace behind")
	}
}
