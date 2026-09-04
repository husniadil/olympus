package backend

import "context"

// An Agent is a coding agent running in a pane, as far as the backend can tell
// (behavior §3.7). Every backend answers the listing; what a row can carry
// depends on how the agent was found, and DetectedBy says which.
type Agent struct {
	PaneID      string `json:"pane_id"`
	SessionName string `json:"session_name"`
	SessionID   string `json:"session_id"`
	// Agent is the agent's canonical name: one of the command heuristic's
	// vocabulary (claude, codex, gemini, aider, opencode, goose, amp, cursor,
	// pi, omp, copilot, devin, agy, cline, droid, kimi, kiro, kilo, hermes,
	// qodercli, qwen, mastracode, maki, muse, grok), or whatever a
	// natively-detecting backend reports.
	Agent string `json:"agent"`
	// Status is working, idle or unknown. It is unknown wherever only the
	// command name was seen: the heuristic MUST NOT invent one.
	Status string `json:"status"`
	// Title is what the agent is working on, where the backend reports it.
	Title string `json:"title,omitempty"`
	// CWD is the directory the agent is working in.
	CWD string `json:"cwd"`
	// DetectedBy is how the row was found: the backend's own detection
	// (herdr), which carries status, title and usage; or a known agent's
	// name in the pane's process tree — its foreground command where the
	// pane has no PID — which carries none of them.
	DetectedBy string `json:"detected_by"`
	// Usage is the agent's quota readout where the backend reports one, in
	// the order the backend lists it.
	Usage []AgentUsage `json:"usage,omitempty"`
}

// An AgentUsage is one quota bar: a short label (5h, 7d, a model name) and
// the percent of it used, 0-100.
type AgentUsage struct {
	Label   string `json:"label"`
	Percent int    `json:"percent"`
}

// Agent status values.
const (
	AgentWorking = "working"
	AgentIdle    = "idle"
	AgentUnknown = "unknown"
)

// Agent detection sources.
const (
	// DetectedByNative is the backend's own agent detection: it saw the
	// agent as an agent, and the row carries its status and title.
	DetectedByNative = "herdr"
	// DetectedByCommand is the process-tree heuristic the ergonomic layer
	// applies where the backend has no detection of its own: a known
	// agent's name under the pane's PID, or in its foreground command where
	// there is no PID.
	DetectedByCommand = "command"
)

// An AgentLister enumerates the agents a backend detects itself. It is
// optional in a different way from ServerLister: a backend without it is not
// unsupported, the layer above derives the rows from the pane listing instead
// (behavior §3.7). Implementing it means the backend can report status, and
// its Capabilities MUST say so in AgentStatus.
type AgentLister interface {
	Agents(ctx context.Context) ([]Agent, error)
}
