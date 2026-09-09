package agentstate

import "testing"

// Two screens as Claude Code drew them under herdr, measured 2026-09-05: the
// agent waiting on a question, and the agent working with its composer and a
// four-line status area at the bottom.
const blockedScreen = `     View Observations Live @ http://localhost:37701

❯ Use the AskUserQuestion tool to ask me whether I prefer tea or coffee. Do nothing else.
────────────────────────────────────────────
 ☐ Minuman

Lo lebih suka teh atau kopi?

❯ 1. Teh
     Tea
  2. Kopi
     Coffee
  3. Type something.
────────────────────────────────────────────
  4. Chat about this

Enter to select · ↑/↓ to navigate · Esc to cancel
`

const workingScreen = `  Jadi herdr menyediakan sensor, agamemnon yang perlu menyediakan bel.

──────────────────────────────────────────── terminal library ─
❯
────────────────────────────────────────────
  MacBookPro · ~/github.com/husniadil/agent-teams              /rc
  husniadil/agent-teams · main ✓ · +7635/−1007
  Claude Fable 5.1 · medium · 19% · $ 847.22 · v2.1.259
  ⏵⏵ bypass permissions on (shift+tab to cycle)
`

func TestLineTakesTheQuestionAWaitingAgentAsked(t *testing.T) {
	if got := Line(Input{Screen: blockedScreen}, Blocked); got != "Lo lebih suka teh atau kopi?" {
		t.Errorf("blocked line: %q", got)
	}
}

func TestLineCutsTheComposerAndTheStatusArea(t *testing.T) {
	want := "Jadi herdr menyediakan sensor, agamemnon yang perlu menyediakan bel."
	if got := Line(Input{Screen: workingScreen}, Working); got != want {
		t.Errorf("working line: %q", got)
	}
}

func TestLineFallsBackWhereAWaitingAgentAsksNothing(t *testing.T) {
	got := Line(Input{Screen: "done\nPress y to continue\n"}, Blocked)
	if got != "Press y to continue" {
		t.Errorf("blocked without a question: %q", got)
	}
}

func TestLineDropsTheClientsOwnOverlay(t *testing.T) {
	got := Line(Input{Screen: "⏺ Done.  1 new message (click) ↓\n"}, Working)
	if got != "⏺ Done." {
		t.Errorf("overlay not dropped: %q", got)
	}
}

func TestLineIsEmptyWhereTheScreenSaysNothing(t *testing.T) {
	for _, screen := range []string{"", "\n\n───\n"} {
		if got := Line(Input{Screen: screen}, Idle); got != "" {
			t.Errorf("screen %q: %q", screen, got)
		}
	}
}
