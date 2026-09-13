package herdr

import (
	"context"
	"io"
	"os"
	"os/exec"
	"sort"
	"time"

	"github.com/husniadil/olympus/backend"
)

// bareSessionConfig strips a herdr session client down to a plain pane: every
// keybinding that mutates layout is unbound, every piece of chrome (sidebar,
// tab bar, pane borders and scrollbars, agent labels, window title, mobile
// header) is hidden, and copy-on-select is left on so a selection still copies.
// Two keys are BOUND, to F17 and F18 (keys a terminal almost never sends):
// the previous- and next-workspace steps, which is how the attach walks the
// client onto its workspace on a herdr whose clients keep their own view
// (attachSessionClient says why).
//
// Two values are load-bearing and NOT free choices: prefix cannot be the empty
// string — herdr rejects an empty keybinding and falls back to the default — so
// it is parked on F19 (a key a terminal almost never sends) rather than
// disabled; and mobile_width_threshold must be 0, or a narrow pane paints the
// mobile header this config exists to remove. It is validated with
// `herdr config check` (config: ok).
const bareSessionConfig = `onboarding = false
[keys]
prefix = "f19"
split_vertical = ""
split_horizontal = ""
new_tab = ""
close_tab = ""
close_pane = ""
close_workspace = ""
new_workspace = ""
new_worktree = ""
resize_mode = ""
zoom = ""
toggle_sidebar = ""
workspace_picker = ""
next_workspace = "f18"
previous_workspace = "f17"
[ui]
sidebar_start_collapsed = true
sidebar_collapsed_mode = "hidden"
hide_tab_bar_when_single_tab = true
mobile_width_threshold = 0
pane_borders = false
pane_outer_borders = false
pane_scrollbars = false
show_agent_labels_on_pane_borders = false
copy_on_select = true
window_title = ""
`

// Attach prepares an attach client for the engine to run inside a PTY.
//
// Two clients, chosen by the spec (§8.10). The default is the raw per-pane
// stream, `herdr terminal attach`, onto the pane the target resolves to — the
// pane itself, or the pane a workspace or tab is showing (§3.6). A
// session-client attach is herdr's own client, with its sidebar, tabs,
// selection and scrollback, steered onto the target first.
//
// The presence gate is here so an attach onto nothing fails as not-found rather
// than as whatever the client happens to print — and here it is a courtesy
// rather than the load-bearing guard it is on zmx: herdr's attach does not
// upsert a missing terminal, it refuses one (§8.1).
//
// Supersession is the SERVER'S, not Olympus's. herdr allows one attached client
// per terminal and refuses a second unless it asks to take over, so there is no
// pidfile guard to keep and no sweep to run: the mechanism §8.5 builds for zmx
// is already inside the backend. Measured: a second attach without takeover is
// refused with `terminal <id> already has an attached client; retry with
// --takeover`, and with it the prior client is detached cleanly and told
// `terminal attach taken over`.
//
// One consequence is worth stating rather than hiding. Because the refusal is
// the server's, a non-superseding attach onto an occupied terminal fails INSIDE
// the client, after the PTY is running, rather than as a conflict Olympus
// raises before spawning one. herdr's socket API reports no per-terminal client
// count, so there is nothing to check beforehand.
func (h *Herdr) Attach(ctx context.Context, target string, spec backend.AttachSpec) (backend.Attachment, error) {
	if spec.Role == backend.RoleViewer {
		// herdr's read-only stream is not a terminal client: it emits JSON
		// frames for a program to decode, not a rendering for a human to sit
		// in, so there is nothing to hand a PTY. Dropping input silently
		// instead would be worse than saying so — a watcher who believes they
		// cannot type, and can, will eventually type into somebody else's
		// session (§8.7).
		return backend.Attachment{}, backend.Errorf(backend.CodeUnsupported,
			"herdr has no read-only terminal client, so a viewer attach cannot be made passive")
	}

	if spec.SessionClient {
		return h.attachSessionClient(ctx, target, spec)
	}

	row, err := h.resolvePane(ctx, target)
	if err != nil {
		return backend.Attachment{}, err
	}
	if row.TerminalID == "" {
		return backend.Attachment{}, backend.Errorf(backend.CodeUnexpected,
			"session %s reports no terminal to attach to", target)
	}

	// The terminal id rather than the pane id: attaching addresses the
	// server-owned terminal, which is the thing a client streams, and it is
	// what the pane row already carries.
	args := []string{"terminal", "attach", row.TerminalID}
	if spec.Supersede {
		args = append(args, "--takeover")
	}
	cmd := exec.CommandContext(ctx, "herdr", args...)
	cmd.Env = h.clientEnv()
	return backend.Attachment{Cmd: cmd}, nil
}

