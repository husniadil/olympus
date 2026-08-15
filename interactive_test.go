package olympus_test

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/husniadil/olympus"
)

// Driving something interactive is the whole point of a real terminal, and it
// is where the difference between "the command ran" and "the program is
// listening" shows up. These run against an actual REPL and an actual
// full-screen application rather than a shell pretending to be one.

func requirePython(t *testing.T) string {
	t.Helper()
	for _, name := range []string{"python3", "python"} {
		if path, err := exec.LookPath(name); err == nil {
			return path
		}
	}
	t.Skip("no python interpreter is installed, so the REPL leg cannot run")
	return ""
}

// A REPL is driven with type/send and read with wait — not with run, whose
// sentinel protocol is shell syntax (§6.5).
func TestDrivingAPythonREPL(t *testing.T) {
	python := requirePython(t)
	for _, l := range legs(t) {
		t.Run(l.name, func(t *testing.T) {
			s := session(t, l.open(t))
			ctx := context.Background()

			if err := s.Send(ctx, python+" -q"); err != nil {
				t.Fatalf("starting the REPL: %v", err)
			}

			// Written as `^>>>\s*$` rather than `^>>> $` on purpose: whether
			// the prompt's trailing space survives into a capture is a
			// BACKEND difference, not something the caller controls. One
			// backend preserves it and another normalizes it away, so a
			// pattern that requires it works on one and silently never matches
			// on the other.
			if _, err := s.WaitFor(ctx, `^>>>\s*$`, olympus.WaitTimeout(20*time.Second)); err != nil {
				t.Fatalf("the REPL prompt never appeared: %v", err)
			}

			if err := s.Send(ctx, "print(6*7)"); err != nil {
				t.Fatalf("sending an expression: %v", err)
			}
			got, err := s.WaitFor(ctx, `^42$`, olympus.WaitTimeout(15*time.Second))
			if err != nil {
				t.Fatalf("the REPL never answered: %v", err)
			}
			if strings.TrimSpace(got.Line) != "42" {
				t.Errorf("the matched line is %q, want the answer alone", got.Line)
			}

			// And back out to the shell, which proves the REPL was really in
			// control of the pane rather than the text merely being echoed.
			if err := s.Send(ctx, "exit()"); err != nil {
				t.Fatalf("leaving the REPL: %v", err)
			}
			if err := s.Send(ctx, `printf 'back-%d\n' 1`); err != nil {
				t.Fatalf("sending to the shell: %v", err)
			}
			if _, err := s.WaitFor(ctx, `back-1`, olympus.WaitTimeout(15*time.Second)); err != nil {
				t.Errorf("the shell did not resume after the REPL exited: %v", err)
			}
		})
	}
}

// §6.5: the sentinel protocol is shell syntax — `;` chaining and `$?` — so
// pointing a run at a REPL cannot work. It times out, which is
// indistinguishable at this layer from a command that took too long, and that
// is the documented outcome rather than a bug to fix.
func TestRunningACommandInAREPLTimesOutRatherThanLying(t *testing.T) {
	python := requirePython(t)
	for _, l := range legs(t) {
		t.Run(l.name, func(t *testing.T) {
			s := session(t, l.open(t))
			ctx := context.Background()

			if err := s.Send(ctx, python+" -q"); err != nil {
				t.Fatalf("starting the REPL: %v", err)
			}
			if _, err := s.WaitFor(ctx, `^>>>\s*$`, olympus.WaitTimeout(20*time.Second)); err != nil {
				t.Fatalf("the REPL prompt never appeared: %v", err)
			}

			_, err := s.Exec(ctx, "print(1)", olympus.RunTimeout(3*time.Second))
			if err == nil {
				t.Fatal("a run against a REPL reported success; its markers cannot have executed")
			}
			// A timeout, never a false completion: reporting an exit code the
			// protocol never observed would be worse than saying nothing.
			if !strings.Contains(err.Error(), "did not complete") {
				t.Errorf("error is %v, want a timeout", err)
			}
		})
	}
}

// A full-screen application on the alternate screen must be READABLE.
//
// The visible grid is exactly what a caller driving one needs, and skipping it
// would mean a program can be started and never observed — which is only
// acceptable if a human is already watching the session live.
func TestAFullScreenApplicationIsVisible(t *testing.T) {
	for _, l := range legs(t) {
		t.Run(l.name, func(t *testing.T) {
			ol := l.open(t)
			ctx := context.Background()
			name := fmt.Sprintf("oly-o%d", counter.Add(1))

			// Enters the alternate screen and repaints, the way an editor or a
			// full-screen client does.
			s, err := ol.Session(ctx, name, olympus.In(t.TempDir()), olympus.Command("sh", "-c",
				`printf '\033[?1049h'; while :; do printf '\033[H\033[2JFRAME-MARKER ready\r\n> '; sleep 1; done`))
			if err != nil {
				t.Fatalf("Session: %v", err)
			}
			t.Cleanup(func() { _, _ = s.Stop(ctx, olympus.Force()) })

			deadline := time.Now().Add(20 * time.Second)
			for {
				captured, err := ol.Capture(ctx, []string{name})
				if err != nil {
					t.Fatalf("Capture: %v", err)
				}
				if strings.Contains(captured.Screens[name], "FRAME-MARKER ready") {
					if ol.Capabilities().TracksAltScreen && !captured.Meta[name].AltScreen {
						t.Errorf("the pane is on the alternate screen but the flag is unset")
					}
					return
				}
				if time.Now().After(deadline) {
					t.Fatalf("a full-screen application was never readable; screen was %q", captured.Screens[name])
				}
				time.Sleep(200 * time.Millisecond)
			}
		})
	}
}

