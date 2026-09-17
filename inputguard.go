package olympus

import (
	"context"

	"github.com/husniadil/olympus/backend"
	"github.com/husniadil/olympus/internal/agentstate"
	"github.com/husniadil/olympus/internal/engine"
)

// inspectInput is a verified send's reading of its target before anything is
// typed (behavior §7.5, §7.6). It names the agents the target holds, refuses
// one whose screen shows it waiting on a person, and hands back the watch the
// echo is polled with.
//
// A target that holds no known agent keeps the whole-screen match, and so
// does a listing that cannot be read: the guard exists for agents, and a
// shell must not stop accepting input because a process table could not be.
func (o *Olympus) inspectInput(ctx context.Context, target string) (engine.Watch, error) {
	agents := o.agentsAt(ctx, target)
	if len(agents) == 0 {
		return nil, nil
	}
	blocked := func(screen string, typed bool) error {
		in := agentstate.Input{Screen: o.detectionScreen(screen)}
		for _, agent := range agents {
			if agentstate.Detect(agent, in) != agentstate.Blocked {
				continue
			}
			if typed {
				err := backend.Errorf(backend.CodeAgentBlocked,
					"the %s agent in %s started waiting on a person after the text was typed, so it was not submitted: the text may be in its prompt or its input box, so read the screen before answering with press", agent, target)
				err.Typed = true
				return err
			}
			return backend.Errorf(backend.CodeAgentBlocked,
				"the %s agent in %s is waiting on a person, so nothing was typed: answer its prompt with press, or send once it is not waiting", agent, target)
		}
		return nil
	}

	// A capture that fails here is not a refusal: the watch reads every
	// capture again while the echo is polled for, and stops there.
	//
	// The same capture counts the box's paste placeholders (§7.6). Without
	// it they are unknown, and a placeholder is not taken as the echo.
	// Counted in the box where one is drawn, and on the whole screen where it
	// is not (an agent whose manifest names no box, such as Codex).
	boxPastes, screenPastes := -1, -1
	if capture, err := o.backend.Screen(ctx, target, backend.ScreenOpts{}); err == nil {
		if err := blocked(capture.Text, false); err != nil {
			return nil, err
		}
		in := agentstate.Input{Screen: o.detectionScreen(capture.Text)}
		screenPastes = agentstate.Pastes(in.Screen)
		for _, agent := range agents {
			box, drawn := agentstate.Composer(agent, in)
			if drawn {
				boxPastes = agentstate.Pastes(box)
				break
			}
			// An agent that draws a box and shows none has something else
			// taking the keys (§7.5): the Enter would answer that instead.
			if agentstate.HasComposer(agent) {
				return nil, backend.Errorf(backend.CodeAgentBlocked,
					"the %s agent in %s is not showing its input box, so nothing was typed: something is open over it, such as a rewind list or a picker; close it with press, or send once the box is back", agent, target)
			}
		}
	}

	return func(screen, head, tail string) (bool, error) {
		if err := blocked(screen, true); err != nil {
			return false, err
		}
		in := agentstate.Input{Screen: o.detectionScreen(screen)}
		for _, agent := range agents {
			if box, drawn := agentstate.Composer(agent, in); drawn {
				if boxPastes >= 0 && agentstate.Pastes(box) > boxPastes {
					return true, nil
				}
				return engine.ScreenContains(box, head) || engine.ScreenContains(box, tail), nil
			}
			// No box on this capture: nothing on the rest of the screen is
			// the echo (§7.6), so the poll waits for the box to come back.
			if agentstate.HasComposer(agent) {
				return false, nil
			}
		}
		if screenPastes >= 0 && agentstate.Pastes(in.Screen) > screenPastes {
			return true, nil
		}
		return engine.ScreenContains(screen, head) || engine.ScreenContains(screen, tail), nil
	}, nil
}

// agentsAt names the agents a target holds.
//
// Only a target of one pane is named: a capture addresses the active pane
// (§10), and where there are several nothing says which one input lands in.
// On a backend that lists agents itself the row for that pane names it, and
// an agent in another pane of the same session is not on the screen read.
// Elsewhere the pane's own process tree or foreground command names it.
func (o *Olympus) agentsAt(ctx context.Context, target string) []string {
	panes, err := o.Panes(ctx, target)
	if err != nil || len(panes) != 1 {
		return nil
	}
	var names []string
	if lister, ok := o.backend.(backend.AgentLister); ok {
		rows, err := lister.Agents(ctx)
		if err != nil {
			return nil
		}
		for _, row := range rows {
			if row.PaneID == panes[0].ID {
				names = append(names, row.Agent)
			}
		}
		return names
	}
	if name, _, ok := agentOf(panes[0], o.processTreeFor(ctx, panes)); ok {
		names = append(names, name)
	}
	return names
}

// detectionScreen is a capture as the manifests read it: trailing blanks off
// every line, and on a backend whose capture is the whole scrollback, the
// tail that stands in for the viewport (see screenStatus).
func (o *Olympus) detectionScreen(screen string) string {
	screen = trimLineEnds(screen)
	if o.backend.Capabilities().NativeScrollback {
		screen = tailLines(screen, detectionRows)
	}
	return screen
}
