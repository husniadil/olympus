// Package herdr drives herdr.
//
// The rules it implements are specified in docs/terminal-behavior.md, and it is
// proved against backend/backendtest. Where a comment here names a section, the
// obvious implementation is the wrong one and the section says why.
//
// herdr's hierarchy is workspace › tab › pane, and it maps onto Olympus's
// session › window › pane the way tmux's does: a workspace is a session (named
// by its label, else by its id, "w5"), a tab is a window ("w5:t2", whose number
// is the window index), and a pane is a pane ("w5:p3"). A verb aimed at a
// workspace or a tab acts on the pane it is showing. The mapping, the target
// shapes and what each verb does at each level are specified once, in
// behavior §3.6; this file cites it rather than restating it.
//
// One structural difference shapes creation. A pane's process is the shell
// herdr's own configuration names — there is no per-pane argv anywhere in its
// request vocabulary — so a command at creation is refused rather than typed
// (§2.3.1).
package herdr

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/husniadil/olympus/backend"
)

// sunPathMax is the platform's limit on a unix socket path, NUL included.
//
// herdr needs TWO sockets and derives the second from the first: an API socket
// at the given path, and a client socket with "-client" inserted before the
// extension (src/server/socket_paths.rs:50-58). So the budget has to be checked
// against the DERIVED path, which is the longer of the two — a socket that fits
// on its own can still produce a client socket that does not, and the server
// then fails to bind with an error naming neither Olympus nor the path it chose.
const (
	sunPathMax          = 104
	clientSocketSuffix  = "-client.sock"
	socketSuffix        = ".sock"
	clientSocketOverage = len(clientSocketSuffix) - len(socketSuffix)
)

// DefaultSocketName is the socket a backend uses when none is chosen
// (behavior §17.1). It is a PATH rather than a name because herdr has no
// concept of a socket name resolved inside a directory of its own.
const DefaultSocketName = "olympus-herdr"

// serverStartTimeout bounds the wait for a server Olympus started to answer
// (behavior §17.3). It is generous because a cold server fetches its bundled
// agent-detection manifests before it settles, and a slow network there is not
// a failure to start.
const (
	serverStartTimeout = 20 * time.Second
	serverStartEnv     = "OLYMPUS_HERDR_START_TIMEOUT"
	serverStartPoll    = 100 * time.Millisecond
)

// waitDelay bounds how long a cancelled subprocess may keep a call waiting.
//
// Cancelling a context kills the CHILD, which is not enough to unblock the
// read: a grandchild inherits the same output pipe, and the copy waits on the
// pipe rather than on the process.
const waitDelay = 2 * time.Second

// A Herdr is a backend driving one herdr server, identified by its API socket
// path.
//
// The server may be one this backend started or one it merely found, and the
// difference is RECORDED rather than inferred (§2.9): only the handle that
// started a server may stop it, and only a server this handle started is given
// this backend's own configuration directory to read.
type Herdr struct {
	socketPath string
	// socketOnly is set when the socket was chosen by server NAME rather than
	// by path (WithServerSocket). The socket then belongs to a named session
	// the operator owns, and this backend addresses it without redirecting
	// herdr's configuration and state directories — the derivation StateHome
	// performs would otherwise put Olympus's own state tree inside the
	// operator's configuration directory (§13.2).
	socketOnly bool
	// serverName is the named session the socket belongs to, when the server
	// was selected by name (WithServerSocket). It is what the session client
	// attaches, since that client addresses a named session by its name and
	// not by its socket (§8.10). Empty for a path-addressed server.
	serverName string

	mu sync.Mutex
	// started is set when this handle brought the server up itself. It is
	// never set by observing one — a server that was already answering might
	// be a box's own headless herdr, an operator's, or one an earlier Olympus
	// process left behind, and none of those are ours to stop.
	started bool
	// serverExited is set when the child this handle spawned has exited with
	// an error, which a server of its own never does: it lost the start to
	// somebody else's server, and the one answering is not ours to claim.
	serverExited bool
}

