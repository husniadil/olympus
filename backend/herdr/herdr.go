// Package herdr drives herdr.
//
// The rules it implements are specified in docs/terminal-behavior.md, and it is
// proved against backend/backendtest. Where a comment here names a section, the
// obvious implementation is the wrong one and the section says why.
//
// One structural difference shapes most of this file. herdr's server owns
// workspaces, tabs and panes rather than sessions, and a pane's process is the
// shell its own configuration names — there is no per-pane argv anywhere in its
// request vocabulary. So an Olympus session is a NAMED PANE: creation makes a
// workspace and labels its root pane, listing reports the panes that carry a
// label, and a command at creation is refused rather than typed (§2.3.1).
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
type Herdr struct {
	socketPath string
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
	// panes the same. One corrected argument fixes it, which §12 makes the
	// definition of a usage error.
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
			RootPane paneRow `json:"root_pane"`
		} `json:"result"`
	}
	if err := json.Unmarshal([]byte(out), &created); err != nil {
		return backend.Session{}, backend.Wrapf(backend.CodeUnexpected, err, "reading the created workspace")
	}
	pane := created.Result.RootPane
	if pane.PaneID == "" {
		return backend.Session{}, backend.Errorf(backend.CodeUnexpected,
			"herdr created a workspace that reports no root pane")
	}

	// The label is the identity, so a failure here leaves a pane nobody can
	// address. Reap it rather than leaking an unreachable session.
	if _, err := h.run(ctx, "pane", "rename", pane.PaneID, spec.Name); err != nil {
		_, _ = h.run(ctx, "pane", "close", pane.PaneID)
		return backend.Session{}, err
	}

	return backend.Session{
		Name:     spec.Name,
		ID:       pane.PaneID,
		Attached: false,
		Dead:     false,
		Liveness: backend.LivenessPresent,
		CWD:      pane.CWD,
		Outcome:  backend.OutcomeCreated,
	}, nil
}

// validateName rejects a name that could not be told apart from a pane id.
//
// Resolution reads a target shaped like "w1:p2" as a pane (§10), so a session
// answering to that spelling would be shadowed by every pane listing. It is
// USAGE because one corrected argument fixes it.
func validateName(name string) error {
	if name == "" {
		return backend.Errorf(backend.CodeUsage, "a session needs a name")
	}
	if backend.IndexedPaneID(name) {
		return backend.Errorf(backend.CodeUsage,
			"session name %q is spelled like a herdr pane id, which addresses a pane rather than a session", name)
	}
	return nil
}

// A paneRow is the part of herdr's pane shape Olympus reads.
type paneRow struct {
	PaneID     string `json:"pane_id"`
	TerminalID string `json:"terminal_id"`
	TabID      string `json:"tab_id"`
	Label      string `json:"label"`
	CWD        string `json:"cwd"`
	// ForegroundCWD tracks the foreground process rather than the pane's
	// spawn directory, which is what makes current_path live here (§3.4).
	ForegroundCWD string            `json:"foreground_cwd"`
	Tokens        map[string]string `json:"tokens"`
	Scroll        *struct {
		OffsetFromBottom int `json:"offset_from_bottom"`
	} `json:"scroll"`
}

func (h *Herdr) Sessions(ctx context.Context) ([]backend.Session, error) {
	rows, err := h.paneRows(ctx)
	if err != nil {
		return nil, err
	}
	sessions := make([]backend.Session, 0, len(rows))
	for _, row := range rows {
		sessions = append(sessions, backend.Session{
			Name: row.Label,
			// The pane id is the server's own identity for the terminal and
			// survives a rename, so it is a real id distinct from the name.
			ID: row.PaneID,
			// herdr's socket API reports no per-terminal client count, so this
			// is always false rather than sometimes wrong. Disclosed at every
			// door (§3.4).
			Attached: false,
			Dead:     false,
			// A pane whose process exits is removed from the listing, so every
			// listed row is one the server vouches for. There is no err field
			// to classify and no window in which a row is indeterminate (§3.2).
			Liveness: backend.LivenessPresent,
			CWD:      row.CWD,
		})
	}
	return sessions, nil
}

// paneRows lists the panes that are Olympus sessions.
//
// A pane WITHOUT a label is not one. herdr's server opens a workspace of its
// own the moment it starts, and panes a human splits off carry no label either;
// reporting those as sessions would hand a caller rows it never created and
// make an empty listing impossible (§3.3).
func (h *Herdr) paneRows(ctx context.Context) ([]paneRow, error) {
	out, err := h.run(ctx, "pane", "list")
	if err != nil {
		if errors.Is(err, errNoServer) {
			// There is nothing to find; nothing went wrong asking (§3.3).
			return nil, nil
		}
		return nil, err
	}
	var listed struct {
		Result struct {
			Panes []paneRow `json:"panes"`
		} `json:"result"`
	}
	if err := json.Unmarshal([]byte(out), &listed); err != nil {
		return nil, backend.Wrapf(backend.CodeUnexpected, err, "reading the pane listing")
	}
	rows := make([]paneRow, 0, len(listed.Result.Panes))
	for _, row := range listed.Result.Panes {
		if row.Label == "" {
			continue
		}
		rows = append(rows, row)
	}
	return rows, nil
}

