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
	// Status is working, idle, blocked or unknown. Blocked is the agent
	// waiting on a person — a permission prompt, a question. It is unknown
	// wherever nothing showed a state: the listing MUST NOT invent one.
	Status string `json:"status"`
	// StatusSource is where a known status came from: native, the backend's
	// own detection; or screen, read off a capture of the pane by the
	// agent's manifest. Omitted when the status is unknown.
	StatusSource string `json:"status_source,omitempty"`
	// Title is what the agent is working on, where the backend reports it.
	Title string `json:"title,omitempty"`
	// CWD is the directory the agent is working in.
	CWD string `json:"cwd"`
	// DetectedBy is how the row was found: the backend's own detection
	// (herdr), which carries status, title and usage; or a known agent's
	// name in the pane's process tree — its foreground command where the
	// pane has no PID — which carries a status only where the pane's screen
	// could be read, and never a title or usage.
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
	AgentBlocked = "blocked"
	AgentUnknown = "unknown"
)

// Agent status sources.
const (
	// StatusSourceNative is a status the backend reported itself.
	StatusSourceNative = "native"
	// StatusSourceScreen is a status read off a capture of the pane.
	StatusSourceScreen = "screen"
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
// (behavior §3.7). Implementing it means the backend reports status itself;
// its Capabilities MUST say so in AgentStatus, as MUST any backend whose
// panes can be captured, since the layer above reads status off the screen.
type AgentLister interface {
	Agents(ctx context.Context) ([]Agent, error)
}
