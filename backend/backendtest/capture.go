package backendtest

import (
	"strings"
	"time"

	"github.com/husniadil/olympus/backend"
)

func captureCases() []Case {
	return []Case{
		{
			Name: "§5.4 a pane outside copy mode reports no scroll offset",
			Fn: func(e *Env) {
				target := e.StartShell()
				e.Warm(target)
				meta := e.Screen(target).Meta
				if meta.ScrollPosition != 0 {
					e.T.Errorf("scroll_position is %d for a pane at the live bottom, want 0", meta.ScrollPosition)
				}
				if meta.AltScreen {
					e.T.Errorf("alt_screen is set for an ordinary shell pane")
				}
			},
		},
		{
			Name: "§5.1 requesting history returns lines that have scrolled off",
			Fn: func(e *Env) {
				// The point of history is the lines the visible screen no
				// longer holds. Asserting only that history is non-empty would
				// pass against a backend that silently returned the visible
				// screen twice.
				target := e.StartShell()
				e.Warm(target)

				if err := e.Backend.Type(e.Ctx(), target, `i=1; while [ $i -le 200 ]; do printf 'ln-%d\n' $i; i=$((i+1)); done`); err != nil {
					e.T.Fatalf("typing: %v", err)
				}
				if err := e.Backend.Submit(e.Ctx(), target); err != nil {
					e.T.Fatalf("submitting: %v", err)
				}
				e.WaitFor(target, "ln-200")

				// Settle first. Comparing two captures of a live session
				// otherwise asserts that the shell did not repaint between
				// them, which fails intermittently and has nothing to do with
				// the rule under test.
				visible := e.Quiesce(target)
				withHistory, err := e.Backend.Screen(e.Ctx(), target, backend.ScreenOpts{HistoryLines: 500})
				if err != nil {
					e.T.Fatalf("capturing with history: %v", err)
				}

				if e.Backend.Capabilities().NativeScrollback {
					// A backend whose capture is already full scrollback has
					// no separate viewport mode to opt into, so BOTH states
					// must return byte-identical output. Asserting that is the
					// regression guard §5.2 asks for: a backend that started
					// honouring the request would be silently changing what
					// every existing caller gets back.
					if visible != withHistory.Text {
						e.T.Errorf("this backend reports native scrollback, so requesting history must be a no-op, but the two captures differ")
					}
					if !strings.Contains(visible, "ln-1\n") {
						e.T.Errorf("a native-scrollback capture does not contain the first line")
					}
					return
				}

				if strings.Contains(visible, "ln-1\n") {
					e.T.Fatalf("the first line is still visible on a 24-row screen, so nothing scrolled off and this case proves nothing")
				}
				if !strings.Contains(withHistory.Text, "ln-1\n") {
					e.T.Errorf("a capture with history does not contain the scrolled-off first line")
				}
				if !strings.Contains(withHistory.Text, "ln-200") {
					e.T.Errorf("a capture with history dropped the visible screen")
				}
			},
		},
		{
			Name: "§5.3 an alt-screen pane is reported by the flag, and capture still succeeds",
			Fn: func(e *Env) {
				// §5.3 specifies two layers behaving differently, and this is
				// the mechanical one. The backend's capture NEVER refuses a
				// target for being on the alternate screen: it succeeds, and
				// the underlying call simply yields nothing useful. The
				// alt-screen flag is the signal a caller uses to decide whether
				// the result is worth taking, and this layer has no opinion
				// about that.
				//
				// Skipping the capture and returning empty is the door's rule,
				// not this one. Asserting it here would require every backend
				// to implement a policy that belongs one layer up.
				target := e.StartCommand("sh", "-c", `printf '\033[?1049h'; sleep 30`)

				if !e.Backend.Capabilities().TracksAltScreen {
					// Not tracking it is an honest answer, not an
					// unsupported-class error: the caller asked a question this
					// backend answers with "not tracked". The call must still
					// succeed, with zero metadata and no subprocess run to
					// check (§5.3).
					capture, err := e.Backend.Screen(e.Ctx(), target, backend.ScreenOpts{})
					if err != nil {
						e.T.Fatalf("capturing succeeded nowhere: %v", err)
					}
					if capture.Meta.AltScreen {
						e.T.Errorf("this backend declares it does not track alt-screen, but reported the flag set")
					}
					return
				}

				deadline := time.Now().Add(e.budgets.Screen)
				for {
					capture, err := e.Backend.Screen(e.Ctx(), target, backend.ScreenOpts{})
					if err != nil {
						e.T.Fatalf("capturing an alt-screen pane failed, but this layer must never refuse one: %v", err)
					}
					if capture.Meta.AltScreen {
						return
					}
					if time.Now().After(deadline) {
						e.T.Errorf("alt_screen was never set for a pane on the alternate screen, so a caller cannot tell an empty capture from a skipped one")
						return
					}
					time.Sleep(e.budgets.Poll)
				}
			},
		},
		{
			Name: "§10 capturing an absent session is not-found",
			Fn: func(e *Env) {
				_, err := e.Backend.Screen(e.Ctx(), e.Name(), backend.ScreenOpts{})
				if err == nil {
					e.T.Fatalf("capturing an absent session succeeded")
				}
				if got := backend.CodeOf(err); got != backend.CodeSessionNotFound {
					e.T.Errorf("capturing an absent session is %q, want %q", got, backend.CodeSessionNotFound)
				}
			},
		},
	}
}