// The alternate screen genuinely has no scrollback, so a history request cannot
// be honoured. Dropping it silently would hand back less than was asked for
// with nothing saying so.
func TestHistoryAgainstAFullScreenApplicationWarns(t *testing.T) {
	for _, l := range legs(t) {
		t.Run(l.name, func(t *testing.T) {
			ol := l.open(t)
			if !ol.Capabilities().TracksAltScreen {
				t.Skip("this backend does not track the alternate screen, so it cannot know to warn")
			}
			ctx := context.Background()
			name := fmt.Sprintf("oly-o%d", counter.Add(1))

			s, err := ol.Session(ctx, name, olympus.In(t.TempDir()), olympus.Command("sh", "-c",
				`printf '\033[?1049h'; while :; do printf '\033[H\033[2JFRAME-MARKER ready\r\n'; sleep 1; done`))
			if err != nil {
				t.Fatalf("Session: %v", err)
			}
			t.Cleanup(func() { _, _ = s.Stop(ctx, olympus.Force()) })

			deadline := time.Now().Add(20 * time.Second)
			for {
				captured, err := ol.Capture(ctx, []string{name}, olympus.WithHistory(500))
				if err != nil {
					t.Fatalf("Capture: %v", err)
				}
				if captured.Meta[name].AltScreen {
					var told bool
					for _, w := range captured.Warnings {
						if strings.Contains(w.Message, "alternate screen") {
							told = true
						}
					}
					if !told {
						t.Errorf("a history request against an alt-screen target said nothing: %+v", captured.Warnings)
					}
					return
				}
				if time.Now().After(deadline) {
					t.Skip("the pane never reported the alternate screen on this backend")
				}
				time.Sleep(200 * time.Millisecond)
			}
		})
	}
}

// Patterns are line-oriented because a screen is lines. Whole-screen matching
// makes every anchored pattern silently never match, while a plain substring
// still works — which is what makes that bug so easy to ship unnoticed.
func TestWaitMatchesPerLine(t *testing.T) {
	for _, l := range legs(t) {
		t.Run(l.name, func(t *testing.T) {
			s := session(t, l.open(t))
			ctx := context.Background()

			if err := s.Send(ctx, `printf 'anchored-%d\n' 8`); err != nil {
				t.Fatalf("Send: %v", err)
			}
			// Anchored to the line, with plenty of other content on screen
			// above and blank padding below.
			got, err := s.WaitFor(ctx, `^anchored-8$`, olympus.WaitTimeout(15*time.Second))
			if err != nil {
				t.Fatalf("an anchored pattern never matched: %v", err)
			}
			if got.Line != "anchored-8" {
				t.Errorf("matched line is %q, want %q", got.Line, "anchored-8")
			}
		})
	}
}

// WaitFor reports the line that matched, so a caller does not have to run the
// match again to find out which line it was.
func TestWaitForReportsTheMatchedLine(t *testing.T) {
	for _, l := range legs(t) {
		t.Run(l.name, func(t *testing.T) {
			s := session(t, l.open(t))
			ctx := context.Background()

			if err := s.Send(ctx, `printf 'matched-%d\n' 7`); err != nil {
				t.Fatalf("Send: %v", err)
			}
			got, err := s.WaitFor(ctx, `matched-7`, olympus.WaitTimeout(10*time.Second))
			if err != nil {
				t.Fatalf("WaitFor: %v", err)
			}
			if !got.Matched {
				t.Error("the result does not report that it matched")
			}
			if !strings.Contains(got.Line, "matched-7") {
				t.Errorf("the matched line is %q, which does not contain the pattern", got.Line)
			}
			// The line is one line, not the whole screen.
			if strings.Count(got.Line, "\n") != 0 {
				t.Errorf("the matched line spans several lines: %q", got.Line)
			}
		})
	}
}

// Pasting and submitting is one operation with the retry discipline, not two
// calls a caller has to sequence themselves.
func TestPasteAndSubmitExecutesTheFinalLine(t *testing.T) {
	for _, l := range legs(t) {
		t.Run(l.name, func(t *testing.T) {
			s := session(t, l.open(t))
			ctx := context.Background()

			if err := s.PasteAndSubmit(ctx, `printf 'pasted-%d\n' 5`); err != nil {
				t.Fatalf("PasteAndSubmit: %v", err)
			}
			if _, err := s.WaitFor(ctx, `pasted-5`, olympus.WaitTimeout(10*time.Second)); err != nil {
				t.Errorf("the pasted line was never executed: %v", err)
			}
		})
	}
}
