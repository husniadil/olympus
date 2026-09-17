package agentstate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The screens under testdata/composer are transcribed from captures of a
// Claude Code 2.1.273 pane on herdr, taken while measuring behavior §7.5 and
// §7.6. Paths, the session name and the status line are shortened; the lines
// the manifest reads are as captured.
func composerScreen(t *testing.T, name string) Input {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "composer", name))
	if err != nil {
		t.Fatal(err)
	}
	return Input{Screen: string(b)}
}

// §7.6: the box is read for an agent whose manifest names one, and its text is
// what was typed into it, not what the transcript above it repeats.
func TestComposerReadsTheBoxAnAgentDraws(t *testing.T) {
	t.Parallel()
	cases := []struct {
		screen      string
		state       State
		drawn       bool
		holds       string
		doesNotHold string
	}{
		{screen: "idle-earlier-text.txt", state: Idle, drawn: true, doesNotHold: "QUOKKA-MARBLE"},
		{screen: "idle-draft.txt", state: Idle, drawn: true, holds: "draft words sitting here"},
		{screen: "working-typed.txt", state: Working, drawn: true, holds: "WORKING-OCELOT typed while busy"},
		{screen: "long-scrolled.txt", state: Idle, drawn: true, holds: "LONGTAIL-PANGOLIN"},
		// A permission prompt replaces the box: nothing is drawn where input lands.
		{screen: "permission.txt", state: Blocked, drawn: false},
		// A question fills the box's region, so a box is read, and only the
		// blocked state says it is not one (§7.5).
		{screen: "question.txt", state: Blocked, drawn: true, holds: "Red"},
	}
	for _, c := range cases {
		t.Run(c.screen, func(t *testing.T) {
			t.Parallel()
			in := composerScreen(t, c.screen)
			if got := Detect("claude", in); got != c.state {
				t.Errorf("Detect = %s, want %s", got, c.state)
			}
			text, drawn := Composer("claude", in)
			if drawn != c.drawn {
				t.Fatalf("drawn = %v, want %v (box %q)", drawn, c.drawn, text)
			}
			if c.holds != "" && !strings.Contains(text, c.holds) {
				t.Errorf("box %q does not hold %q", text, c.holds)
			}
			if c.doesNotHold != "" && strings.Contains(text, c.doesNotHold) {
				t.Errorf("box %q holds %q, which only the transcript shows", text, c.doesNotHold)
			}
		})
	}
}

// §7.6 scope: no box is read where the manifest names none, where there is no
// manifest, or where the screen draws no box.
func TestComposerIsNotDrawnWhereNoBoxIsNamed(t *testing.T) {
	t.Parallel()
	box := composerScreen(t, "idle-draft.txt")
	if _, drawn := Composer("codex", box); drawn {
		t.Error("a box was read for an agent whose manifest names none")
	}
	if _, drawn := Composer("no-such-agent", box); drawn {
		t.Error("a box was read for an agent with no manifest")
	}
	if _, drawn := Composer("claude", Input{Screen: "$ make build\nok\n$ "}); drawn {
		t.Error("a box was read off a shell's screen")
	}
}

// §7.6: Claude Code's paste placeholders, counted in the box they sit in.
func TestPastesCountsThePlaceholdersInTheBox(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("testdata", "composer", "pasted.txt"))
	if err != nil {
		t.Fatal(err)
	}
	box, drawn := Composer("claude", Input{Screen: string(b)})
	if !drawn {
		t.Fatal("no box read off the pasted screen")
	}
	if got := Pastes(box); got != 3 {
		t.Errorf("Pastes = %d, want 3 in %q", got, box)
	}
	if got := Pastes("›⠁[Pasted Content 2000 chars]"); got != 1 {
		t.Errorf("Pastes on Codex's placeholder = %d, want 1", got)
	}
	if got := Pastes("a draft with no placeholder"); got != 0 {
		t.Errorf("Pastes on plain text = %d, want 0", got)
	}
}