// startedTheServer reports whether this handle brought the answering server up.
func (h *Herdr) startedTheServer() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.started
}

// noteStarted records the answering server as this handle's own, unless the
// child it spawned has already died — in which case the answer is somebody
// else's. Both facts are read and written under one lock so the order in which
// the child's exit and the server's first answer are observed cannot leave the
// claim standing.
func (h *Herdr) noteStarted() {
	h.mu.Lock()
	defer h.mu.Unlock()
	if !h.serverExited {
		h.started = true
	}
}

// noteSpawning clears the record of a lost start, so a handle that lost once
// and starts again on a socket gone empty can claim the server it brings up.
func (h *Herdr) noteSpawning() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.serverExited = false
}

func (h *Herdr) noteServerExited() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.serverExited = true
	h.started = false
}

// An Option configures a backend.
type Option func(*Herdr)

// WithSocketPath selects the herdr API socket by PATH, used verbatim.
//
// Tests MUST set this, and pointing it somewhere private is not merely about
// which server answers. herdr keeps the unnamed session's persisted layout in
// its CONFIGURATION directory rather than beside its socket
// (src/session.rs:157-165, src/config/io.rs:29-41), so a second server on a
// private socket would still overwrite the operator's own `session.json`. This
// option therefore moves the configuration and state directories with the
// socket — see StateHome (§2.9).
func WithSocketPath(path string) Option {
	return func(h *Herdr) { h.socketPath = path }
}

// New builds a herdr backend.
func New(opts ...Option) *Herdr {
	h := &Herdr{socketPath: DefaultSocketPath()}
	for _, opt := range opts {
		opt(h)
	}
	return h
}

// DefaultSocketPath is where a backend with no chosen socket puts its server.
//
// Olympus's own socket, never the operator's: a session Olympus creates in
// somebody's live herdr would appear in their sidebar and their workspace list,
// which is a change well outside the target they named. The posture matches
// tmux's rather than zmx's, and the diagnostic says so (§17.2).
func DefaultSocketPath() string {
	return filepath.Join(os.TempDir(), DefaultSocketName, "herdr.sock")
}

// Scope reports the socket this backend drives. The lock key and the diagnostic
// identify a server by it.
func (h *Herdr) Scope() string { return h.socketPath }

// StateHome is the directory herdr's configuration and state are redirected to
// for every invocation this backend makes.
//
// It is derived from the socket rather than configurable, so the pairing cannot
// be half-applied: a caller who moved the socket and not the state would still
// be sharing the operator's persisted session layout, which is the trap this
// exists to close (§2.9).
func (h *Herdr) StateHome() string {
	dir, base := filepath.Split(h.socketPath)
	return filepath.Join(dir, strings.TrimSuffix(base, socketSuffix)+"-state")
}

