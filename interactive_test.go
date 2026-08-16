package olympus_test

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/husniadil/olympus"
	"github.com/husniadil/olympus/backend"
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
	t.Parallel()
	python := requirePython(t)
	for _, l := range legs(t) {
		t.Run(l.name, func(t *testing.T) {
			ol := l.open(t)
			s := session(t, ol)
			ctx := context.Background()

			// PYTHON_BASIC_REPL pins which REPL answers, and the flake it
			// settles took four rounds of a full suite under load to reproduce.
			//
			// Python 3.13 made PyREPL the default: a cursor-addressing REPL that
			// draws its prompt by positioning rather than by printing a line. Under
			// load its first paint lands on the row the shell's echo is still on,
			// and the pane genuinely renders
			//
			//     $ /usr/bin/python3 -q>>>
			//
			// with the prompt appended to the command instead of starting a line.
			// The capture is CORRECT — that is what the terminal drew — so nothing
			// in Olympus is wrong; the anchored pattern below simply cannot match
			// it, roughly one run in eight here.
			//
			// The basic REPL prints its prompt on a line of its own, on every
			// version, which is the line-oriented behaviour this test exists to
			// drive. Full-screen repainting is a different property with its own
			// test against a real editor. Unknown to Python 3.12 and earlier, where
			// it is ignored rather than an error.
			if err := s.Send(ctx, "PYTHON_BASIC_REPL=1 "+python+" -q"); err != nil {
				t.Fatalf("starting the REPL: %v", err)
			}

			// Written as `^>>>\s*$` rather than `^>>> $` on purpose: whether
			// the prompt's trailing space survives into a capture is a
			// BACKEND difference, not something the caller controls. One
			// backend preserves it and another normalizes it away, so a
			// pattern that requires it works on one and silently never matches
			// on the other.
			if _, err := s.WaitFor(ctx, `^>>>\s*$`, olympus.WaitTimeout(20*time.Second)); err != nil {
				// The screen AND the pane rows are the evidence. A timeout that
				// only says a pattern did not appear cannot distinguish a
				// program that never started, a prompt spelled differently, and
				// a pane that died — three different problems with one message.
				panes, panesErr := ol.Panes(ctx, s.Name())
				t.Fatalf("the REPL prompt never appeared: %v\nScreen was:\n%q\nPanes: %+v (err=%v)",
					err, mustScreen(t, s).Text, panes, panesErr)
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
	t.Parallel()
	python := requirePython(t)
	for _, l := range legs(t) {
		t.Run(l.name, func(t *testing.T) {
			ol := l.open(t)
			s := session(t, ol)
			ctx := context.Background()

			if err := s.Send(ctx, python+" -q"); err != nil {
				t.Fatalf("starting the REPL: %v", err)
			}
			if _, err := s.WaitFor(ctx, `^>>>\s*$`, olympus.WaitTimeout(20*time.Second)); err != nil {
				// The screen AND the pane rows are the evidence. A timeout that
				// only says a pattern did not appear cannot distinguish a
				// program that never started, a prompt spelled differently, and
				// a pane that died — three different problems with one message.
				panes, panesErr := ol.Panes(ctx, s.Name())
				t.Fatalf("the REPL prompt never appeared: %v\nScreen was:\n%q\nPanes: %+v (err=%v)",
					err, mustScreen(t, s).Text, panes, panesErr)
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
	t.Parallel()
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
				captured, err := ol.Screens(ctx, []string{name})
				if err != nil {
					t.Fatalf("Capture: %v", err)
				}
				if strings.Contains(captured.Screens[name], "FRAME-MARKER ready") {
					if !ol.Capabilities().TracksAltScreen {
						return
					}
					// The flag is re-read rather than taken from the capture
					// above. A capture reads the metadata BEFORE the screen — it
					// has to, since the alt-screen answer decides what to ask
					// for (§5.3) — so the flag in one result can predate the
					// text beside it. Asserting them as simultaneous passed
					// everywhere until a loaded macOS runner made the window
					// wide enough to lose, and reported a correct backend as
					// failing to track the alternate screen.
					for {
						again, err := ol.Screens(ctx, []string{name})
						if err != nil {
							t.Fatalf("Capture: %v", err)
						}
						if again.Meta[name].AltScreen {
							return
						}
						if time.Now().After(deadline) {
							t.Fatal("the pane is on the alternate screen but the flag never became set")
						}
						time.Sleep(100 * time.Millisecond)
					}
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
	t.Parallel()
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
				captured, err := ol.Screens(ctx, []string{name}, olympus.WithHistory(500))
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
// §5.4: waiting for a pattern is LINE-oriented. Anchors are what callers write
// — `^42$` for an answer, `^>>>` for a prompt — and matching the screen as one
// blob makes every one of them silently never match.
func TestWaitMatchesPerLine(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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

// Driving a real full-screen editor, end to end: open it, type, save, quit, and
// check the file on disk.
//
// This is the case the whole alt-screen and key-vocabulary work exists for, and
// it is worth an integration test because every part of it failed at least once:
// the editor was invisible through the door, and Ctrl-X was not a key Olympus
// could press.
func TestDrivingAFullScreenEditor(t *testing.T) {
	t.Parallel()
	editor, err := lookPathGNUNano()
	if err != nil {
		t.Skip("GNU nano is not installed, so the editor leg cannot run")
	}

	for _, l := range legs(t) {
		t.Run(l.name, func(t *testing.T) {
			ol := l.open(t)
			if !ol.Capabilities().ControlKeys {
				// Without control keys the editor can be opened and read but
				// never saved or exited, which is a documented limitation
				// (§4.9) rather than a failure to report here.
				t.Skip("this backend does not deliver control keys, so a full-screen program cannot be driven")
			}
			ctx := context.Background()

			work := t.TempDir()
			file := filepath.Join(work, "note.txt")
			if err := os.WriteFile(file, []byte("hello from a file\n"), 0o600); err != nil {
				t.Fatalf("writing the file: %v", err)
			}

			name := fmt.Sprintf("oly-o%d", counter.Add(1))
			s, err := ol.Session(ctx, name, olympus.In(work))
			if err != nil {
				t.Fatalf("Session: %v", err)
			}
			t.Cleanup(func() { _, _ = s.Stop(ctx, olympus.Force()) })
			warmUp(t, s)

			// Launched INSIDE the shell rather than as the session's command,
			// so the shell is still there to come back to.
			if err := s.Send(ctx, editor+" note.txt"); err != nil {
				t.Fatalf("opening the editor: %v", err)
			}
			if _, err := s.WaitFor(ctx, `GNU nano`, olympus.WaitTimeout(20*time.Second)); err != nil {
				t.Fatalf("the editor never appeared — a caller cannot see it: %v", err)
			}

			// Type never submits, which is what puts text in a full-screen
			// program rather than at a shell prompt.
			if err := s.Type(ctx, "EDITED-BY-OLYMPUS"); err != nil {
				t.Fatalf("typing into the editor: %v", err)
			}
			if _, err := s.WaitFor(ctx, `EDITED-BY-OLYMPUS`, olympus.WaitTimeout(15*time.Second)); err != nil {
				t.Fatalf("the typed text never reached the editor: %v", err)
			}

			// Write out, confirm the filename, exit. None of these are
			// reachable without the full control range.
			for _, step := range []struct {
				key  backend.Key
				want string
			}{
				// BOTH wordings, because nano changed it and the supported
				// range spans the change: 9.2 says "Write to File", 7.2 — what
				// Ubuntu 24.04 ships, and what CI runs — says "File Name to
				// Write". The comment here used to name the old wording while
				// the pattern accepted only the new one, which is a note that
				// knew about the problem without preventing it.
				{"c-o", `Write to File|File Name to Write`},
				{"enter", `GNU nano`},
			} {
				if err := s.Press(ctx, step.key); err != nil {
					t.Fatalf("pressing %s: %v", step.key, err)
				}
				if _, err := s.WaitFor(ctx, step.want, olympus.WaitTimeout(15*time.Second)); err != nil {
					t.Fatalf("after %s, the editor did not show %q: %v", step.key, step.want, err)
				}
			}
			if err := s.Press(ctx, "c-x"); err != nil {
				t.Fatalf("pressing c-x: %v", err)
			}

			// The shell comes back, which proves the editor really exited
			// rather than the keys landing somewhere harmless.
			if err := s.Send(ctx, `printf 'after-editor-%d\n' 1`); err != nil {
				t.Fatalf("sending to the shell: %v", err)
			}
			if _, err := s.WaitFor(ctx, `after-editor-1`, olympus.WaitTimeout(20*time.Second)); err != nil {
				t.Fatalf("the shell did not resume after the editor exited: %v", err)
			}

			// And the edit is on disk: the keys did what they looked like they
			// did, rather than merely painting convincingly.
			saved, err := os.ReadFile(file)
			if err != nil {
				t.Fatalf("reading the file back: %v", err)
			}
			if !strings.Contains(string(saved), "EDITED-BY-OLYMPUS") {
				t.Errorf("the file on disk does not contain the edit: %q", saved)
			}
		})
	}
}

// lookPathGNUNano finds GNU nano specifically, not merely a binary called nano.
//
// On macOS /usr/bin/nano is a symlink to pico — "UW PICO 5.09" — so a plain
// LookPath succeeds and hands back a different editor whose every string
// differs. The test then waits twenty seconds for "GNU nano" and reports that
// the editor never appeared, which is true of the editor it was looking for and
// says nothing about the one it actually launched.
//
// Found on a macOS CI runner, where Homebrew's nano is absent. It passes
// locally only because a Homebrew nano sits earlier in PATH.
func lookPathGNUNano() (string, error) {
	path, err := exec.LookPath("nano")
	if err != nil {
		return "", err
	}
	out, err := exec.Command(path, "--version").CombinedOutput()
	if err != nil || !strings.Contains(string(out), "GNU nano") {
		return "", fmt.Errorf("%s is not GNU nano", path)
	}
	return path, nil
}