// attachSessionClient runs herdr's own session client — which unlike the raw
// terminal stream carries the sidebar, the tabs, selection, scrollback and copy
// — steered onto the target (§8.10).
//
// The steering is a sequence of server requests: the client takes no target
// of its own. A workspace is focused; a tab is focused within its focused
// workspace; a pane is zoomed within its focused tab, which also moves focus
// onto it (measured: zooming a pane that was not focused answers
// `focus_changed: true`, and zooming into a tab already zoomed on another pane
// still moves focus). The steering is not undone when the client exits — the
// server keeps the focus and the zoom a human would have left the same way.
//
// HOW the client reaches a workspace depends on the herdr. Below 0.9.0 every
// client shows the server's one focus, so the steering runs here, before the
// spawn, and the client comes up showing it. From 0.9.0 each client keeps a
// view of its own once it has moved on its own, and a `workspace focus` on
// the server still moves EVERY client (measured 2026-09-13 with two clients
// and a marker typed into each: after `focus three` both typed into
// `three`); so a bare client, whose configuration is this backend's, is
// walked to its workspace with the client's own next- and previous-workspace
// keys once it is up, as the attachment's Settle (§8.10). A client with the
// operator's configuration (`--client` without `--bare`) has no keys this
// backend can count on, so it is steered on the server as before, and moves
// every other client with it.
//
// Which client is spawned depends on how the server was selected. A server
// selected BY NAME is one of herdr's named sessions, and its client is
// `herdr session attach <name>`: that client resolves the session under the
// operator's configuration directory and needs the name, not the socket. A
// server selected by PATH — Olympus's own default, or a `--socket-path` onto
// somebody's headless server — has no name to attach; there plain `herdr`
// with the socket override is the client, measured to attach the server on
// that socket rather than the operator's default.
//
// herdr's session client takes no --viewer and no --takeover (verified against
// the binary), so there is no read-only or supersession control to pass. A
// viewer attach is already refused above; an explicit opt-out of supersession
// is reported as unhonored rather than silently dropped.
func (h *Herdr) attachSessionClient(ctx context.Context, target string, spec backend.AttachSpec) (backend.Attachment, error) {
	r, err := h.resolve(ctx, target)
	if err != nil {
		return backend.Attachment{}, err
	}
	version, err := h.Version(ctx)
	if err != nil {
		return backend.Attachment{}, err
	}
	walk := spec.Bare && !sharedClientFocus(version)
	if !walk {
		if err := h.steer(ctx, r); err != nil {
			return backend.Attachment{}, err
		}
	}

	var cmd *exec.Cmd
	env := h.clientEnv()
	if h.serverName != "" {
		cmd = exec.CommandContext(ctx, "herdr", "session", "attach", h.serverName)
		// The named session resolves under the operator's real configuration
		// directory, which attachEnv already reads; the socket override would
		// only say the same thing a second way.
		env = attachEnv()
	} else {
		cmd = exec.CommandContext(ctx, "herdr")
	}

	// The client is attached to the whole session, so it does not end when
	// the target it was steered onto does: herdr closes the pane, the
	// workspace goes with it, and the client is moved to whatever the server
	// focuses next. The engine polls this and ends the attach on absent
	// (§8.10). Probed by resolved id rather than by the target as given, so
	// a label reused by a later workspace does not read as the same one.
	id := r.id()
	att := backend.Attachment{Cmd: cmd, Probe: func(ctx context.Context) backend.State { return h.Probe(ctx, id) }}
	if walk {
		att.Settle = func(ctx context.Context, keys io.Writer) error { return h.walk(ctx, r, keys) }
	}
	if spec.Bare {
		// A stripped config that hides the client's chrome. HERDR_CONFIG_PATH
		// overrides the config FILE without changing the config directory the
		// session resolves against (verified: the session still resolves), so
		// the client renders as a plain pane while still attaching the same
		// server. The temp file is reaped when the attach ends.
		path, err := writeBareConfig()
		if err != nil {
			return backend.Attachment{}, err
		}
		env = append(env, "HERDR_CONFIG_PATH="+path)
		att.Cleanup = func() error { return os.Remove(path) }
	}
	if !spec.Supersede {
		att.Notices = append(att.Notices,
			"herdr's session client has no co-attach control, so --keep-others cannot be honored here")
	}
	cmd.Env = env
	return att, nil
}