func (h *Herdr) Capabilities() backend.Capabilities {
	return backend.Capabilities{
		Backend: backend.Herdr,
		// A read returns the visible grid by default and scrollback only when
		// a depth is asked for, so history is a flag on the capture rather
		// than something the backend hands back natively (§5.2).
		NativeScrollback: false,
		// No grouped-session concept: a pane belongs to one tab of one
		// workspace, and nothing makes a second independently-scrollable
		// window onto it.
		Views: false,
		// No corpse concept: a pane whose process exits is closed, and the
		// pane id stops resolving. Measured by exiting a session's shell.
		RemainOnExit: false,
		// No server-environment command of any kind. A pane's environment is
		// chosen at creation and never readable back.
		ServerEnv: false,
		// Text injection writes raw bytes straight into the pane's PTY
		// (src/app/api/panes.rs:1511-1516), so Olympus spells the keys itself
		// and every one of them arrives. Measured against `cat -v`: the
		// control letters, a lone escape, tab, backspace, the arrows, home,
		// end, page-up/down and the function keys all came back.
		ControlKeys: true,
		// Neither workspace creation nor pane splitting carries a size: a
		// pane takes the server's own geometry and is resized only by an
		// attaching client (§2.1).
		SpawnSizing: false,
		// No request carries an argv, so a session's process cannot be chosen
		// (§2.3.1).
		SpawnCommand: false,
		// Display-only pane metadata is stored by the server and read back
		// through a pane row, which is somewhere a status outlives the process
		// that set it (§13.1).
		SessionStatus: true,
		// The terminal tracks the alternate screen internally
		// (src/terminal/runtime.rs:343-346) but nothing in the socket API
		// reports it, so the flag would always be false and a caller could not
		// tell that from "not on the alternate screen" (§5.3).
		TracksAltScreen: false,
		// Named sessions are servers, listed by `herdr session list` (§13.2).
		Servers: true,
		// Two clients: the raw per-pane stream and herdr's own application,
		// which a bare attach runs with its chrome hidden (§8.10).
		SessionClient: true,
		Bare:          true,
		// The session client shows the server's one focus (§8.10).
		Focus: true,
		// workspace, tab and pane each carry a label the server renames.
		Rename: true,
		// `herdr agent list` reports each agent's own status and title (§3.7).
		AgentStatus: true,
	}
}

func (h *Herdr) Version(ctx context.Context) (string, error) {
	out, err := h.run(ctx, "--version")
	if err != nil {
		return "", err
	}
	// "herdr 0.8.2"
	fields := strings.Fields(strings.TrimSpace(out))
	if len(fields) == 0 {
		return "", backend.Errorf(backend.CodeUnexpected, "herdr reported no version")
	}
	return fields[len(fields)-1], nil
}

// validateSocketPath rejects a socket whose derived client socket would not
// fit, before any invocation.
//
// Without it the failure names neither Olympus nor the path: the server exits
// with `local socket name length exceeds capacity of sun_path of sockaddr_un`
// and every subsequent call reports no server running. Measured.
func (h *Herdr) validateSocketPath() error {
	if h.socketPath == "" {
		return backend.Errorf(backend.CodeUsage, "a herdr backend needs a socket path")
	}
	budget := sunPathMax - 1
	if length := len(h.socketPath) + clientSocketOverage; length > budget {
		return backend.Errorf(backend.CodeUsage,
			"socket path %s makes herdr's derived client socket %d bytes long, over the %d-byte budget",
			h.socketPath, length, budget)
	}
	return nil
}

