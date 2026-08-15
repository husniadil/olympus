package backend

// A Name identifies a backend. It is the key sessions are scoped by: the same
// session name on tmux and on zmx are different sessions, which is why every
// envelope discloses the resolved backend (behavior §0.4).
type Name string

const (
	Tmux Name = "tmux"
	Zmx  Name = "zmx"
)

// A Liveness classifies whether a row's session is alive, produced by the
// backend and never by a consumer parsing error strings (behavior §3.2).
type Liveness string

const (
	// LivenessPresent is a live session the backend vouches for.
	LivenessPresent Liveness = "present"
	// LivenessGone is positive evidence of death: safe to finalize and reap.
	LivenessGone Liveness = "gone"
	// LivenessUnknown is a row that exists but could not be confirmed this
	// pass. Consumers MUST treat it as present for reap purposes — never
	// finalize on doubt.
	LivenessUnknown Liveness = "unknown"
)

// OrUnknown resolves an unset classification to LivenessUnknown. The zero value
// of a tri-state is the doubtful arm, not the confident one: a backend that
// forgets to classify a row must not thereby claim it is gone, and must not
// emit an empty string no consumer branches on.
func (l Liveness) OrUnknown() Liveness {
	if l == "" {
		return LivenessUnknown
	}
	return l
}

// A State is the presence probe's tri-state answer (behavior §3.5). It is
// deliberately distinct from Liveness: this answers "does the target exist",
// where StateError means the backend could not be asked at all.
type State string

const (
	StatePresent State = "present"
	StateAbsent  State = "absent"
	StateError   State = "error"
)

// An Outcome reports what starting a session actually did (behavior §2.6). It
// appears only on start.
type Outcome string

const (
	OutcomeCreated Outcome = "created"
	OutcomeReused  Outcome = "reused"
	OutcomeReaped  Outcome = "reaped"
)

// A Session is one listed session row.
type Session struct {
	Name     string   `json:"name"`
	ID       string   `json:"id"`
	Attached bool     `json:"attached"`
	Dead     bool     `json:"dead"`
	Liveness Liveness `json:"liveness"`
	CWD      string   `json:"cwd"`
	// Outcome is set by start and left empty everywhere else, so a listing row
	// never implies an action was taken.
	Outcome Outcome `json:"outcome,omitempty"`
}

// A Pane is one listed pane row.
//
// Three of these fields mean genuinely different things per backend and MUST be
// documented at every door rather than reported as equivalent (behavior §3.4):
// CreatedAt is session-granular on both backends; CurrentPath is live on tmux
// and static on zmx; CurrentCommand is the live foreground process on tmux and
// the static spawn argv on zmx.
//
// ID is not unique across rows once a grouped view exists, since a base session
// and its views share the same underlying pane. A consumer needing one row per
// logical session dedupes by ID, keeping the earliest CreatedAt.
type Pane struct {
	ID          string `json:"pane_id"`
	SessionName string `json:"session_name"`
	SessionID   string `json:"session_id"`
	WindowIndex int    `json:"window_index"`
	Dead        bool   `json:"dead"`
	// CreatedAt is Unix seconds.
	CreatedAt      int64    `json:"created_at"`
	CurrentPath    string   `json:"current_path"`
	CurrentCommand string   `json:"current_command"`
	Liveness       Liveness `json:"liveness"`
}

// A ScreenMeta carries what a capture could not put in the text itself
// (behavior §5.4). AltScreen is what makes an empty capture mean "skipped by
// design" rather than "nothing there".
type ScreenMeta struct {
	AltScreen bool `json:"alt_screen"`
	// ScrollPosition is lines scrolled up from the live bottom, 0 when not in
	// copy mode. tmux-only; zmx is always the zero value.
	ScrollPosition int `json:"scroll_position"`
}

// Capabilities are the static, subprocess-free backend facts a consumer
// feature-probes before hitting an unsupported error (behavior §13).
//
// Backend is carried on the value so a Capabilities is self-describing in Go,
// but is not marshalled: on the wire capabilities always hang off a row that
// already names the backend, and repeating it there would be a second place
// for the two to disagree.
//
// There is deliberately no capability for whether a session outlives its
// command. That is a property of the caller's own wrapper, not of backend
// mechanics.
type Capabilities struct {
	Backend          Name `json:"-"`
	NativeScrollback bool `json:"native_scrollback"`
	Views            bool `json:"views"`
	RemainOnExit     bool `json:"remain_on_exit"`
	ServerEnv        bool `json:"server_env"`
	// ControlKeys reports whether control keys reach the session.
	//
	// This is the capability that decides whether a full-screen program can be
	// DRIVEN, as opposed to merely started and read. An editor is left with
	// Ctrl-X and saved with Ctrl-O; a pager is scrolled with Ctrl-F. Where
	// these are not delivered a caller can open such a program and watch it,
	// and never get out of it.
	//
	// Measured, rather than assumed from the backend's documentation: sending
	// each byte to `cat -v` and reading back what arrived. tmux delivers the
	// control range, tab and escape. zmx delivers printable text, tab and the
	// terminator, but drops the control letters, a lone escape, and the arrow
	// and home keys — while passing page-up and the function keys. The boundary
	// is not fully characterized and is not worth characterizing: what a caller
	// needs to know is that control keys cannot be relied on there.
	ControlKeys bool `json:"control_keys"`
	// TracksAltScreen reports whether capture metadata's alt-screen flag
	// means anything on this backend. Without it a caller cannot tell "this
	// pane is not on the alternate screen" from "this backend does not
	// track that", and an empty capture is ambiguous in exactly the way the
	// flag exists to prevent (behavior §5.3).
	TracksAltScreen bool `json:"tracks_alt_screen"`
}
