package herdr

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/husniadil/olympus/backend"
)

// An agentRow is the part of `herdr agent list` Olympus reads. The subcommand
// prints its envelope as JSON without being asked, and refuses `--json`
// (measured on 0.8.2).
type agentRow struct {
	PaneID      string `json:"pane_id"`
	WorkspaceID string `json:"workspace_id"`
	Agent       string `json:"agent"`
	Status      string `json:"agent_status"`
	CWD         string `json:"cwd"`
	// Title is the terminal title with the agent's own status glyph removed,
	// which is the title as a human would repeat it.
	Title string `json:"terminal_title_stripped"`
	// Tokens is the agent's display metadata, where the usage bars live as
	// `usage_<n>` entries.
	Tokens map[string]string `json:"tokens"`
	// Session is the agent's own conversation as herdr stores it, reported
	// to herdr by the agent's integration hook; absent where none was.
	Session *backend.AgentSession `json:"agent_session"`
}

// usageToken is one usage bar as herdr renders it: `-    5h: ▰▰▰▱▱▱▱▱▱▱  33%`.
// The label is what precedes the colon, the percent is the last number
// before the sign.
var usageToken = regexp.MustCompile(`^-\s*([^:]+):.*?(\d+)%\s*$`)

// Agents lists the agents herdr itself has detected in its panes (§3.7). herdr
// watches each pane's foreground process and terminal title, so every row
// carries a status and a title, and the usage bars where the agent reports
// them.
//
// The snapshot is read as well, because the agent row names its workspace by
// id only and a session row is named by the workspace's label (§3.6): the
// name here must be the one Sessions answers, or the two listings cannot be
// joined.
func (h *Herdr) Agents(ctx context.Context) ([]backend.Agent, error) {
	out, err := h.run(ctx, "agent", "list")
	if err != nil {
		if errors.Is(err, errNoServer) {
			// Nothing to find; nothing went wrong asking (§3.3).
			return []backend.Agent{}, nil
		}
		return nil, err
	}
	snap, err := h.snapshot(ctx)
	if err != nil {
		return nil, err
	}
	agents, err := parseAgents(out, snap)
	if err != nil {
		return nil, err
	}
	// The agent's process is a second request per row — the listing names
	// the pane, and only process-info names what holds its terminal. Paid
	// here rather than on the pane listing because an agent row is what a
	// caller goes on to ask about, and there are few of them. Best-effort:
	// a row whose pane vanished, or whose info cannot be read, keeps its pid
	// at zero rather than failing the listing.
	for i := range agents {
		agents[i].PID = h.agentPID(ctx, agents[i].PaneID)
	}
	return agents, nil
}

// agentPID reports the process holding a pane's terminal right now: the
// leader of the foreground process group, which is the agent while the
// agent has the pane. Zero where herdr cannot say.
func (h *Herdr) agentPID(ctx context.Context, paneID string) int {
	out, err := h.run(ctx, "pane", "process-info", "--pane", paneID)
	if err != nil {
		return 0
	}
	return parseAgentPID(out)
}

// parseAgentPID reads the foreground process group leader out of a
// `pane process-info` reply; zero where the reply does not carry one.
func parseAgentPID(out string) int {
	var info struct {
		Result struct {
			ProcessInfo struct {
				ForegroundProcessGroupID int `json:"foreground_process_group_id"`
			} `json:"process_info"`
		} `json:"result"`
	}
	if err := json.Unmarshal([]byte(out), &info); err != nil {
		return 0
	}
	return info.Result.ProcessInfo.ForegroundProcessGroupID
}

// parseAgents reads the rows out of `herdr agent list`, naming each
// row's session the way the snapshot does.
func parseAgents(out string, snap snapshot) ([]backend.Agent, error) {
	var reply struct {
		Result struct {
			Agents []agentRow `json:"agents"`
		} `json:"result"`
	}
	if err := json.Unmarshal([]byte(out), &reply); err != nil {
		return nil, backend.Wrapf(backend.CodeUnexpected, err, "reading the herdr agent listing")
	}
	agents := make([]backend.Agent, 0, len(reply.Result.Agents))
	for _, row := range reply.Result.Agents {
		name := row.WorkspaceID
		if ws, ok := snap.workspaceByID(row.WorkspaceID); ok {
			name = displayName(ws)
		}
		agents = append(agents, backend.Agent{
			PaneID:      row.PaneID,
			SessionName: name,
			SessionID:   row.WorkspaceID,
			Agent:       row.Agent,
			Status:      agentStatus(row.Status),
			Title:       row.Title,
			CWD:         row.CWD,
			DetectedBy:  backend.DetectedByNative,
			Usage:       parseUsage(row.Tokens),
			// Verbatim: an id or a path is herdr's to spell, and a row with
			// none carries none.
			AgentSession: row.Session,
		})
	}
	return agents, nil
}

// agentStatus maps herdr's status onto the shared vocabulary. Anything herdr
// spells that is not one of the known states is unknown rather than passed
// through: the vocabulary is semver-bound and a new spelling upstream must not
// appear on the wire unannounced. blocked — the agent waiting on a person —
// was folded into unknown until 0.12.0, which hid the one state a caller most
// needs to act on.
//
// herdr spells an idle agent two ways and both are idle here. `done` is one
// nobody has looked at since it stopped, `idle` one somebody has (herdr's own
// pane_agent_status, since 0.4.5). The difference is a fact about the
// OPERATOR's attention rather than about the agent, and only a backend that
// draws the panes can know it — a status read off a capture never can — so it
// has no place in a vocabulary four backends share. A caller that wants it
// wants a field of its own, honest about being herdr's alone.
//
// Folding it in was five months late: `done` shipped in herdr 0.4.5 and every
// finished agent read as unknown here until now, which is the state a caller
// takes as "cannot say" for the one that says the turn is over.
func agentStatus(s string) string {
	switch s {
	case backend.AgentWorking, backend.AgentIdle, backend.AgentBlocked:
		return s
	case herdrDone:
		return backend.AgentIdle
	}
	return backend.AgentUnknown
}

// herdrDone is herdr's spelling for an idle agent nobody has looked at since
// it stopped. Named here rather than inline because it is herdr's word, not
// one of ours.
const herdrDone = "done"

// parseUsage reads the usage bars out of an agent's tokens: every `usage_<n>`
// key in numeric order, each rendered as a label, a bar and a percent. A value
// that does not parse is skipped rather than failing the row — the bars are
// display text herdr owns the shape of, and a row is an agent with or without
// them.
func parseUsage(tokens map[string]string) []backend.AgentUsage {
	type keyed struct {
		n     int
		value string
	}
	var bars []keyed
	for key, value := range tokens {
		rest, ok := strings.CutPrefix(key, "usage_")
		if !ok {
			continue
		}
		n, err := strconv.Atoi(rest)
		if err != nil {
			continue
		}
		bars = append(bars, keyed{n, value})
	}
	sort.Slice(bars, func(i, j int) bool { return bars[i].n < bars[j].n })
	var usage []backend.AgentUsage
	for _, bar := range bars {
		m := usageToken.FindStringSubmatch(bar.value)
		if m == nil {
			continue
		}
		percent, err := strconv.Atoi(m[2])
		if err != nil || percent < 0 || percent > 100 {
			continue
		}
		usage = append(usage, backend.AgentUsage{Label: strings.TrimSpace(m[1]), Percent: percent})
	}
	return usage
}
