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

// §7.5: a box read as missing is redrawn before the send is refused. Claude
// Code 2.1.274 once drew its input line over the box's bottom rule, and every
// send to it was refused until something made it draw again (measured).
func TestSendAsksForARedrawBeforeRefusingAMissingBox(t *testing.T) {
	f := &fakeBackend{text: composerScreen(t, "misdrawn.txt")}
	f.onRedraw = func(f *fakeBackend) { f.text = composerScreen(t, "idle-earlier-text.txt") }
	f.onType = func(f *fakeBackend, _ string) { f.text = composerScreen(t, "idle-draft.txt") }
	paneRunning(f, "claude")
	s := &Session{ol: fakeOlympus(f), name: "build"}

	if err := s.Send(context.Background(), "draft words sitting here", VerifyBudget(shortBudget)); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if f.redraws != 1 || len(f.typed) != 1 || f.submits != 1 {
		t.Errorf("redrew %d, typed %d and submitted %d, want one of each", f.redraws, len(f.typed), f.submits)
	}
}

// §7.5: the same when the box is drawn wrong after the text was typed, which
// is how it was caught: a redraw shows the text in the box, and it is
// submitted once, never typed again.
func TestSendAsksForARedrawWhenTheBoxGoesMidDelivery(t *testing.T) {
	f := &fakeBackend{text: composerScreen(t, "idle-earlier-text.txt")}
	f.onType = func(f *fakeBackend, _ string) { f.text = composerScreen(t, "misdrawn.txt") }
	f.onRedraw = func(f *fakeBackend) { f.text = composerScreen(t, "misdrawn-redrawn.txt") }
	paneRunning(f, "claude")
	s := &Session{ol: fakeOlympus(f), name: "build"}

	if err := s.Send(context.Background(), "Reply with the single word ok.", VerifyBudget(shortBudget)); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if f.redraws != 1 || len(f.typed) != 1 || f.submits != 1 {
		t.Errorf("redrew %d, typed %d and submitted %d, want one of each", f.redraws, len(f.typed), f.submits)
	}
}

// §7.5: a redraw that shows a prompt drawn wrong is refused as the prompt,
// before typing: a question reads as a box, and only its blocked state says
// it is not one.
func TestSendRefusesAPromptARedrawShows(t *testing.T) {
	f := &fakeBackend{text: composerScreen(t, "misdrawn.txt")}
	f.onRedraw = func(f *fakeBackend) { f.text = composerScreen(t, "question.txt") }
	paneRunning(f, "claude")
	s := &Session{ol: fakeOlympus(f), name: "build"}

	err := s.Send(context.Background(), "word word word", VerifyBudget(shortBudget))
	if !errors.Is(err, ErrBlocked) || TypedOf(err) || f.redraws != 1 || len(f.typed) != 0 {
		t.Fatalf("error %v, redrew %d, typed %d; want an untyped AGENT_BLOCKED after one redraw and nothing typed", err, f.redraws, len(f.typed))
	}
}

// §7.5: a box drawn wrong twice in one delivery is redrawn twice, and the
// text is submitted once.
func TestSendRedrawsABoxDrawnWrongTwice(t *testing.T) {
	f := &fakeBackend{text: composerScreen(t, "idle-earlier-text.txt")}
	f.onType = func(f *fakeBackend, _ string) { f.text = composerScreen(t, "misdrawn.txt") }
	f.onRedraw = func(f *fakeBackend) {
		if f.redraws == 1 {
			f.text = composerScreen(t, "idle-earlier-text.txt")
			// The redraw's own read finds the box, and the capture after it
			// finds it drawn wrong again.
			captures := 0
			f.onScreen = func(f *fakeBackend) {
				if captures++; captures == 2 {
					f.onScreen = nil
					f.text = composerScreen(t, "misdrawn.txt")
				}
			}
			return
		}
		f.text = composerScreen(t, "misdrawn-redrawn.txt")
	}
	paneRunning(f, "claude")
	s := &Session{ol: fakeOlympus(f), name: "build"}

	if err := s.Send(context.Background(), "Reply with the single word ok.", VerifyBudget(time.Second)); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if f.redraws != 2 || len(f.typed) != 1 || f.submits != 1 {
		t.Errorf("redrew %d, typed %d and submitted %d, want two redraws, one send and one terminator", f.redraws, len(f.typed), f.submits)
	}
}