// Focus steers the server onto a target without attaching anything: the
// same steering a session-client attach performs below herdr 0.9.0 (§8.10),
// for a caller whose client is already attached. Every session client on
// the server shows the server's one focus below 0.9.0, so a caller holding
// two clients onto two targets re-steers whenever it brings one of them to
// the front; from 0.9.0 it moves every client too, those that had moved on
// their own included (measured).
func (h *Herdr) Focus(ctx context.Context, target string) error {
	r, err := h.resolve(ctx, target)
	if err != nil {
		return err
	}
	return h.steer(ctx, r)
}

func (h *Herdr) steer(ctx context.Context, r resolved) error {
	for _, args := range steeringArgs(r) {
		if _, err := h.run(ctx, args...); err != nil {
			return err
		}
	}
	return nil
}

// walk brings a bare client onto its target with the client's own keys: the
// workspace by stepping from the one it came up on (the server's focus, which
// a client shows on connecting — measured) to the target, the shorter way
// round the ring the client's next- and previous-workspace keys walk; then
// the tab and the pane, where the target names one, on the server, since the
// active tab and the zoom are the workspace's own state and a client on
// another workspace is not moved by them.
//
// The keys are F17 and F18 in the kitty spelling, which is what herdr's
// client reads once it has asked for that protocol (`CSI > 7 u`); the
// legacy `CSI 31 ~` went unread (measured). A beat between presses: two
// written at once were read as one.
func (h *Herdr) walk(ctx context.Context, r resolved, keys io.Writer) error {
	snap, err := h.snapshot(ctx)
	if err != nil {
		return err
	}
	steps := workspaceSteps(snap.Workspaces, snap.FocusedWorkspaceID, r.workspace.WorkspaceID)
	key := nextWorkspaceKey
	if steps < 0 {
		key, steps = previousWorkspaceKey, -steps
	}
	for i := 0; i < steps; i++ {
		if i > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(keyGap):
			}
		}
		if _, err := io.WriteString(keys, key); err != nil {
			return backend.Wrapf(backend.CodeUnexpected, err, "walking the client to %s", r.workspace.WorkspaceID)
		}
	}
	if r.kind == kindWorkspace {
		return nil
	}
	for _, args := range steeringArgs(r)[1:] {
		if _, err := h.run(ctx, args...); err != nil {
			return err
		}
	}
	return nil
}

