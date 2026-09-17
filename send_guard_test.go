package olympus

import (
	"context"
	"errors"
	"os"
	"path/filepath"
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