func (h *Herdr) Panes(ctx context.Context, target string) ([]backend.Pane, error) {
	rows, err := h.paneRows(ctx)
	if err != nil {
		return nil, err
	}
	panes := []backend.Pane{}
	for _, row := range rows {
		if target != "" && row.Label != target && row.PaneID != target {
			continue
		}
		pane := backend.Pane{
			ID:          row.PaneID,
			SessionName: row.Label,
			SessionID:   row.PaneID,
			WindowIndex: tabIndex(row.TabID),
			Dead:        false,
			CreatedAt:   createdAt(row.TerminalID),
			// Live, not static: herdr reports the foreground process's own
			// directory, so this follows a cd (§3.4).
			CurrentPath: row.ForegroundCWD,
			Liveness:    backend.LivenessPresent,
		}
		if pane.CurrentPath == "" {
			pane.CurrentPath = row.CWD
		}
		if target != "" {
			// Only for a TARGETED listing. The live foreground process is a
			// second request per row, and a whole-server listing is what
			// target resolution reads before every pane-id-addressed
			// operation (§10) — paying a subprocess per pane there would put
			// the cost of this field on the cheapest read there is. Disclosed
			// at every door (§3.4).
			pane.CurrentCommand = h.foregroundCommand(ctx, row.PaneID)
		}
		panes = append(panes, pane)
	}
	if target != "" && len(panes) == 0 {
		return nil, backend.Errorf(backend.CodeSessionNotFound, "no session %s", target)
	}
	return panes, nil
}

// tabIndex reads the window index out of a tab id, which herdr spells
// "w<workspace>:t<tab>".
func tabIndex(tabID string) int {
	_, tab, found := strings.Cut(tabID, ":t")
	if !found {
		return 0
	}
	n, err := strconv.Atoi(tab)
	if err != nil {
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

// Probe answers presence, never a transport error (§3.5).
func (h *Herdr) Probe(ctx context.Context, target string) backend.State {
	rows, err := h.paneRows(ctx)
	if err != nil {
		// A server hiccup must never read as absent: a caller polling across a
		// flaky backend needs "definitely gone" and "could not ask" to be
		// different answers.
		return backend.StateError
	}
	for _, row := range rows {
		if row.Label == target || row.PaneID == target {
			return backend.StatePresent
		}
	}
	// A name that never existed is absent even with no server running: the
	// listing collapses that into an empty list, and the error arm is reserved
	// for a genuinely unreachable backend.
	return backend.StateAbsent
}

// resolvePane turns a session name into the pane id every herdr verb addresses.
func (h *Herdr) resolvePane(ctx context.Context, target string) (paneRow, error) {
	rows, err := h.paneRows(ctx)
	if err != nil {
		return paneRow{}, err
	}
	for _, row := range rows {
		if row.Label == target || row.PaneID == target {
			return row, nil
		}
	}
	return paneRow{}, backend.Errorf(backend.CodeSessionNotFound, "no session %s", target)
}

func (h *Herdr) Kill(ctx context.Context, target string) error {
	row, err := h.resolvePane(ctx, target)
	if err != nil {
		if backend.CodeOf(err) == backend.CodeSessionNotFound {
			// Already the desired state.
			return nil
		}
		return err
	}
	// Closing the only pane of a workspace closes the tab and the workspace
	// with it, so a session Olympus made leaves nothing behind. Measured.
	_, err = h.run(ctx, "pane", "close", row.PaneID)
	if err != nil && backend.CodeOf(err) == backend.CodeSessionNotFound {
		return nil
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

// env points every invocation at this backend's own server, configuration and
// state.
func (h *Herdr) env(base []string) []string {
	home := h.StateHome()
	return append(base,
		"HERDR_SOCKET_PATH="+h.socketPath,
		"XDG_CONFIG_HOME="+filepath.Join(home, "config"),
		"XDG_STATE_HOME="+filepath.Join(home, "state"),
	)
}

func (h *Herdr) run(ctx context.Context, args ...string) (string, error) {
	if err := h.validateSocketPath(); err != nil {
		return "", err
	}
	cmd := h.command(ctx, args...)
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

// SessionOf reports the session name owning a pane id.
//
// herdr publishes a pane's ID to the pane but not its label, so a process that
// wants to name its own session has to ask the server — of the socket it is
// actually inside, which is what AmbientSocketPath answers.
func (h *Herdr) SessionOf(ctx context.Context, paneID string) (string, error) {
	row, err := h.resolvePane(ctx, paneID)
	if err != nil {
		return "", err
	}
	return row.Label, nil
}
