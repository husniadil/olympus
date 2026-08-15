package backendtest

import "github.com/husniadil/olympus/backend"

func listingCases() []Case {
	return []Case{
		{
			Name: "§3.3 an empty listing is a real answer, not an error and not a default",
			Fn: func(e *Env) {
				// Nothing has been created in this case's isolated backend, so
				// there may be no server behind the socket at all. That is
				// "nothing to find here", not "something went wrong asking".
				before, err := e.Backend.Sessions(e.Ctx())
				if err != nil {
					e.T.Fatalf("listing with nothing running returned an error: %v", err)
				}
				if len(before) != 0 {
					e.T.Fatalf("listing returned %d sessions before anything was created", len(before))
				}

				// The round trip is the point. An empty list on its own proves
				// nothing — a backend that always answers "nothing here"
				// satisfies it perfectly — so the case also requires the
				// listing to notice a session appearing and disappearing.
				target := e.StartShell()
				if got := len(e.sessions()); got != 1 {
					e.T.Fatalf("listing returned %d sessions after creating one, want 1", got)
				}
				if err := e.Backend.Kill(e.Ctx(), target); err != nil {
					e.T.Fatalf("killing: %v", err)
				}
				if got := len(e.sessions()); got != 0 {
					e.T.Errorf("listing returned %d sessions after the only one was killed, want none", got)
				}
			},
		},
		{
			Name: "§3.2 every listed session carries a backend-owned liveness classification",
			Fn: func(e *Env) {
				// The classification must come from the backend. A listing
				// that synthesizes every row as alive gives consumers no gone
				// signal at all, and dead rows then survive reconciliation
				// forever.
				target := e.StartShell()
				row := e.SessionNamed(target)
				switch row.Liveness {
				case backend.LivenessPresent, backend.LivenessGone, backend.LivenessUnknown:
				default:
					e.T.Errorf("session %s has liveness %q, want one of present, gone or unknown", target, row.Liveness)
				}
				if row.Liveness != backend.LivenessPresent {
					e.T.Errorf("a live session is classified %q, want %q", row.Liveness, backend.LivenessPresent)
				}
			},
		},
		{
			Name: "§3.2 every listed pane carries a liveness classification",
			Fn: func(e *Env) {
				target := e.StartShell()
				panes := e.panes(target)
				for _, p := range panes {
					switch p.Liveness {
					case backend.LivenessPresent, backend.LivenessGone, backend.LivenessUnknown:
					default:
						e.T.Errorf("pane %s has liveness %q, want one of the three", p.ID, p.Liveness)
					}
				}
			},
		},
		{
			Name: "§3.4 created_at is a plausible epoch on every pane row",
			Fn: func(e *Env) {
				// Asserted unconditionally, never gated behind non-zero: a
				// wrong format variable expands to empty with exit 0, so a
				// gated assertion would silently assert nothing at all.
				target := e.StartShell()
				for _, p := range e.panes(target) {
					if !PlausibleEpoch(p.CreatedAt) {
						e.T.Errorf("pane %s has created_at %d, which is not a plausible epoch for a session created just now", p.ID, p.CreatedAt)
					}
				}
			},
		},
		{
			Name: "§3.4 current_command is populated for a spawned command",
			Fn: func(e *Env) {
				// Asserted non-empty rather than equal to a fixed string: the
				// field means different things per backend (live foreground
				// process on one, static spawn argv on another) and the shell
				// binary genuinely differs across environments.
				//
				// The exec-spawned shape is used because it is the one shape
				// where both meanings produce a value — a bare shell session
				// legitimately carries none on a backend that reports the
				// spawn argv.
				target := e.StartCommand("sh", "-c", `printf 'cmd-%d\n' 2; sleep 30`)
				e.WaitFor(target, "cmd-2")
				for _, p := range e.panes(target) {
					if p.CurrentCommand == "" {
						e.T.Errorf("pane %s has an empty current_command for an exec-spawned session", p.ID)
					}
				}
			},
		},
		{
			Name: "§3.4 pane rows name their owning session",
			Fn: func(e *Env) {
				// Target resolution depends on this: a pane row that does not
				// name its session cannot be swapped for one, and every
				// pane-id caller silently mismatches every name (§10).
				target := e.StartShell()
				for _, p := range e.panes(target) {
					if p.SessionName != target {
						e.T.Errorf("pane %s names session %q, want %q", p.ID, p.SessionName, target)
					}
					if p.ID == "" {
						e.T.Errorf("pane row for %s has no pane id", target)
					}
				}
			},
		},
		{
			Name: "§3.5 a name that never existed probes absent, not error",
			Fn: func(e *Env) {
				// Absent even with no server running: the error arm is
				// reserved for a genuinely unreachable backend, and collapsing
				// the two would destroy the distinction a reconciling caller
				// needs between definitely-gone and could-not-ask.
				if got := e.Backend.Probe(e.Ctx(), e.Name()); got != backend.StateAbsent {
					e.T.Errorf("probe of a name that never existed is %q, want %q", got, backend.StateAbsent)
				}
			},
		},
	}
}

func (e *Env) panes(target string) []backend.Pane {
	e.T.Helper()
	panes, err := e.Backend.Panes(e.Ctx(), target)
	if err != nil {
		e.T.Fatalf("listing panes of %s: %v", target, err)
	}
	if len(panes) == 0 {
		e.T.Fatalf("session %s has no panes", target)
	}
	return panes
}

func (e *Env) sessions() []backend.Session {
	e.T.Helper()
	sessions, err := e.Backend.Sessions(e.Ctx())
	if err != nil {
		e.T.Fatalf("listing sessions: %v", err)
	}
	return sessions
}