// workspaceSteps is how many next-workspace presses (positive) or
// previous-workspace presses (negative) take a client from one workspace to
// another, the shorter way round: the keys walk the workspaces in the order
// of their numbers and wrap at either end (measured). Zero where the target
// is where the client already is, or where either is not in the list.
func workspaceSteps(rows []workspaceRow, from, to string) int {
	ordered := append([]workspaceRow(nil), rows...)
	sort.SliceStable(ordered, func(i, j int) bool { return ordered[i].Number < ordered[j].Number })
	at := func(id string) int {
		for i, w := range ordered {
			if w.WorkspaceID == id {
				return i
			}
		}
		return -1
	}
	a, b := at(from), at(to)
	if a < 0 || b < 0 || a == b {
		return 0
	}
	n := len(ordered)
	forward := (b - a + n) % n
	if forward <= n-forward {
		return forward
	}
	return forward - n
}

const (
	nextWorkspaceKey     = "\x1b[57381u" // F18, kitty spelling
	previousWorkspaceKey = "\x1b[57380u" // F17, kitty spelling
	keyGap               = 40 * time.Millisecond
)

// steeringArgs is the sequence of herdr invocations that puts the server's
// focus onto a resolved target, in the order the client will read it: the
// workspace, then the tab within it, then the pane within that (§8.10).
//
// A workspace needs only the first; a tab the first two; a pane all three. The
// pane step is a zoom rather than a bare focus because herdr has no
// pane-focus request of its own, and a zoom both focuses the pane and shows it
// alone, which is what a caller attaching one pane of a split tab means.
//
// The zoom is server state and outlives the attach that set it, so a later
// attach onto the workspace or the tab would show that one pane still — the
// caller asked for the tab, and a tab is its split. Where the tab it lands on
// is zoomed, the workspace and tab steps end by zooming out (measured: the
// second attach onto a two-pane workspace showed one pane, and nothing the
// caller could target brought the split back).
func steeringArgs(r resolved) [][]string {
	steps := [][]string{{"workspace", "focus", r.workspace.WorkspaceID}}
	if r.kind == kindWorkspace {
		return unzoom(steps, r)
	}
	steps = append(steps, []string{"tab", "focus", r.tab.TabID})
	if r.kind == kindTab {
		return unzoom(steps, r)
	}
	return append(steps, []string{"pane", "zoom", "--pane", r.pane.PaneID, "--on"})
}

func unzoom(steps [][]string, r resolved) [][]string {
	if !r.zoomed || r.pane.PaneID == "" {
		return steps
	}
	return append(steps, []string{"pane", "zoom", "--pane", r.pane.PaneID, "--off"})
}

// clientEnv is the environment an interactive client runs with, pointed at
// this backend's server.
//
// A server this handle started is given this backend's own configuration
// directory: it is the one that server was booted against, and there is
// nothing of anybody else's in it. A server this handle did not start belongs
// to whoever runs it, and the attach client is the one invocation whose
// behaviour its configuration decides. Imposing a directory of Olympus's own
// would hand a human their own terminal configured like a fresh install
// (src/client/mod.rs:1225-1234).
func (h *Herdr) clientEnv() []string {
	if h.startedTheServer() {
		return h.env(attachEnv())
	}
	return h.socketEnv(attachEnv())
}

// writeBareConfig lays the stripped config down in a temp file for one attach.
//
// A per-attach temp file rather than a managed path under StateHome: this is
// the operator's ambient config directory the client reads from, which Olympus
// does not own, and the override is a single file that exists only for the life
// of this one client.
func writeBareConfig() (string, error) {
	f, err := os.CreateTemp("", "herdr-bare-*.toml")
	if err != nil {
		return "", backend.Wrapf(backend.CodeUnexpected, err, "creating a stripped herdr config for a bare session attach")
	}
	if _, err := f.WriteString(bareSessionConfig); err != nil {
		_ = f.Close()
		_ = os.Remove(f.Name())
		return "", backend.Wrapf(backend.CodeUnexpected, err, "writing a stripped herdr config")
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(f.Name())
		return "", backend.Wrapf(backend.CodeUnexpected, err, "closing a stripped herdr config")
	}
	return f.Name(), nil
}