func (h *Herdr) Create(ctx context.Context, spec backend.CreateSpec) (backend.Session, error) {
	// Both rejected before branching on anything and before any invocation, so
	// the contract cannot become state-dependent (§2.7, §2.3.1).
	if spec.RemainOnExit {
		return backend.Session{}, backend.Errorf(backend.CodeUnsupported,
			"herdr has no remain-on-exit: a pane whose process exits is closed")
	}
	if len(spec.Command) > 0 {
		return backend.Session{}, backend.Errorf(backend.CodeUnsupported,
			"herdr cannot spawn a session onto a command: a pane runs the shell its own configuration names, and typing the argv instead would echo it into the session and reinterpret every shell metacharacter")
	}
	if err := validateName(spec.Name); err != nil {
		return backend.Session{}, err
	}
	if err := h.ensureServer(ctx); err != nil {
		return backend.Session{}, err
	}

	// A name is the session's identity here, and herdr will happily label two
	// workspaces the same. One corrected argument fixes it, which §12 makes
	// the definition of a usage error.
	sessions, err := h.Sessions(ctx)
	if err != nil {
		return backend.Session{}, err
	}
	for _, s := range sessions {
		if s.Name == spec.Name {
			return backend.Session{}, backend.Errorf(backend.CodeUsage,
				"a session named %s already exists", spec.Name)
		}
	}

	// Initial size is accepted for interface conformance and ignored: no
	// creation request carries one, and a pane is sized by whatever client
	// attaches (§2.1). Papering over that would be a lie.
	args := []string{"workspace", "create", "--no-focus", "--label", spec.Name}
	if spec.Dir != "" {
		args = append(args, "--cwd", spec.Dir)
	}
	args = append(args, spawnEnvArgs()...)

	out, err := h.run(ctx, args...)
	if err != nil {
		return backend.Session{}, err
	}
	var created struct {
		Result struct {
			Workspace workspaceRow `json:"workspace"`
			RootPane  paneRow      `json:"root_pane"`
		} `json:"result"`
	}
	if err := json.Unmarshal([]byte(out), &created); err != nil {
		return backend.Session{}, backend.Wrapf(backend.CodeUnexpected, err, "reading the created workspace")
	}
	workspace, pane := created.Result.Workspace, created.Result.RootPane
	if workspace.WorkspaceID == "" || pane.PaneID == "" {
		return backend.Session{}, backend.Errorf(backend.CodeUnexpected,
			"herdr created a workspace that reports no workspace id or no root pane")
	}

	// The root pane carries the label too, so a pane listing — or a human in
	// the sidebar — sees which pane the session began as. A failure here is a
	// workspace whose pane could not be labelled; the workspace is the
	// session and is still addressable, but Olympus made a shape it did not
	// mean to, so it is reaped rather than left half-made (§3.6).
	if _, err := h.run(ctx, "pane", "rename", pane.PaneID, spec.Name); err != nil {
		_, _ = h.run(ctx, "workspace", "close", workspace.WorkspaceID)
		return backend.Session{}, err
	}

	return backend.Session{
		Name:     spec.Name,
		ID:       workspace.WorkspaceID,
		Attached: false,
		Dead:     false,
		Liveness: backend.LivenessPresent,
		CWD:      pane.CWD,
		Outcome:  backend.OutcomeCreated,
	}, nil
}

// validateName rejects a name that could not be told apart from an id in the
// hierarchy.
//
// Resolution reads a target shaped like "w1" as a workspace id, "w1:t2" as a
// tab and "w1:p2" as a pane (§10, §3.6), so a session answering to any of those
// spellings would be shadowed by the id — and a workspace nothing has labelled
// is NAMED by its id, so a label of that shape would make one name address two
// workspaces. It is USAGE because one corrected argument fixes it.
func validateName(name string) error {
	if name == "" {
		return backend.Errorf(backend.CodeUsage, "a session needs a name")
	}
	switch {
	case backend.IndexedPaneID(name):
		return backend.Errorf(backend.CodeUsage,
			"session name %q is spelled like a herdr pane id, which addresses a pane rather than a session", name)
	case backend.IndexedTabID(name):
		return backend.Errorf(backend.CodeUsage,
			"session name %q is spelled like a herdr tab id, which addresses a tab rather than a session", name)
	case backend.IndexedWorkspaceID(name):
		return backend.Errorf(backend.CodeUsage,
			"session name %q is spelled like a herdr workspace id, which is the name of whichever workspace has that id", name)
	}
	return nil
}

// Sessions lists every workspace on the server. Each one is an Olympus
// session, named by its label where it has one and by its workspace id where
// it has not (§3.6).
func (h *Herdr) Sessions(ctx context.Context) ([]backend.Session, error) {
	snap, err := h.snapshot(ctx)
	if err != nil {
		return nil, err
	}
	sessions := make([]backend.Session, 0, len(snap.Workspaces))
	for _, ws := range snap.Workspaces {
		sessions = append(sessions, backend.Session{
			Name: displayName(ws),
			// The workspace id is the server's own identity for it and
			// survives a rename, so it is a real id distinct from the name.
			ID: ws.WorkspaceID,
			// herdr's socket API reports no per-terminal client count, so this
			// is always false rather than sometimes wrong. Disclosed at every
			// door (§3.4).
			Attached: false,
			Dead:     false,
			// A workspace whose last pane exits is removed from the listing,
			// so every listed row is one the server vouches for. There is no
			// err field to classify and no window in which a row is
			// indeterminate (§3.2).
			Liveness: backend.LivenessPresent,
			// A workspace has no directory of its own; the pane it is showing
			// has one, and that is the pane every verb on the workspace acts
			// on (§3.6).
			CWD: snap.focusedPaneOf(ws.ActiveTabID).CWD,
			// The server has one focus, and every session client on it shows
			// this workspace; a consumer steering clients (§8.10) reads it to
			// tell a client whose target is not what it is showing.
			Focused: ws.WorkspaceID == snap.FocusedWorkspaceID,
		})
	}
	return sessions, nil
}

