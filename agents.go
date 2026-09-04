package olympus

import (
	"context"
	"path/filepath"
	"strings"

	"github.com/husniadil/olympus/backend"
)

// agentCommands are the foreground commands that mark a pane as an agent's
// where the backend has no detection of its own (behavior §3.7). Matched
// against the base name of the command's first token, case-sensitively:
// these are the binaries' own names, and a different spelling is a different
// program.
var agentCommands = map[string]bool{
	"claude":       true,
	"codex":        true,
	"gemini":       true,
	"aider":        true,
	"opencode":     true,
	"goose":        true,
	"amp":          true,
	"cursor-agent": true,
}

// Agents lists the agents running in panes on the resolved backend.
//
// Every backend answers; this is never unsupported. A backend that detects
// agents itself (backend.AgentLister) reports rows with a status and a title,
// and says so in Capabilities.AgentStatus. Everywhere else the rows are
// derived from the pane listing: a pane whose foreground command is a known
// agent is an agent, with `detected_by: "command"` and a status of unknown —
// the heuristic knows the command's name and nothing else, and MUST NOT
// invent a state it cannot see (behavior §3.7).
func (o *Olympus) Agents(ctx context.Context) ([]backend.Agent, error) {
	if lister, ok := o.backend.(backend.AgentLister); ok {
		agents, err := lister.Agents(ctx)
		if agents == nil && err == nil {
			agents = []backend.Agent{}
		}
		return agents, err
	}
	panes, err := o.Panes(ctx, "")
	if err != nil {
		return nil, err
	}
	agents := []backend.Agent{}
	for _, pane := range panes {
		name, ok := agentCommand(pane.CurrentCommand)
		if !ok {
			continue
		}
		agents = append(agents, backend.Agent{
			PaneID:      pane.ID,
			SessionName: pane.SessionName,
			SessionID:   pane.SessionID,
			Agent:       name,
			Status:      backend.AgentUnknown,
			CWD:         pane.CurrentPath,
			DetectedBy:  backend.DetectedByCommand,
		})
	}
	return agents, nil
}

// agentCommand reports which agent a pane's current command is, if any: the
// base name of its first token, so `/usr/local/bin/claude --resume` is claude
// and a shell is nothing.
func agentCommand(command string) (string, bool) {
	fields := strings.Fields(command)
	if len(fields) == 0 {
		return "", false
	}
	name := filepath.Base(fields[0])
	return name, agentCommands[name]
}
