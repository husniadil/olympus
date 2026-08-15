package backendtest

import (
	"strings"
	"time"

	"github.com/husniadil/olympus/backend"
)

func lifecycleCases() []Case {
	return []Case{
		{
			Name: "§2.1 a created session is immediately addressable",
			Fn: func(e *Env) {
				target := e.StartShell()
				if got := e.Backend.Probe(e.Ctx(), target); got != backend.StatePresent {
					e.T.Errorf("probe of a just-created session is %q, want %q", got, backend.StatePresent)
				}
				row := e.SessionNamed(target)
				if row.Name != target {
					e.T.Errorf("listed row names %q, want %q", row.Name, target)
				}
			},
		},
		{
			Name: "§2.3 a command session is spawned by exec, never typed into a shell",
			Fn: func(e *Env) {
				// The distinction is observable: a typed command is echoed by
				// the PTY, so the argv itself would appear on screen. An
				// exec-spawned one produces only its own output. Typing also
				// loses every argument containing shell metacharacters, which
				// is the reason the rule exists.
				target := e.StartCommand("sh", "-c", `printf 'spawned-%d\n' 5; sleep 30`)
				e.WaitFor(target, "spawned-5")

				screen := e.Screen(target).Text
				if strings.Contains(screen, "printf 'spawned-") {
					e.T.Errorf("the spawn argv was echoed, so it was typed into a shell rather than executed. Screen was:\n%s", screen)
				}
			},
		},
		{
			Name: "§2.8.1 interrupting a shell-backed session",
			Fn: func(e *Env) {
				want := e.Expect.InterruptShellBacked
				if want == "" {
					e.T.Fatalf("this backend has not declared InterruptShellBacked; §2.8.1 outcomes must be declared, never inherited by silence")
				}

				target := e.StartShell()
				e.Warm(target)
				if err := e.Backend.Type(e.Ctx(), target, "sleep 30"); err != nil {
					e.T.Fatalf("typing: %v", err)
				}
				if err := e.Backend.Submit(e.Ctx(), target); err != nil {
					e.T.Fatalf("submitting: %v", err)
				}
				// Let the shell actually enter the sleep, or the interrupt
				// races the command it is meant to interrupt.
				time.Sleep(e.budgets.Settle)

				if err := e.Backend.Interrupt(e.Ctx(), target); err != nil {
					e.T.Fatalf("interrupting: %v", err)
				}

				// The observable is whether the shell became responsive again:
				// a shell whose foreground command is still sleeping queues the
				// probe instead of running it.
				responsive := e.shellRuns(target, "woke")
				switch want {
				case InterruptStops:
					if !responsive {
						e.T.Errorf("the shell never became responsive, so the interrupt did not reach the foreground process")
					}
				case InterruptIneffective:
					if responsive {
						e.T.Errorf("the interrupt worked, but this backend declares it ineffective for a shell-backed session — update the declaration")
					}
				}
			},
		},
		{
			Name: "§2.8.1 interrupting an exec-spawned session",
			Fn: func(e *Env) {
				want := e.Expect.InterruptExecSpawned
				if want == "" {
					e.T.Fatalf("this backend has not declared InterruptExecSpawned; §2.8.1 outcomes must be declared, never inherited by silence")
				}

				// A process spawned by exec can inherit an ignored interrupt
				// disposition, and a signal ignored on entry can never be
				// trapped — so no delivery mechanism can help. That is a real
				// declared outcome, and the caller must fall through to a
				// forced kill.
				target := e.StartCommand("sh", "-c", `printf 'running-%d\n' 1; sleep 30`)
				e.WaitFor(target, "running-1")

				if err := e.Backend.Interrupt(e.Ctx(), target); err != nil {
					e.T.Fatalf("interrupting: %v", err)
				}
				time.Sleep(e.budgets.Settle)

				stopped := e.foregroundStopped(target)
				switch want {
				case InterruptStops:
					if !stopped {
						e.T.Errorf("the spawned process survived the interrupt")
					}
				case InterruptIneffective:
					if stopped {
						e.T.Errorf("the interrupt stopped the spawned process, but this backend declares it ineffective — update the declaration")
					}
				}
			},
		},
		{
			Name: "§2.8 a killed session stops being present",
			Fn: func(e *Env) {
				target := e.StartShell()
				if err := e.Backend.Kill(e.Ctx(), target); err != nil {
					e.T.Fatalf("killing: %v", err)
				}
				deadline := time.Now().Add(e.budgets.Screen)
				for {
					if e.Backend.Probe(e.Ctx(), target) == backend.StateAbsent {
						return
					}
					if time.Now().After(deadline) {
						e.T.Errorf("a killed session is still present after the budget")
						return
					}
					time.Sleep(e.budgets.Poll)
				}
			},
		},
	}
}

// shellRuns reports whether the session's shell executes a command now. The
// probe is expansion-based for the reason given on Warm: only substituted
// output distinguishes execution from echo.
func (e *Env) shellRuns(target, tag string) bool {
	e.T.Helper()
	if err := e.Backend.Type(e.Ctx(), target, `printf '`+tag+`-%d\n' 8`); err != nil {
		return false
	}
	if err := e.Backend.Submit(e.Ctx(), target); err != nil {
		return false
	}
	_, ok := e.screenContains(target, tag+"-8", e.budgets.Screen)
	return ok
}

// foregroundStopped reports whether a session's spawned process is no longer
// running. Deliberately backend-neutral: a backend that keeps a corpse pane
// answers through the dead flag, one that tears the session down answers
// through absence, and both mean the same thing here.
func (e *Env) foregroundStopped(target string) bool {
	e.T.Helper()
	if e.Backend.Probe(e.Ctx(), target) == backend.StateAbsent {
		return true
	}
	panes, err := e.Backend.Panes(e.Ctx(), target)
	if err != nil {
		return false
	}
	for _, p := range panes {
		if p.Dead {
			return true
		}
	}
	return false
}