// Panes lists panes. An empty target is every pane on the server; a target is
// the panes it contains — one for a pane, a tab's for a tab, a workspace's for
// a workspace (§3.6).
func (h *Herdr) Panes(ctx context.Context, target string) ([]backend.Pane, error) {
	snap, err := h.snapshot(ctx)
	if err != nil {
		return nil, err
	}
	var scope resolved
	if target != "" {
		scope, err = snap.resolve(target)
		if err != nil {
			return nil, err
		}
	}
	panes := []backend.Pane{}
	for _, row := range snap.Panes {
		if target != "" && !scope.contains(row) {
			continue
		}
		ws, _ := snap.workspaceByID(row.WorkspaceID)
		pane := backend.Pane{
			ID:          row.PaneID,
			SessionName: displayName(ws),
			SessionID:   row.WorkspaceID,
			WindowIndex: tabIndex(row.TabID),
			Dead:        false,
			CreatedAt:   createdAt(row.TerminalID),
			// Live, not static: herdr reports the foreground process's own
			// directory, so this follows a cd (§3.4).
			CurrentPath: row.ForegroundCWD,
			Liveness:    backend.LivenessPresent,
			Title:       row.Label,
		}
		if tab, ok := snap.tabByID(row.TabID); ok {
			pane.WindowName = tab.Label
		}
		if pane.CurrentPath == "" {
			pane.CurrentPath = row.CWD
		}
		if target != "" {
			// Only for a TARGETED listing. The live foreground process is a
			// second request per row, and a whole-server listing is the
			// cheapest read there is — paying a subprocess per pane there
			// would put the cost of this field on every caller who asked
			// what exists. Disclosed at every door (§3.4).
			pane.CurrentCommand = h.foregroundCommand(ctx, row.PaneID)
		}
		panes = append(panes, pane)
	}
	return panes, nil
}

// tabIndex reads the window index out of a tab id, which herdr spells
// "w<workspace>:t<tab>" with the tab as a public number: the tenth tab is
// "tA", and a decimal parse there answered 0 (§3.4).
func tabIndex(tabID string) int {
	_, tab, found := strings.Cut(tabID, ":t")
	if !found {
		return 0
	}
	n, ok := backend.PublicNumber(tab)
	if !ok {
		return 0
	}
	return n
}

// createdAt reads a pane's birth time out of its terminal id.
//
// herdr exposes no creation timestamp anywhere in its API, and the terminal id
// carries one: it is allocated as the microseconds since the epoch in hex,
// followed by a counter (src/terminal/id.rs:15-22). Thirteen hex digits hold
// that many microseconds from 2001 until well past 2300, so the split is stable
// for every id this backend will meet.
//
// Deriving it is a deliberate trade against shelling out to `ps` for the shell's
// process start time once per listed pane. If herdr ever changes the shape, this
// returns an implausible epoch rather than a wrong one, and the conformance
// suite's §3.4 case fails loudly instead of the column quietly going zero.
func createdAt(terminalID string) int64 {
	const (
		prefix = "term_"
		digits = 13
	)
	if !strings.HasPrefix(terminalID, prefix) {
		return 0
	}
	body := terminalID[len(prefix):]
	if len(body) < digits {
		return 0
	}
	micros, err := strconv.ParseInt(body[:digits], 16, 64)
	if err != nil {
		return 0
	}
	return micros / 1_000_000
}

