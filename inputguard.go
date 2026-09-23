package olympus

import (
	"context"
	"time"
	"unicode/utf8"

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
func (o *Olympus) inspectInput(ctx context.Context, target, text string) (engine.Watch, error) {
	agents, title := o.agentsAt(ctx, target)
	if len(agents) == 0 {
		return nil, nil
	}
	blocked := func(screen string, typed bool) error {
		in := agentstate.Input{Screen: o.detectionScreen(screen), OSCTitle: title}
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
	// The box is read off the whole capture, not the detection tail: on a
	// backend whose capture is its scrollback, a tall draft pushes the box's
	// top rule out of the tail, and the box would read as gone.
	composer := func(screen string) (string, bool, bool) {
		in := agentstate.Input{Screen: trimLineEnds(screen)}
		for _, agent := range agents {
			if box, drawn := agentstate.Composer(agent, in); drawn {
				return box, true, true
			}
			if agentstate.HasComposer(agent) {
				return "", false, true
			}
		}
		return "", false, false
	}
	drawn := func(screen string) bool {
		_, ok, _ := composer(screen)
		return ok
	}
	chars := utf8.RuneCountInString(text)

	// A capture that fails here is not a refusal: the watch reads every
	// capture again while the echo is polled for, and stops there.
	//
	// The same capture counts the paste placeholders already shown (§7.6):
	// in the box where one is drawn, and on the whole screen where the agent
	// names none (Codex). Without it they are unknown, and no placeholder is
	// taken as the echo.
	boxPastes, codexPastes := -1, -1
	if capture, err := o.backend.Screen(ctx, target, backend.ScreenOpts{}); err == nil {
		if err := blocked(capture.Text, false); err != nil {
			return nil, err
		}
		screen := capture.Text
		if _, ok, hasBox := composer(screen); hasBox && !ok {
			// A screen drawn wrong reads the same as a box that is not
			// there, so the agent is asked to draw it again first (§7.5).
			if again, ok := o.redrawn(ctx, target, drawn); ok {
				// A redraw can show a prompt that was drawn wrong too, and
				// a question reads as a box.
				if err := blocked(again, false); err != nil {
					return nil, err
				}
				screen = again
			}
		}
		box, boxDrawn, hasBox := composer(screen)
		switch {
		case boxDrawn:
			boxPastes = agentstate.Pastes(box)
		case hasBox:
			// An agent that draws a box and shows none has something else
			// taking the keys, or has not drawn it yet (§7.5): an Enter
			// would answer whatever is there.
			return nil, backend.Errorf(backend.CodeAgentBlocked,
				"the %s agent in %s is not showing its input box, so nothing was typed: it may still be starting, or something is open over it such as a rewind list or a picker; send again once the box is back, or close what is open with press", agents[0], target)
		default:
			codexPastes = agentstate.PastedContent(trimLineEnds(screen), chars)
		}
	}

	// A paste can reach the agent in pieces, each drawn as its own
	// placeholder: a count that rose is taken only once the next capture shows
	// the same count, so the Enter does not land while pieces still arrive.
	lastRisen := -1
	return func(screen, head, tail string) (bool, error) {
		if err := blocked(screen, true); err != nil {
			return false, err
		}
		if _, ok, hasBox := composer(screen); hasBox && !ok {
			// Asked each time the box reads as gone: one that stays gone
			// after a redraw has something open over it, and the send stops
			// below.
			if again, ok := o.redrawn(ctx, target, drawn); ok {
				if err := blocked(again, true); err != nil {
					return false, err
				}
				screen = again
			}
		}
		box, boxDrawn, hasBox := composer(screen)
		switch {
		case boxDrawn:
			if n := agentstate.Pastes(box); boxPastes >= 0 && n > boxPastes {
				settled := n == lastRisen
				lastRisen = n
				return settled, nil
			}
			return engine.ScreenContains(box, head) || engine.ScreenContains(box, tail), nil
		case hasBox:
			// The box went after the text was typed: something opened over
			// it, and a resend would type into that.
			err := backend.Errorf(backend.CodeAgentBlocked,
				"the %s agent in %s stopped showing its input box after the text was typed, so it was not submitted: something opened over it, and the text may be in its input box, so read the screen before sending again", agents[0], target)
			err.Typed = true
			return false, err
		}
		if codexPastes >= 0 && agentstate.PastedContent(trimLineEnds(screen), chars) > codexPastes {
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
// Elsewhere the pane's own process tree or foreground command names it, and
// the pane's title is handed back with it: the agent listing reads a
// command-detected agent's status off its title as well as its screen
// (§3.7), and a manifest can state a blocker in the title alone. The title is
// read once, before anything is typed.
func (o *Olympus) agentsAt(ctx context.Context, target string) ([]string, string) {
	panes, err := o.Panes(ctx, target)
	if err != nil || len(panes) != 1 {
		return nil, ""
	}
	var names []string
	if lister, ok := o.backend.(backend.AgentLister); ok {
		rows, err := lister.Agents(ctx)
		if err != nil {
			return nil, ""
		}
		for _, row := range rows {
			if row.PaneID == panes[0].ID {
				names = append(names, row.Agent)
			}
		}
		return names, ""
	}
	if name, _, ok := agentOf(panes[0], o.processTreeFor(ctx, panes)); ok {
		names = append(names, name)
	}
	return names, agentTitle(panes[0])
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

// redrawWait bounds how long a redrawn screen is read for the agent's box,
// and redrawPoll is how often it is read in that time.
const (
	redrawWait = time.Second
	redrawPoll = 50 * time.Millisecond
)

// redrawn asks the process in the target's pane to draw its screen again, and
// reads the screen until drawn says the agent's box is on it (§7.5). A backend
// that cannot ask, a request that fails, and a box still missing when the wait
// is over are all false: the caller then treats the box as gone.
func (o *Olympus) redrawn(ctx context.Context, target string, drawn func(string) bool) (string, bool) {
	r, ok := o.backend.(backend.Redrawer)
	if !ok || r.Redraw(ctx, target) != nil {
		return "", false
	}
	deadline := time.Now().Add(redrawWait)
	for {
		if capture, err := o.backend.Screen(ctx, target, backend.ScreenOpts{}); err == nil && drawn(capture.Text) {
			return capture.Text, true
		}
		if time.Now().After(deadline) {
			return "", false
		}
		select {
		case <-ctx.Done():
			return "", false
		case <-time.After(redrawPoll):
		}
	}
}