// §7.5: an overlay is still there after a redraw, so the send is refused as
// before, before typing or typed.
func TestSendRefusesAnOverlayARedrawDoesNotClear(t *testing.T) {
	f := &fakeBackend{text: composerScreen(t, "rewind.txt")}
	f.onRedraw = func(*fakeBackend) {}
	paneRunning(f, "claude")
	s := &Session{ol: fakeOlympus(f), name: "build"}
	err := s.Send(context.Background(), "word word word", VerifyBudget(shortBudget))
	if !errors.Is(err, ErrBlocked) || TypedOf(err) || f.redraws != 1 || len(f.typed) != 0 {
		t.Fatalf("before typing: error %v, redrew %d, typed %d; want an untyped AGENT_BLOCKED after one redraw and nothing typed", err, f.redraws, len(f.typed))
	}

	f = &fakeBackend{text: composerScreen(t, "idle-earlier-text.txt")}
	f.onType = func(f *fakeBackend, _ string) { f.text = composerScreen(t, "rewind.txt") }
	f.onRedraw = func(*fakeBackend) {}
	paneRunning(f, "claude")
	s = &Session{ol: fakeOlympus(f), name: "build"}
	err = s.Send(context.Background(), "word word word", VerifyBudget(shortBudget))
	if !errors.Is(err, ErrBlocked) || !TypedOf(err) || f.redraws != 1 || len(f.typed) != 1 || f.submits != 0 {
		t.Fatalf("mid-delivery: error %v, redrew %d, typed %d, submitted %d; want a typed AGENT_BLOCKED after one redraw, one send and no terminator", err, f.redraws, len(f.typed), f.submits)
	}
}

// §7.5: a box that goes while the echo is polled for stops the send, typed,
// with no resend: the whole screen may hold the same words in an overlay's
// list, and a resend would type into it.
func TestSendStopsWhenAnOverlayOpensMidDelivery(t *testing.T) {
	f := &fakeBackend{text: composerScreen(t, "idle-earlier-text.txt")}
	f.onType = func(f *fakeBackend, _ string) { f.text = composerScreen(t, "rewind.txt") }
	paneRunning(f, "claude")
	s := &Session{ol: fakeOlympus(f), name: "build"}

	err := s.Send(context.Background(), "word word word", VerifyBudget(shortBudget))
	if !errors.Is(err, ErrBlocked) || !TypedOf(err) {
		t.Fatalf("error is %v, want a typed AGENT_BLOCKED", err)
	}
	if len(f.typed) != 1 || f.submits != 0 {
		t.Errorf("typed %d and submitted %d, want one send and no terminator", len(f.typed), f.submits)
	}
}

// §7.6: on a backend whose capture is its scrollback, the box is read off the
// whole capture. A draft taller than the detection tail pushed the box's top
// rule out of it, and a normal screen was refused.
func TestSendReadsTheBoxOffTheWholeScrollback(t *testing.T) {
	screen := composerScreen(t, "idle-earlier-text.txt")
	draft := strings.Repeat("a line of the draft\n", 30)
	f := &fakeBackend{text: screen}
	f.caps.NativeScrollback = true
	f.onType = func(f *fakeBackend, _ string) {
		f.text = strings.Replace(screen, "❯\n", "❯ "+draft+"the tall draft ends here\n", 1)
	}
	paneRunning(f, "claude")
	s := &Session{ol: fakeOlympus(f), name: "build"}

	if err := s.Send(context.Background(), "the tall draft ends here", VerifyBudget(shortBudget)); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if f.submits != 1 {
		t.Errorf("submitted %d, want 1", f.submits)
	}
}

// §7.6: a paste that arrives in pieces is taken once its placeholders stop
// growing, so the Enter does not land among the pieces.
func TestSendWaitsForAPasteToStopArriving(t *testing.T) {
	f := &fakeBackend{text: composerScreen(t, "idle-earlier-text.txt")}
	pasted := composerScreen(t, "pasted.txt")
	one := strings.Replace(pasted, "[Pasted text #60][Pasted text #61][Pasted text #62]", "[Pasted text #60]", 1)
	captures := 0
	f.onType = func(f *fakeBackend, _ string) {
		f.onScreen = func(f *fakeBackend) {
			captures++
			if captures == 1 {
				f.text = one
			} else {
				f.text = pasted
			}
		}
	}
	paneRunning(f, "claude")
	s := &Session{ol: fakeOlympus(f), name: "build"}

	if err := s.Send(context.Background(), strings.Repeat("word ", 600), VerifyBudget(shortBudget)); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if f.submits != 1 || captures < 3 {
		t.Errorf("submitted %d after %d captures, want one after the count held", f.submits, captures)
	}
}

// §7.6: Codex's placeholder counts only when it names this text's length, so
// an older paste leaving the screen or a quoted placeholder is not the echo.
func TestSendTakesOnlyCodexsPlaceholderOfThisLength(t *testing.T) {
	quoted := strings.Replace(composerScreen(t, "codex-idle.txt"), "› Ask Codex", "• see [Pasted Content 1999 chars] in the log\n\n› Ask Codex", 1)
	f := &fakeBackend{text: quoted}
	f.onType = func(f *fakeBackend, _ string) {
		f.text = strings.Replace(quoted, "› Ask Codex to do anything", "› [Pasted Content 1999 chars]", 1)
	}
	paneRunning(f, "codex")
	s := &Session{ol: fakeOlympus(f), name: "build"}

	err := s.Send(context.Background(), strings.Repeat("w", 2000), VerifyBudget(shortBudget))
	if !errors.Is(err, ErrTimeout) {
		t.Fatalf("error is %v, want a timeout: no placeholder names 2000 characters", err)
	}
	if f.submits != 0 {
		t.Errorf("submitted %d on another length's placeholder, want 0", f.submits)
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