// foregroundCommand reports the binary running in a pane right now.
//
// Best-effort: a pane that vanished between the listing and this call has no
// command to report, and failing the whole listing for it would turn an
// ordinary race into an error.
func (h *Herdr) foregroundCommand(ctx context.Context, paneID string) string {
	out, err := h.run(ctx, "pane", "process-info", "--pane", paneID)
	if err != nil {
		return ""
	}
	var info struct {
		Result struct {
			ProcessInfo struct {
				ForegroundProcesses []struct {
					Name string `json:"name"`
				} `json:"foreground_processes"`
			} `json:"process_info"`
		} `json:"result"`
	}
	if err := json.Unmarshal([]byte(out), &info); err != nil {
		return ""
	}
	processes := info.Result.ProcessInfo.ForegroundProcesses
	if len(processes) == 0 {
		return ""
	}
	return processes[len(processes)-1].Name
}

// Probe answers presence, never a transport error (§3.5). A target is present
// when the workspace, tab or pane it names exists (§3.6).
func (h *Herdr) Probe(ctx context.Context, target string) backend.State {
	snap, err := h.snapshot(ctx)
	if err != nil {
		// A server hiccup must never read as absent: a caller polling across a
		// flaky backend needs "definitely gone" and "could not ask" to be
		// different answers.
		return backend.StateError
	}
	if _, err := snap.resolve(target); err != nil {
		// A name that never existed is absent even with no server running:
		// the snapshot collapses that into an empty hierarchy, and the error
		// arm is reserved for a genuinely unreachable backend.
		return backend.StateAbsent
	}
	return backend.StatePresent
}

// resolve turns a target into the rows every herdr verb addresses, against the
// server as it is right now (§3.6).
func (h *Herdr) resolve(ctx context.Context, target string) (resolved, error) {
	snap, err := h.snapshot(ctx)
	if err != nil {
		return resolved{}, err
	}
	return snap.resolve(target)
}

// resolvePane turns a target into the ONE pane a verb acts on: the pane itself
// for a pane target, the tab's focused pane for a tab, and the focused pane of
// the active tab for a workspace (§3.6). Every verb that drives or reads a
// terminal goes through this, so a workspace and its focused pane are the same
// target to all of them.
func (h *Herdr) resolvePane(ctx context.Context, target string) (paneRow, error) {
	r, err := h.resolve(ctx, target)
	if err != nil {
		return paneRow{}, err
	}
	if r.pane.PaneID == "" {
		// The level exists and shows nothing — a workspace or tab the
		// snapshot caught mid-teardown, with its layout gone before its row.
		// Not-found rather than unexpected: by the time the caller reads
		// this, the target is on its way out (§3.3).
		return paneRow{}, backend.Errorf(backend.CodeSessionNotFound,
			"%s %s has no pane to act on", r.kind, target)
	}
	return r.pane, nil
}

// Kill ends whatever the target names: a workspace with every tab and pane in
// it, a tab with every pane in it, or one pane (§3.6).
//
// The three are the same close at three levels rather than one close of the
// resolved pane, because closing the focused pane of a workspace that has two
// would leave the workspace standing with the other — a session that was told
// to stop and did not. Closing the only pane of a workspace closes the tab and
// the workspace with it, so a pane-addressed stop of a single-pane session
// still leaves nothing behind. Measured.
func (h *Herdr) Kill(ctx context.Context, target string) error {
	r, err := h.resolve(ctx, target)
	if err != nil {
		if backend.CodeOf(err) == backend.CodeSessionNotFound {
			// Already the desired state.
			return nil
		}
		return err
	}
	switch r.kind {
	case kindPane:
		_, err = h.run(ctx, "pane", "close", r.pane.PaneID)
	case kindTab:
		_, err = h.run(ctx, "tab", "close", r.tab.TabID)
	default:
		_, err = h.run(ctx, "workspace", "close", r.workspace.WorkspaceID)
	}
	if err != nil && backend.CodeOf(err) == backend.CodeSessionNotFound {
		return nil
	}
	return err
}

