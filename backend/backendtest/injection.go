package backendtest

import (
	"strings"
	"time"

	"github.com/husniadil/olympus/backend"
)

func injectionCases() []Case {
	return []Case{
		{
			Name: "§4.3 literal injection never submits, and §4.5 a later submit does",
			Fn: func(e *Env) {
				// One case, because the two halves only mean something
				// together: text that never appears proves nothing about
				// submission, and text that appears after a submit proves
				// nothing about the injection having held.
				target := e.StartShell()
				e.Warm(target)

				if err := e.Backend.Type(e.Ctx(), target, `printf 'held-%d\n' 3`); err != nil {
					e.T.Fatalf("typing: %v", err)
				}
				// The expanded output must NOT appear: the typed line is
				// echoed verbatim by the PTY, so only the substitution
				// distinguishes "sitting on the prompt" from "executed".
				e.Never(target, "held-3")

				if err := e.Backend.Submit(e.Ctx(), target); err != nil {
					e.T.Fatalf("submitting: %v", err)
				}
				e.WaitFor(target, "held-3")
			},
		},
		{
			Name: "§4.6 paste carries multiple lines and still never auto-submits",
			Fn: func(e *Env) {
				target := e.StartShell()
				e.Warm(target)

				// The embedded newline ends the first line, so a shell runs
				// it. The final line has no terminator, so it must sit on the
				// prompt unexecuted — that is the rule under test.
				text := "printf 'pasted-%d\\n' 1\nprintf 'pending-%d\\n' 2"
				if err := e.Backend.Paste(e.Ctx(), target, text); err != nil {
					e.T.Fatalf("pasting: %v", err)
				}

				e.WaitFor(target, "pasted-1")
				e.Never(target, "pending-2")

				if err := e.Backend.Submit(e.Ctx(), target); err != nil {
					e.T.Fatalf("submitting: %v", err)
				}
				e.WaitFor(target, "pending-2")
			},
		},
		{
			Name: "§4.7 atomic submit delivers text and terminator together",
			Fn: func(e *Env) {
				target := e.StartShell()
				e.Warm(target)

				if err := e.Backend.SendAtomic(e.Ctx(), target, `printf 'atomic-%d\n' 9`); err != nil {
					e.T.Fatalf("sending atomically: %v", err)
				}
				e.WaitFor(target, "atomic-9")
			},
		},
		{
			Name: "§4 named keys reach the session as keypresses",
			Fn: func(e *Env) {
				// Interrupt-by-key is the observable case: the shell has to
				// receive a keypress, not the two characters "C" and "-c".
				target := e.StartShell()
				e.Warm(target)

				if err := e.Backend.Type(e.Ctx(), target, "sleep 30"); err != nil {
					e.T.Fatalf("typing: %v", err)
				}
				if err := e.Backend.Submit(e.Ctx(), target); err != nil {
					e.T.Fatalf("submitting: %v", err)
				}
				time.Sleep(e.budgets.Settle)

				if err := e.Backend.Press(e.Ctx(), target, backend.KeyCtrlC); err != nil {
					e.T.Fatalf("pressing: %v", err)
				}
				if !e.shellRuns(target, "keyed") {
					e.T.Errorf("the shell did not become responsive, so the key did not arrive as a keypress")
				}
			},
		},
		{
			Name: "§4 an unknown key name is a usage error, not an unexpected one",
			Fn: func(e *Env) {
				// It is input the caller could have validated by changing one
				// argument, which §12 makes the definition of a usage error.
				target := e.StartShell()
				err := e.Backend.Press(e.Ctx(), target, backend.Key("no-such-key"))
				if err == nil {
					e.T.Fatalf("an unknown key name was accepted")
				}
				if got := backend.CodeOf(err); got != backend.CodeUsage {
					e.T.Errorf("an unknown key name is %q, want %q", got, backend.CodeUsage)
				}
			},
		},
		{
			Name: "§10 an operation on an absent session is not-found",
			Fn: func(e *Env) {
				absent := e.Name()
				err := e.Backend.Type(e.Ctx(), absent, "anything")
				if err == nil {
					e.T.Fatalf("typing into an absent session succeeded")
				}
				if got := backend.CodeOf(err); got != backend.CodeSessionNotFound {
					e.T.Errorf("typing into an absent session is %q, want %q", got, backend.CodeSessionNotFound)
				}
				if !strings.Contains(err.Error(), absent) {
					e.T.Errorf("the error %q does not name the target", err.Error())
				}
			},
		},
	}
}
