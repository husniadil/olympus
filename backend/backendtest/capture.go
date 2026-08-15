package backendtest

import (
	"strings"

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

				visible := e.Screen(target).Text
				if strings.Contains(visible, "ln-1\n") {
					e.T.Fatalf("the first line is still visible on a 24-row screen, so nothing scrolled off and this case proves nothing")
				}

				withHistory, err := e.Backend.Screen(e.Ctx(), target, backend.ScreenOpts{HistoryLines: 500})
				if err != nil {
					e.T.Fatalf("capturing with history: %v", err)
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
			Name: "§5.3 an alt-screen pane captures empty with the flag set",
			Fn: func(e *Env) {
				// Empty is the designed answer here, and the flag is what
				// makes it mean "skipped by design" rather than "nothing
				// there". A caller cannot tell the two apart without it.
				target := e.StartCommand("sh", "-c", `printf 'alt-%d\n' 1; printf '\033[?1049h'; sleep 30`)
				e.WaitFor(target, "alt-1")

				capture := e.Screen(target)
				if !capture.Meta.AltScreen {
					e.T.Fatalf("alt_screen is not set for a pane on the alternate screen. Screen was:\n%s", capture.Text)
				}
				if strings.TrimSpace(capture.Text) != "" {
					e.T.Errorf("an alt-screen pane captured %q, want the empty string", capture.Text)
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