// Rename gives the level a target names a new label: a workspace, a tab or a
// pane, each of which herdr labels independently (behavior §2.11). The new
// label is held to the same rule as a created session's name, since a
// workspace label spelled like an id would shadow the id (§10).
func (h *Herdr) Rename(ctx context.Context, target, name string) error {
	if err := validateName(name); err != nil {
		return err
	}
	r, err := h.resolve(ctx, target)
	if err != nil {
		return err
	}
	switch r.kind {
	case kindPane:
		_, err = h.run(ctx, "pane", "rename", r.pane.PaneID, name)
	case kindTab:
		_, err = h.run(ctx, "tab", "rename", r.tab.TabID, name)
	default:
		_, err = h.run(ctx, "workspace", "rename", r.workspace.WorkspaceID, name)
	}
	return err
}

// command builds a herdr invocation with the isolation and hygiene rules
// applied.
func (h *Herdr) command(ctx context.Context, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, "herdr", args...)
	cmd.Env = h.env(invocationEnv())
	cmd.WaitDelay = waitDelay
	return cmd
}

// env points an invocation at this backend's own server, configuration and
// state.
//
// The configuration and state directories travel with the socket because herdr
// keeps a session's persisted layout in the CONFIGURATION directory rather than
// beside its socket, so a server started here would otherwise overwrite the
// operator's saved workspaces (§2.9).
func (h *Herdr) env(base []string) []string {
	if h.socketOnly {
		// A named session's socket lives inside the operator's configuration
		// tree, so deriving a state home from it would create Olympus's
		// directories there. The socket alone is the address (§13.2).
		return h.socketEnv(base)
	}
	home := h.StateHome()
	return append(h.socketEnv(base),
		"XDG_CONFIG_HOME="+filepath.Join(home, "config"),
		"XDG_STATE_HOME="+filepath.Join(home, "state"),
	)
}

// socketEnv points an invocation at this backend's server and leaves the
// configuration directory alone.
//
// Used where the ambient configuration is the right one to read: an attach onto
// a server this handle did not start belongs to whoever runs that server, and
// the client reads real settings out of that directory (see attachEnv).
func (h *Herdr) socketEnv(base []string) []string {
	return append(base, "HERDR_SOCKET_PATH="+h.socketPath)
}

func (h *Herdr) run(ctx context.Context, args ...string) (string, error) {
	if err := h.validateSocketPath(); err != nil {
		return "", err
	}
	return runCommand(h.command(ctx, args...), args)
}

// runCommand runs a prepared herdr invocation and maps its failure into the
// error vocabulary.
func runCommand(cmd *exec.Cmd, args []string) (string, error) {
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return stdout.String(), classify(err, stdout.String(), stderr.String(), args)
	}
	return stdout.String(), nil
}

// errNoServer marks the one condition callers branch on rather than surface.
//
// "No server running" is not an error for every question: a listing collapses
// it into an empty list and a probe into absent (§12.3). A target-addressed
// operation turns it into not-found naming its own target instead, which is why
// it travels as a distinguishable sentinel rather than as one fixed code.
var errNoServer = errors.New("no herdr server is running")

