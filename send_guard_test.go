package olympus

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/husniadil/olympus/backend"
)

// A composer screen, transcribed from a measured capture (see
// internal/agentstate/composer_test.go).
func composerScreen(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("internal", "agentstate", "testdata", "composer", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// paneRunning puts one pane in the fake's session "build", running command.
func paneRunning(f *fakeBackend, command string) {
	f.panes = []backend.Pane{{ID: "%1", SessionName: "build", SessionID: "$1", CurrentCommand: command}}
}

const shortBudget = 30 * time.Millisecond

// §7.5: an agent showing a permission prompt is refused before a keystroke.
func TestSendRefusesAnAgentWaitingOnAPerson(t *testing.T) {
	f := &fakeBackend{text: composerScreen(t, "permission.txt")}
	paneRunning(f, "claude")
	s := &Session{ol: fakeOlympus(f), name: "build"}

	err := s.Send(context.Background(), "ZEBRA-4417", VerifyBudget(shortBudget))
	if !errors.Is(err, ErrBlocked) {
		t.Fatalf("error is %v, want AGENT_BLOCKED", err)
	}
	if TypedOf(err) {
		t.Errorf("a refusal before typing is marked typed: %v", err)
	}
	if len(f.typed) != 0 || f.submits != 0 {
		t.Errorf("typed %d and submitted %d into a waiting agent, want nothing", len(f.typed), f.submits)
	}
}

// §7.6: the same text already in the transcript is not the echo. Before this
// rule it verified at once and the Enter submitted whatever the box held.
func TestSendDoesNotCountTheTranscriptAsTheEcho(t *testing.T) {
	f := &fakeBackend{text: composerScreen(t, "idle-earlier-text.txt")}
	paneRunning(f, "claude")
	s := &Session{ol: fakeOlympus(f), name: "build"}

	err := s.Send(context.Background(), "QUOKKA-MARBLE", VerifyBudget(shortBudget))
	if !errors.Is(err, ErrTimeout) {
		t.Fatalf("error is %v, want a timeout", err)
	}
	if f.submits != 0 {
		t.Errorf("submitted %d times on text only the transcript showed, want 0", f.submits)
	}
}

// §7.6: text that arrives in the box is the echo, and is submitted once.
func TestSendSubmitsOnceTheBoxHoldsTheText(t *testing.T) {
	f := &fakeBackend{text: composerScreen(t, "idle-earlier-text.txt")}
	f.onType = func(f *fakeBackend, _ string) { f.text = composerScreen(t, "idle-draft.txt") }
	paneRunning(f, "claude")
	s := &Session{ol: fakeOlympus(f), name: "build"}

	if err := s.Send(context.Background(), "draft words sitting here", VerifyBudget(shortBudget)); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if len(f.typed) != 1 || f.submits != 1 {
		t.Errorf("typed %d and submitted %d, want one of each", len(f.typed), f.submits)
	}
}

// §7.6: a text the agent collapsed into a paste placeholder cannot be matched,
// and a placeholder that was not there before typing is its echo. Before this
// rule a send of about 3,000 characters timed out and its resend left the
// text in the box twice (measured on Claude Code 2.1.274).
func TestSendTakesANewPastePlaceholderAsTheEcho(t *testing.T) {
	f := &fakeBackend{text: composerScreen(t, "idle-earlier-text.txt")}
	f.onType = func(f *fakeBackend, _ string) { f.text = composerScreen(t, "pasted.txt") }
	paneRunning(f, "claude")
	s := &Session{ol: fakeOlympus(f), name: "build"}

	if err := s.Send(context.Background(), strings.Repeat("word ", 600), VerifyBudget(shortBudget)); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if len(f.typed) != 1 || f.submits != 1 {
		t.Errorf("typed %d and submitted %d, want one of each", len(f.typed), f.submits)
	}
}

// §7.6: an agent that draws no box is read on the whole screen, and Codex's
// placeholder is the echo there. Before the rule a send of 2,000 characters
// timed out and left the paste twice (measured on codex-cli 0.154.0).
func TestSendTakesCodexsPastePlaceholderAsTheEcho(t *testing.T) {
	f := &fakeBackend{text: composerScreen(t, "codex-idle.txt")}
	f.onType = func(f *fakeBackend, _ string) { f.text = composerScreen(t, "codex-pasted.txt") }
	paneRunning(f, "codex")
	s := &Session{ol: fakeOlympus(f), name: "build"}

	if err := s.Send(context.Background(), strings.Repeat("word ", 400), VerifyBudget(shortBudget)); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if len(f.typed) != 1 || f.submits != 1 {
		t.Errorf("typed %d and submitted %d, want one of each", len(f.typed), f.submits)
	}
}

// §7.6: placeholders already in the box before typing are not the echo.
func TestSendDoesNotTakeAnOldPastePlaceholderAsTheEcho(t *testing.T) {
	f := &fakeBackend{text: composerScreen(t, "pasted.txt")}
	paneRunning(f, "claude")
	s := &Session{ol: fakeOlympus(f), name: "build"}

	err := s.Send(context.Background(), strings.Repeat("word ", 600), VerifyBudget(shortBudget))
	if !errors.Is(err, ErrTimeout) {
		t.Fatalf("error is %v, want a timeout", err)
	}
	if f.submits != 0 {
		t.Errorf("submitted %d times on placeholders that were already there, want 0", f.submits)
	}
}

// §7.5: an agent that draws a box and shows none has something else open, and
// the send is refused before typing. Measured on Claude Code 2.1.274: with its
// rewind list open, a send was reported delivered, matched against the list.
func TestSendRefusesAnAgentShowingNoInputBox(t *testing.T) {
	for _, screen := range []string{"rewind.txt", "model-picker.txt"} {
		f := &fakeBackend{text: composerScreen(t, screen)}
		paneRunning(f, "claude")
		s := &Session{ol: fakeOlympus(f), name: "build"}

		err := s.Send(context.Background(), "word word word", VerifyBudget(shortBudget))
		if !errors.Is(err, ErrBlocked) || TypedOf(err) {
			t.Fatalf("%s: error is %v, want an untyped AGENT_BLOCKED", screen, err)
		}
		if len(f.typed) != 0 || f.submits != 0 {
			t.Errorf("%s: typed %d and submitted %d, want nothing", screen, len(f.typed), f.submits)
		}
	}
}

// §7.6: a box that goes while the echo is polled for is not replaced by the
// whole screen, which may hold the same words in an overlay's list.
func TestSendDoesNotMatchAnOverlayOpenedMidDelivery(t *testing.T) {
	f := &fakeBackend{text: composerScreen(t, "idle-earlier-text.txt")}
	f.onType = func(f *fakeBackend, _ string) { f.text = composerScreen(t, "rewind.txt") }
	paneRunning(f, "claude")
	s := &Session{ol: fakeOlympus(f), name: "build"}

	err := s.Send(context.Background(), "word word word", VerifyBudget(shortBudget))
	if !errors.Is(err, ErrTimeout) {
		t.Fatalf("error is %v, want a timeout", err)
	}
	if f.submits != 0 {
		t.Errorf("submitted %d times into an overlay, want 0", f.submits)
	}
}

// §7.5: a question that opens while the echo is polled for stops the send. Its
// option holds the sent text, which a match on the box alone would count.
func TestSendStopsWhenAQuestionOpensMidDelivery(t *testing.T) {
	f := &fakeBackend{text: composerScreen(t, "idle-earlier-text.txt")}
	f.onType = func(f *fakeBackend, _ string) { f.text = composerScreen(t, "question.txt") }
	paneRunning(f, "claude")
	s := &Session{ol: fakeOlympus(f), name: "build"}

	err := s.Send(context.Background(), "Red", VerifyBudget(shortBudget))
	if !errors.Is(err, ErrBlocked) {
		t.Fatalf("error is %v, want AGENT_BLOCKED", err)
	}
	if len(f.typed) != 1 || f.submits != 0 {
		t.Errorf("typed %d and submitted %d, want one send and no terminator", len(f.typed), f.submits)
	}
	// The text was typed, so the refusal must not tell the caller it was not.
	if !TypedOf(err) {
		t.Errorf("a refusal after typing is not marked typed: %v", err)
	}
	if strings.Contains(err.Error(), "nothing was typed") {
		t.Errorf("the error says nothing was typed after one send: %v", err)
	}
}

// §7.6 scope: a pane with no known agent keeps the whole-screen match, and a
// text already on its screen verifies as it always has.
func TestSendToAShellKeepsTheWholeScreenMatch(t *testing.T) {
	f := &fakeBackend{text: "$ make build\nok\n$ "}
	paneRunning(f, "zsh")
	s := &Session{ol: fakeOlympus(f), name: "build"}

	if err := s.Send(context.Background(), "make build", VerifyBudget(shortBudget)); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if f.submits != 1 {
		t.Errorf("submitted %d times, want 1", f.submits)
	}
}

// §7.5: an atomic send is text and its terminator in one write, so into a
// waiting agent it is an answer with no chance to notice first. It is refused
// the same way, before anything is written.
func TestSendAtomicRefusesAnAgentWaitingOnAPerson(t *testing.T) {
	f := &fakeBackend{text: composerScreen(t, "question.txt")}
	paneRunning(f, "claude")
	s := &Session{ol: fakeOlympus(f), name: "build"}

	if err := s.SendAtomic(context.Background(), "Red"); !errors.Is(err, ErrBlocked) {
		t.Fatalf("error is %v, want AGENT_BLOCKED", err)
	}
	if f.atomic != 0 {
		t.Errorf("wrote %d atomic sends into a waiting agent, want 0", f.atomic)
	}
}

// listingFake is the fake with an agent listing of its own, as a backend that
// detects agents natively has.
type listingFake struct {
	*fakeBackend
	rows []backend.Agent
}

func (l *listingFake) Agents(context.Context) ([]backend.Agent, error) { return l.rows, nil }

// §7.5 scope: on a backend that lists agents itself, a pane target is read
// for the agent in that pane only. An agent in a sibling pane of the same
// session is not on the screen the send reads, and must not refuse it.
func TestSendReadsOnlyTheTargetPanesAgent(t *testing.T) {
	rows := []backend.Agent{{PaneID: "w1:p3", SessionName: "work", SessionID: "w1", Agent: "claude"}}

	t.Run("a shell pane beside the agent", func(t *testing.T) {
		// The shell printed a capture of a prompt; the agent is elsewhere.
		f := &fakeBackend{text: composerScreen(t, "permission.txt")}
		f.onType = func(f *fakeBackend, text string) { f.text += "\n$ " + text }
		f.panes = []backend.Pane{{ID: "w1:p2", SessionName: "work", SessionID: "w1", CurrentCommand: "zsh"}}
		s := &Session{ol: &Olympus{backend: &listingFake{f, rows}}, name: "w1:p2"}

		if err := s.Send(context.Background(), "make build", VerifyBudget(shortBudget)); err != nil {
			t.Fatalf("Send to the shell pane: %v", err)
		}
		if f.submits != 1 {
			t.Errorf("submitted %d times, want 1", f.submits)
		}
	})

	t.Run("the agent's own pane", func(t *testing.T) {
		f := &fakeBackend{text: composerScreen(t, "permission.txt")}
		f.panes = []backend.Pane{{ID: "w1:p3", SessionName: "work", SessionID: "w1", CurrentCommand: "claude"}}
		s := &Session{ol: &Olympus{backend: &listingFake{f, rows}}, name: "w1:p3"}

		if err := s.Send(context.Background(), "yes", VerifyBudget(shortBudget)); !errors.Is(err, ErrBlocked) {
			t.Fatalf("error is %v, want AGENT_BLOCKED", err)
		}
	})
}
