package olympus

import (
	"context"
	"errors"
	"fmt"
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
	if got := ReasonOf(err); got != "" {
		t.Errorf("a refusal for a person has reason %q, want none", got)
	}
	if f.sends() != 0 || f.submits != 0 {
		t.Errorf("typed %d and submitted %d into a waiting agent, want nothing", f.sends(), f.submits)
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
	if f.sends() != 1 || f.submits != 1 {
		t.Errorf("typed %d and submitted %d, want one of each", f.sends(), f.submits)
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
	if f.sends() != 1 || f.submits != 1 {
		t.Errorf("typed %d and submitted %d, want one of each", f.sends(), f.submits)
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
	if f.sends() != 1 || f.submits != 1 {
		t.Errorf("typed %d and submitted %d, want one of each", f.sends(), f.submits)
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
		if !errors.Is(err, ErrBlocked) || TypedOf(err) || ReasonOf(err) != ReasonNoInputBox {
			t.Fatalf("%s: error is %v, want an untyped AGENT_BLOCKED for no input box", screen, err)
		}
		if f.sends() != 0 || f.submits != 0 {
			t.Errorf("%s: typed %d and submitted %d, want nothing", screen, f.sends(), f.submits)
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
	if f.redraws != 1 || f.sends() != 1 || f.submits != 1 {
		t.Errorf("redrew %d, typed %d and submitted %d, want one of each", f.redraws, f.sends(), f.submits)
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
	if f.redraws != 1 || f.sends() != 1 || f.submits != 1 {
		t.Errorf("redrew %d, typed %d and submitted %d, want one of each", f.redraws, f.sends(), f.submits)
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
	if !errors.Is(err, ErrBlocked) || TypedOf(err) || f.redraws != 1 || f.sends() != 0 {
		t.Fatalf("error %v, redrew %d, typed %d; want an untyped AGENT_BLOCKED after one redraw and nothing typed", err, f.redraws, f.sends())
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
	if f.redraws != 2 || f.sends() != 1 || f.submits != 1 {
		t.Errorf("redrew %d, typed %d and submitted %d, want two redraws, one send and one terminator", f.redraws, f.sends(), f.submits)
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
	if !errors.Is(err, ErrBlocked) || TypedOf(err) || f.redraws != 1 || f.sends() != 0 {
		t.Fatalf("before typing: error %v, redrew %d, typed %d; want an untyped AGENT_BLOCKED after one redraw and nothing typed", err, f.redraws, f.sends())
	}

	f = &fakeBackend{text: composerScreen(t, "idle-earlier-text.txt")}
	f.onType = func(f *fakeBackend, _ string) { f.text = composerScreen(t, "rewind.txt") }
	f.onRedraw = func(*fakeBackend) {}
	paneRunning(f, "claude")
	s = &Session{ol: fakeOlympus(f), name: "build"}
	err = s.Send(context.Background(), "word word word", VerifyBudget(shortBudget))
	if !errors.Is(err, ErrBlocked) || !TypedOf(err) || f.redraws != 1 || f.sends() != 1 || f.submits != 0 {
		t.Fatalf("mid-delivery: error %v, redrew %d, typed %d, submitted %d; want a typed AGENT_BLOCKED after one redraw, one send and no terminator", err, f.redraws, f.sends(), f.submits)
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
	if !errors.Is(err, ErrBlocked) || !TypedOf(err) || ReasonOf(err) != ReasonNoInputBox {
		t.Fatalf("error is %v, want a typed AGENT_BLOCKED for no input box", err)
	}
	if f.sends() != 1 || f.submits != 0 {
		t.Errorf("typed %d and submitted %d, want one send and no terminator", f.sends(), f.submits)
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

	// A window long enough for the count to rise twice and then hold. A
	// shorter one ends while the pieces still arrive, which is a send that
	// fails as incomplete.
	if err := s.Send(context.Background(), strings.Repeat("word ", 600), VerifyBudget(time.Second)); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if f.sends() != 1 || f.submits != 1 || captures < 3 {
		t.Errorf("sent %d and submitted %d after %d captures, want one of each after the count held", f.sends(), f.submits, captures)
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
	if f.sends() != 1 || f.submits != 0 {
		t.Errorf("typed %d and submitted %d, want one send and no terminator", f.sends(), f.submits)
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

// §7.5: a blocker the agent states only in its terminal title refuses a send
// as one on its screen does, since the agent listing reads the title too.
func TestSendRefusesAnAgentWhoseTitleSaysItIsWaiting(t *testing.T) {
	f := &fakeBackend{text: "some output\n"}
	f.panes = []backend.Pane{{ID: "%1", SessionName: "build", SessionID: "$1", CurrentCommand: "amp",
		Title: "Plugin confirmation needed - amp - work"}}
	s := &Session{ol: fakeOlympus(f), name: "build"}

	err := s.Send(context.Background(), "ZEBRA-4417", VerifyBudget(shortBudget))
	if !errors.Is(err, ErrBlocked) {
		t.Fatalf("error is %v, want AGENT_BLOCKED", err)
	}
	if f.sends() != 0 {
		t.Errorf("typed %d times into a waiting agent, want nothing", f.sends())
	}
}

// boxHolding is an idle Claude Code screen whose input box holds rows.
func boxHolding(t *testing.T, rows ...string) string {
	t.Helper()
	return strings.Replace(composerScreen(t, "idle-earlier-text.txt"), "❯\n", "❯ "+strings.Join(rows, "\n  ")+"\n", 1)
}

// longText is one line of 1,157 characters, the length that lost its head.
func longText() string {
	var b strings.Builder
	b.WriteString("HEADMARK the start of a long message ")
	for i := 0; b.Len() < 1140; i++ {
		fmt.Fprintf(&b, "word%d ", i)
	}
	b.WriteString("TAILMARK")
	return b.String()
}

// §7.7: a verified send into an agent is one paste. Typed, a text longer than
// the terminal's read reaches Claude Code in pieces, and it keeps only the
// last: measured on 2.1.299 on herdr, tmux and zmx, the box held the final
// 135 of 1,157 characters.
func TestSendPastesIntoAnAgent(t *testing.T) {
	text := longText()
	f := &fakeBackend{text: composerScreen(t, "idle-earlier-text.txt")}
	f.onType = func(f *fakeBackend, _ string) { f.text = boxHolding(t, "[Pasted text #1]") }
	paneRunning(f, "claude")
	s := &Session{ol: fakeOlympus(f), name: "build"}

	if err := s.Send(context.Background(), text, VerifyBudget(shortBudget)); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if len(f.pasted) != 1 || len(f.typed) != 0 || f.submits != 1 {
		t.Errorf("pasted %d, typed %d and submitted %d, want one paste, no typing and one terminator", len(f.pasted), len(f.typed), f.submits)
	}
}

// §7.7: a shell keeps typing. A paste there is a different input, and nothing
// measured asks for one.
func TestSendTypesIntoAShell(t *testing.T) {
	f := &fakeBackend{text: "$ "}
	f.onType = func(f *fakeBackend, text string) { f.text = "$ " + text }
	paneRunning(f, "zsh")
	s := &Session{ol: fakeOlympus(f), name: "build"}

	if err := s.Send(context.Background(), "make build", VerifyBudget(shortBudget)); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if len(f.typed) != 1 || len(f.pasted) != 0 {
		t.Errorf("typed %d and pasted %d, want one typing and no paste", len(f.typed), len(f.pasted))
	}
}

// §7.6: a box that holds only the tail of the text is an incomplete delivery.
// Measured on Claude Code 2.1.299: the box held the last 135 of 1,157
// characters, the send reported the text in the box, and the Enter submitted
// the tail alone.
func TestSendFailsWhenTheBoxHoldsOnlyTheTail(t *testing.T) {
	text := longText()
	tail := text[1022:]
	f := &fakeBackend{text: composerScreen(t, "idle-earlier-text.txt")}
	f.onType = func(f *fakeBackend, _ string) { f.text = boxHolding(t, tail[:len(tail)/2], tail[len(tail)/2:]) }
	paneRunning(f, "claude")
	s := &Session{ol: fakeOlympus(f), name: "build"}

	err := s.Send(context.Background(), text, VerifyBudget(shortBudget))
	if !errors.Is(err, ErrTimeout) {
		t.Fatalf("error is %v, want a timeout", err)
	}
	if !TypedOf(err) {
		t.Errorf("error %v is not marked typed, though the tail is in the box", err)
	}
	if !strings.Contains(err.Error(), "incomplete") {
		t.Errorf("error %q does not say the text is incomplete", err)
	}
	if inputs := len(f.typed) + len(f.pasted); inputs != 1 || f.submits != 0 {
		t.Errorf("sent %d times and submitted %d, want one send and no terminator", inputs, f.submits)
	}
}

// §7.6: a placeholder that arrives beside part of the text is no proof the
// rest is in it, so the send fails rather than submit what cannot be checked.
func TestSendFailsWhenAPlaceholderComesWithPartOfTheText(t *testing.T) {
	text := longText()
	f := &fakeBackend{text: composerScreen(t, "idle-earlier-text.txt")}
	f.onType = func(f *fakeBackend, _ string) { f.text = boxHolding(t, "[Pasted text #2]"+text[1022:]) }
	paneRunning(f, "claude")
	s := &Session{ol: fakeOlympus(f), name: "build"}

	err := s.Send(context.Background(), text, VerifyBudget(shortBudget))
	if !errors.Is(err, ErrTimeout) || !TypedOf(err) {
		t.Fatalf("error is %v, want a timeout marked typed", err)
	}
	if f.submits != 0 {
		t.Errorf("submitted %d, want 0", f.submits)
	}
}

// §7.6: the whole text soft-wrapped across the box's rows is the echo.
func TestSendTakesTheWholeTextWrappedAcrossTheBox(t *testing.T) {
	text := "the head of a message that wraps across several rows of the input box and ends with its tail"
	f := &fakeBackend{text: composerScreen(t, "idle-earlier-text.txt")}
	f.onType = func(f *fakeBackend, _ string) { f.text = boxHolding(t, text[:30], text[30:61], text[61:]) }
	paneRunning(f, "claude")
	s := &Session{ol: fakeOlympus(f), name: "build"}

	if err := s.Send(context.Background(), text, VerifyBudget(shortBudget)); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if f.submits != 1 {
		t.Errorf("submitted %d, want 1", f.submits)
	}
}

// scrolledBox is a Claude Code box of rows at most cols wide holding text,
// as it draws one taller than its cap: the last rows, or the first after c-a.
func scrolledBox(t *testing.T, text string, cols, rows int, start bool) string {
	t.Helper()
	var lines []string
	line := ""
	for _, word := range strings.Fields(text) {
		if line != "" && len(line)+1+len(word) > cols {
			lines = append(lines, line)
			line = ""
		}
		if line != "" {
			line += " "
		}
		line += word
	}
	lines = append(lines, line)
	if start {
		return boxHolding(t, lines[:rows]...)
	}
	return boxHolding(t, lines[len(lines)-rows:]...)
}

// §7.6: a box that shows the text's tail and not its head is scrolled to the
// cursor, or lost the head. The start key scrolls it up, and a head there is
// the echo. Measured on Claude Code 2.1.299: 790 characters in a 52x24 pane
// showed the last 7 rows, and the send failed as incomplete with the whole
// text in the box.
func TestSendScrollsATallBoxToItsStartForTheHead(t *testing.T) {
	text := longText()[:790]
	f := &fakeBackend{text: composerScreen(t, "idle-earlier-text.txt")}
	f.onType = func(f *fakeBackend, _ string) { f.text = scrolledBox(t, text, 48, 7, false) }
	f.onPress = func(f *fakeBackend, keys []backend.Key) {
		f.text = scrolledBox(t, text, 48, 7, keys[0] == "c-a")
	}
	paneRunning(f, "claude")
	s := &Session{ol: fakeOlympus(f), name: "build"}

	if err := s.Send(context.Background(), text, VerifyBudget(shortBudget)); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if f.submits != 1 {
		t.Errorf("submitted %d, want 1", f.submits)
	}
	if got := fmt.Sprint(f.pressed); got != "[c-a c-e]" {
		t.Errorf("pressed %s, want the start key and then the end key", got)
	}
}

// §7.6: a box whose start is not the text's head lost it, and the cursor is
// put back at the end before the send fails.
func TestSendFailsWhenTheStartOfTheBoxIsNotTheHead(t *testing.T) {
	text := longText()
	tail := text[1022:]
	f := &fakeBackend{text: composerScreen(t, "idle-earlier-text.txt")}
	f.onType = func(f *fakeBackend, _ string) { f.text = boxHolding(t, tail[:len(tail)/2], tail[len(tail)/2:]) }
	paneRunning(f, "claude")
	s := &Session{ol: fakeOlympus(f), name: "build"}

	err := s.Send(context.Background(), text, VerifyBudget(shortBudget))
	if !errors.Is(err, ErrTimeout) || !TypedOf(err) {
		t.Fatalf("error is %v, want a timeout marked typed", err)
	}
	if f.submits != 0 {
		t.Errorf("submitted %d, want 0", f.submits)
	}
	if got := fmt.Sprint(f.pressed); got != "[c-a c-e]" {
		t.Errorf("pressed %s, want the start key and then the end key", got)
	}
}