// classify maps herdr's own error vocabulary onto the shared one (§12).
//
// The CLI answers with a JSON envelope carrying a stable code, so this reads
// that rather than matching prose — herdr spells the same condition differently
// depending on which verb produced it, and a message match would classify every
// untested verb as unexpected.
func classify(err error, stdout, stderr string, args []string) error {
	var execErr *exec.Error
	if errors.As(err, &execErr) {
		return backend.Wrapf(backend.CodeBackendUnavailable, err, "herdr is not available")
	}

	code, message := envelopeError(stdout, stderr)
	if message == "" {
		message = strings.TrimSpace(stderr + stdout)
	}
	if message == "" {
		message = err.Error()
	}

	switch code {
	case "server_not_running":
		return fmt.Errorf("%w: %s", errNoServer, message)
	case "pane_not_found", "workspace_not_found", "tab_not_found":
		return backend.Errorf(backend.CodeSessionNotFound, "%s", message)
	case "session_stop_failed":
		// "session <name> is not running or cannot be reached at <socket>":
		// the named server is not there to stop (§13.2).
		return backend.Errorf(backend.CodeSessionNotFound, "%s", message)
	case "invalid_key", "invalid_request", "invalid_metadata_source",
		"invalid_metadata_token", "invalid_metadata_ttl":
		return backend.Errorf(backend.CodeUsage, "%s", message)
	default:
		return backend.Wrapf(backend.CodeUnexpected, errors.New(message), "herdr %s", strings.Join(args, " "))
	}
}

// envelopeError reads the code and message out of herdr's error envelope,
// wherever it was printed.
//
// Both streams are searched because the CLI is not consistent about which one
// carries it: a pane read prints its envelope to stderr while a pane listing
// prints its own to stdout. Reading only one classifies half the verbs as
// unexpected.
func envelopeError(streams ...string) (code, message string) {
	for _, stream := range streams {
		for _, line := range strings.Split(stream, "\n") {
			line = strings.TrimSpace(line)
			if !strings.HasPrefix(line, "{") {
				continue
			}
			var envelope struct {
				Error *struct {
					Code    string `json:"code"`
					Message string `json:"message"`
				} `json:"error"`
			}
			if err := json.Unmarshal([]byte(line), &envelope); err != nil || envelope.Error == nil {
				continue
			}
			return envelope.Error.Code, envelope.Error.Message
		}
	}
	return "", ""
}

// PaneIDEnv is the variable herdr sets in every pane it launches, naming that
// pane (src/pane.rs:145-152). It is how a process inside a session can learn
// where it is.
const PaneIDEnv = "HERDR_PANE_ID"

// socketPathEnv is herdr's own socket override, read by the herdr binary
// whether or not Olympus passes it.
const socketPathEnv = "HERDR_SOCKET_PATH"

// AmbientSocketPath is the API socket a herdr process in THIS environment would
// address, which is what a process inside a pane needs in order to name its own
// session from outside.
//
// It mirrors herdr's own resolution rather than guessing: the override first
// (src/session.rs:173-181), then the configuration directory's `herdr.sock`
// (src/session.rs:161-171, src/config/io.rs:29-33). A server Olympus started
// puts the override in its own environment, and a pane inherits it — so inside
// an Olympus session this returns Olympus's socket, and inside the operator's
// own herdr it returns theirs.
//
// Deliberately NOT this backend's configured socket: a handle pointed at one
// server cannot change which one this process is sitting in.
func AmbientSocketPath() string {
	if v := os.Getenv(socketPathEnv); v != "" {
		return v
	}
	if v := os.Getenv("XDG_CONFIG_HOME"); v != "" {
		return filepath.Join(v, "herdr", "herdr.sock")
	}
	if home := os.Getenv("HOME"); home != "" {
		return filepath.Join(home, ".config", "herdr", "herdr.sock")
	}
	return ""
}

// SessionOf reports the session owning a pane id: the name of the workspace
// the pane belongs to (§3.6).
//
// herdr publishes a pane's ID to the pane but not its workspace's label, so a
// process that wants to name its own session has to ask the server — of the
// socket it is actually inside, which is what AmbientSocketPath answers. The
// answer is the same name a listing gives the workspace, so one nothing has
// labelled is named by its id here too rather than by nothing.
func (h *Herdr) SessionOf(ctx context.Context, paneID string) (string, error) {
	r, err := h.resolve(ctx, paneID)
	if err != nil {
		return "", err
	}
	return displayName(r.workspace), nil
}
