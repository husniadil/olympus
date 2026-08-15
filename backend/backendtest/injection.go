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

				// Only the FINAL line is asserted, because only the final line
				// is guaranteed. §4.6 says in as many words that intermediate
				// line execution is consumer-dependent: a bracketed-paste-aware
				// line editor — zsh's ZLE, bash 5.1's readline — inserts an
				// embedded newline literally instead of running it, which is
				// the whole point of the framing Olympus pastes with.
				//
				// This case used to require the first line to have RUN, and
				// passed only against a shell that happens not to hold it. A
				// default bash held it, the case failed, and what it had found
				// was its own assumption rather than a defect.
				text := "printf 'pasted-%d\\n' 1\nprintf 'pending-%d\\n' 2"
				if err := e.Backend.Paste(e.Ctx(), target, text); err != nil {
					e.T.Fatalf("pasting: %v", err)
				}

				// The final line has no terminator, so nothing can have run it.
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
			Name: "§4 named keys are translated and arrive as keypresses",
			Fn: func(e *Env) {
				// Deliberately NOT Ctrl-C. Interrupt-by-key looks like the
				// obvious observable and is not backend-neutral: on a backend
				// whose send path generates no terminal signal, 0x03 arrives
				// as a byte and interrupts nothing, so the case would be
				// asserting a tmux property rather than the key vocabulary
				// (§2.8.1, cause 1). Interrupting is covered on its own terms
				// by the §2.8.1 cases, against each backend's declaration.
				//
				// What is under test here is translation: the session has to
				// receive a keypress, not the five characters "enter".
				target := e.StartShell()
				e.Warm(target)

				if err := e.Backend.Type(e.Ctx(), target, `printf 'keyed-%d\n' 6`); err != nil {
					e.T.Fatalf("typing: %v", err)
				}
				e.Never(target, "keyed-6")

				if err := e.Backend.Press(e.Ctx(), target, backend.KeyEnter); err != nil {
					e.T.Fatalf("pressing: %v", err)
				}
				e.WaitFor(target, "keyed-6")
			},
		},
		{
			Name: "§4 the whole control range and the function keys are pressable",
			Fn: func(e *Env) {
				// Driving a full-screen program means pressing whatever IT
				// binds. A backend that supports only a handful of control
				// letters cannot leave an editor, and the failure looks like a
				// usage error naming a key that plainly exists.
				target := e.StartShell()
				for _, key := range []backend.Key{"c-a", "c-k", "c-o", "c-x", "c-w", "c-z", "f1", "f5", "f12"} {
					if err := e.Backend.Press(e.Ctx(), target, key); err != nil {
						e.T.Errorf("pressing %q: %v", key, err)
					}
				}

				// Open does not mean anything goes. The shapes are c-<letter>
				// and f<1-12>; something that merely looks like one is still
				// the caller's mistake to fix, and a backend that accepted it
				// would be silently sending nothing.
				for _, key := range []backend.Key{"c-1", "c-", "f0", "f13", "ctrl-x"} {
					if err := e.Backend.Press(e.Ctx(), target, key); err == nil {
						e.T.Errorf("pressing %q was accepted, but it is not a key", key)
					} else if backend.CodeOf(err) != backend.CodeUsage {
						e.T.Errorf("pressing %q is %q, want %q", key, backend.CodeOf(err), backend.CodeUsage)
					}
				}
			},
		},
		{
			Name: "§4.9 a backend claiming control keys actually delivers them",
			Fn: func(e *Env) {
				// Measured against the session rather than trusted: `cat -v`
				// prints a control byte as ^X, so whatever arrives is visible
				// as ordinary output. Accepting a key and dropping it is worse
				// than rejecting it — the caller sees success and waits for an
				// effect that will never come.
				if !e.Backend.Capabilities().ControlKeys {
					return
				}
				target := e.StartShell()
				e.Warm(target)

				if err := e.Backend.Type(e.Ctx(), target, "cat -v"); err != nil {
					e.T.Fatalf("typing: %v", err)
				}
				if err := e.Backend.Submit(e.Ctx(), target); err != nil {
					e.T.Fatalf("submitting: %v", err)
				}
				time.Sleep(e.budgets.Settle)

				if err := e.Backend.Type(e.Ctx(), target, "MARK"); err != nil {
					e.T.Fatalf("typing the marker: %v", err)
				}
				if err := e.Backend.Press(e.Ctx(), target, backend.KeyCtrlA); err != nil {
					e.T.Fatalf("pressing: %v", err)
				}
				if err := e.Backend.Submit(e.Ctx(), target); err != nil {
					e.T.Fatalf("submitting: %v", err)
				}
				e.WaitFor(target, "MARK^A")
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
