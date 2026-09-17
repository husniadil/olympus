package herdr

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/husniadil/olympus/backend"
)

// bareSessionConfig strips a herdr session client down to a plain pane onto
// ONE workspace: every keybinding that leaves the workspace or changes what
// the session holds is unbound (tabs, workspaces, worktrees, the sidebar, the
// picker), every piece of chrome around the pane (sidebar, tab bar, the
// outer border and scrollbars, agent labels, window title, mobile header)
// is hidden, and copy-on-select is left on so a selection still copies.
//
// What the operator does INSIDE the workspace stays theirs: the prefix is
// the one their own configuration names (§13.3, rawConfiguredPrefix), and
// the pane keys behind it — split, close pane, zoom, resize, focus between
// panes — keep herdr's own bindings, so a bare pane splits the way the
// operator's own client does. It was parked on F19 with every pane key
// unbound for a while, on the reading that a bare pane holds nothing to
// split; the operator split one from herdr's own client and asked why the
// bare one could not. `pane_borders = true` draws a divider between
// split panes and nothing around a lone one: it is 0.9.0's "auto" in the
// legacy spelling, which the herdr the release gate installs (0.8.2)
// still parses, where the word did not (CI, 2026-09-13).
//
// Two keys are BOUND, to F17 and F18 (keys a terminal almost never sends):
// the previous- and next-workspace steps, which is how the attach walks the
// client onto its workspace on a herdr whose clients keep their own view
// (attachSessionClient says why).
//
// One value is load-bearing and NOT a free choice: mobile_width_threshold
// must be 0, or a narrow pane paints the mobile header this config exists
// to remove. It is validated with `herdr config check` (config: ok).
func bareSessionConfig(prefix string) string {
	return `onboarding = false
[keys]
prefix = "` + prefix + `"
new_tab = ""
close_tab = ""
close_workspace = ""
new_workspace = ""
new_worktree = ""
rename_workspace = ""
toggle_sidebar = ""
workspace_picker = ""
next_workspace = "f18"
previous_workspace = "f17"
[ui]
sidebar_start_collapsed = true
sidebar_collapsed_mode = "hidden"
hide_tab_bar_when_single_tab = true
mobile_width_threshold = 0
pane_borders = true
pane_outer_borders = false
pane_scrollbars = false
show_agent_labels_on_pane_borders = false
copy_on_select = true
window_title = ""
`
}

// kittyPush is what herdr's client writes as it comes up to ask for the
// kitty keyboard protocol, in which it then reads the walk's keys: the
// attachment names it as SettleAfter, so the engine walks the client a
// beat after it rather than waiting for the client to go quiet, which a
// client on a streaming workspace never does (measured, §8.10).
const kittyPush = "\x1b[>7u"

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

	// A tag names the client a bare attach launches on a server that moves
	// one client's view, and no other attach launches a tagged client, so
	// one given anywhere else is refused rather than dropped: a caller that
	// then asks where its client is would look for a name nobody carries.
	if spec.ClientTag != "" {
		if err := backend.CheckClientTag(spec.ClientTag); err != nil {
			return backend.Attachment{}, err
		}
		if !spec.Bare {
			return backend.Attachment{}, backend.Errorf(backend.CodeUsage,
				"a client tag names the client a bare attach launches; this attach is not bare")
		}
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
// keys once it is up, as the attachment's Settle (§8.10) — unless the server
// advertises that it moves one client's view, where the client is launched
// onto its workspace and moved by a tag instead (attachClientView). A client with the
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
	// Asked before the target is resolved, so a tag this server can never
	// launch a client with is usage whatever the target is.
	if spec.ClientTag != "" {
		views, err := h.clientViews(ctx)
		if err != nil {
			return backend.Attachment{}, err
		}
		if !views {
			return backend.Attachment{}, backend.Errorf(backend.CodeUsage,
				"a client tag needs a herdr server that advertises client_view_focus; this one launches no client with a tag")
		}
	}
	r, err := h.resolve(ctx, target)
	if err != nil {
		return backend.Attachment{}, err
	}
	// A server that moves one client's view is asked, never inferred from
	// its version: a build with the request and one without report the same
	// number. There the client is launched onto its workspace and moved by
	// its tag, and nothing is walked (attachClientView).
	if spec.Bare {
		views, err := h.clientViews(ctx)
		if err != nil {
			return backend.Attachment{}, err
		}
		if views {
			return h.attachClientView(ctx, r, spec)
		}
	}
	version, err := h.Version(ctx)
	if err != nil {
		return backend.Attachment{}, err
	}
	walk := spec.Bare && !sharedClientFocus(version)
	// The walk's origin and its steps are read HERE, under the lock, and
	// not when the walk runs: the client comes up on the focus at the
	// moment it connects, and the walk of another bare client between now
	// and then would move the focus it was read from (walklock.go).
	var lock *walkLock
	var ring []workspaceRow
	var origin string
	if !walk {
		if err := h.steer(ctx, r); err != nil {
			return backend.Attachment{}, err
		}
	} else {
		var err error
		if lock, err = acquireWalkLock(ctx, h.socketPath); err != nil {
			return backend.Attachment{}, err
		}
		snap, err := h.snapshot(ctx)
		if err != nil {
			lock.release()
			return backend.Attachment{}, err
		}
		ring, origin = snap.Workspaces, snap.FocusedWorkspaceID
	}

	cmd, env := h.sessionClientCommand(ctx)

	// The client is attached to the whole session, so it does not end when
	// the target it was steered onto does: herdr closes the pane, the
	// workspace goes with it, and the client is moved to whatever the server
	// focuses next. The engine polls this and ends the attach on absent
	// (§8.10). Probed by resolved id rather than by the target as given, so
	// a label reused by a later workspace does not read as the same one.
	// A walked client is probed where it IS (bareClient): its target until
	// it is moved, then each target a go took it to. A steered client is
	// probed on its target, which is where the server put it.
	at := &bareClient{at: r}
	att := backend.Attachment{Cmd: cmd, Probe: func(ctx context.Context) backend.State { return h.Probe(ctx, at.id()) }}
	if walk {
		att.Settle = func(ctx context.Context, keys io.Writer, expect backend.Expect) error {
			defer lock.release()
			return h.walk(ctx, ring, origin, r, keys, at, expect)
		}
		att.SettleAfter = []byte(kittyPush)
		att.Go = func(ctx context.Context, target string, keys io.Writer, expect backend.Expect) error {
			lock, err := acquireWalkLock(ctx, h.socketPath)
			if err != nil {
				return err
			}
			defer lock.release()
			snap, err := h.snapshot(ctx)
			if err != nil {
				return err
			}
			to, err := snap.resolve(target)
			if err != nil {
				return err
			}
			return h.walk(ctx, snap.Workspaces, at.workspace(), to, keys, at, expect)
		}
	}
	if spec.Bare {
		// A stripped config that hides the client's chrome. HERDR_CONFIG_PATH
		// overrides the config FILE without changing the config directory the
		// session resolves against (verified: the session still resolves), so
		// the client renders as a plain pane while still attaching the same
		// server. The temp file is reaped when the attach ends.
		path, err := writeBareConfig(rawConfiguredPrefix(filepath.Dir(h.socketPath)))
		if err != nil {
			lock.release()
			return backend.Attachment{}, err
		}
		env = append(env, "HERDR_CONFIG_PATH="+path)
		// A client that ends before it settles never runs the walk, so the
		// lock is dropped here as well.
		att.Cleanup = func() error {
			lock.release()
			return os.Remove(path)
		}
	}
	if !spec.Supersede {
		att.Notices = append(att.Notices, keepOthersNotice)
	}
	cmd.Env = env
	return att, nil
}

const keepOthersNotice = "herdr's session client has no co-attach control, so --keep-others cannot be honored here"

// sessionClientCommand is herdr's own client onto this backend's server, and
// the environment it runs with: `herdr session attach <name>` for a server
// selected by name, plain `herdr` with the socket override for one selected
// by path (§8.10).
func (h *Herdr) sessionClientCommand(ctx context.Context, args ...string) (*exec.Cmd, []string) {
	if h.serverName != "" {
		// The named session resolves under the operator's real configuration
		// directory, which attachEnv already reads; the socket override would
		// only say the same thing a second way.
		return exec.CommandContext(ctx, "herdr", append([]string{"session", "attach", h.serverName}, args...)...), attachEnv()
	}
	return exec.CommandContext(ctx, "herdr", args...), h.clientEnv()
}

// attachClientView runs a bare client on a server that moves one client's
// view (`client_view_focus`, §8.10). The client is launched onto the
// target's workspace with `--workspace`, which moves neither the server's
// focus nor any other client, and named with `--client-tag`: the caller's
// tag where the spec carries one, a tag of its own otherwise. From then on
// it is addressed by that tag and nothing else.
//
// No key is pressed and no title is waited for, and no walk lock is taken:
// nothing here reads the server's focus, so there is nothing for another
// attach to move under it. The zoom steps of the steering table run here,
// before the client exists, so the first state it is sent already has the
// target's pane focused (showOnClient says why that cannot wait). The
// attachment's Settle waits for the client to be listed and puts it on the
// target's tab where the target names one; a go zooms and moves it the same
// way, and the probe reads where `client.list` has it.
func (h *Herdr) attachClientView(ctx context.Context, r resolved, spec backend.AttachSpec) (backend.Attachment, error) {
	tag := spec.ClientTag
	if tag == "" {
		var err error
		if tag, err = newClientTag(); err != nil {
			return backend.Attachment{}, err
		}
	}
	if _, _, err := h.zoomSteps(ctx, r); err != nil {
		return backend.Attachment{}, err
	}
	cmd, env := h.sessionClientCommand(ctx, "--workspace", r.workspace.WorkspaceID, "--client-tag", tag)
	path, err := writeBareConfig(rawConfiguredPrefix(filepath.Dir(h.socketPath)))
	if err != nil {
		return backend.Attachment{}, err
	}
	cmd.Env = append(env, "HERDR_CONFIG_PATH="+path)

	at := &bareClient{at: r}
	att := backend.Attachment{
		Cmd:     cmd,
		Cleanup: func() error { return os.Remove(path) },
		Probe:   func(ctx context.Context) backend.State { return h.probeClientView(ctx, tag, at) },
		// Settle runs once the client is up; it asks the server where the
		// client is rather than watching what it paints, so the kitty push
		// only says when to start asking.
		SettleAfter: []byte(kittyPush),
		Settle: func(ctx context.Context, _ io.Writer, expect backend.Expect) error {
			return h.showOnClient(ctx, tag, r, at, expect)
		},
		Go: func(ctx context.Context, target string, _ io.Writer, expect backend.Expect) error {
			to, err := h.resolve(ctx, target)
			if err != nil {
				return err
			}
			return h.showOnClient(ctx, tag, to, at, expect)
		},
		Focus: func(ctx context.Context, target string, _ io.Writer, expect backend.Expect) error {
			to, err := h.resolve(ctx, target)
			if err != nil {
				return err
			}
			return h.focusOnClient(ctx, tag, to, at, expect)
		},
	}
	if !spec.Supersede {
		att.Notices = append(att.Notices, keepOthersNotice)
	}
	return att, nil
}

// showOnClient puts the tagged client on a target with `client.view.focus`
// and returns once the client is showing it, which is what lets the engine
// forward what was typed after a go (§8.10). A tab or pane target names its
// tab; a workspace target does not, and the client shows the tab it last
// showed there, as a person switching to it would see.
//
// The server's answer says the view moved, and that is not yet enough to
// type into it: the client addresses what it sends to the pane it believes
// it shows, and the server drops input for a pane the client no longer
// views, until the repaint that tells the client where it is has reached it
// (measured: a marker written straight after the answer never echoed). A
// server that reports when a client has applied its view is asked to answer
// once it has, for a client that acknowledges what it applies
// (showOnAckingClient). Elsewhere the end of that repaint's synchronized
// frame is waited for, and a beat after it, the same as after a walk. That
// frame end is the only sign such a server gives, and it cannot be told from
// a late frame the client painted for where it was before (§8.11).
//
// The zoom steps of the steering table run on the server BEFORE the view
// moves: the zoom is the tab's own state, there is no zoom of one client's,
// and a zoom that moves the tab's focus changes which pane the server takes
// the client's input for. Zoomed first, the repaint the view change is
// waited for is built from a state that already has the pane focused. Zoomed
// after, the zoom's repaint needed a wait of its own, and nothing the client
// paints tells that repaint from a late frame of the view change: the wait
// was met by one before the zoom had even answered (measured, 17 of 20 goes
// under load), and a marker typed after the go was dropped whenever the
// zoom's repaint was still on its way (4 of 20).
//
// The client is waited for in `client.list` first, since the first placement
// may run before it has connected, and one already where the target is (as
// a client launched onto its workspace is) is not moved again.
func (h *Herdr) showOnClient(ctx context.Context, tag string, to resolved, at *bareClient, expect backend.Expect) error {
	tab := ""
	if to.kind != kindWorkspace {
		tab = to.tab.TabID
	}
	row, err := h.waitForClient(ctx, tag)
	if err != nil {
		return err
	}
	caps, err := h.capabilities(ctx)
	if err != nil {
		return err
	}
	if caps.viewAck && row.SnapshotAcks {
		return h.showOnAckingClient(ctx, tag, to, row, at)
	}
	moves := row.WorkspaceID != to.workspace.WorkspaceID || (tab != "" && row.TabID != tab)

	// A zoom that moves the focus of the tab the client already shows has
	// no view change to carry it, so its repaint is waited for as the only
	// sign the client has it. That frame cannot be told from an earlier one
	// (§8.11). A zoom that leaves the focus where it was needs no wait: the
	// pane the client addresses is the one the server takes input for.
	var repainted func(time.Duration) bool
	if !moves && row.TabID == to.tab.TabID {
		repainted = expect([]byte(frameEnd))
	}
	refocused, _, err := h.zoomSteps(ctx, to)
	if err != nil {
		return err
	}
	if refocused && repainted != nil {
		if !repainted(pressWithin) {
			return backend.Errorf(backend.CodeUnexpected, "the client did not paint %s", to.id())
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(keyGap):
		}
	}

	if moves {
		params := map[string]string{"client_tag": tag, "workspace_id": to.workspace.WorkspaceID}
		if tab != "" {
			params["tab_id"] = tab
		}
		painted := expect([]byte(frameEnd))
		result, err := h.call(ctx, "client.view.focus", params)
		if err != nil {
			return err
		}
		var answer struct {
			Client clientRow `json:"client"`
		}
		if err := json.Unmarshal(result, &answer); err != nil {
			return backend.Wrapf(backend.CodeUnexpected, err, "reading the herdr answer to client.view.focus")
		}
		if answer.Client.WorkspaceID != to.workspace.WorkspaceID || (tab != "" && answer.Client.TabID != tab) {
			return backend.Errorf(backend.CodeUnexpected, "herdr put the client on %s %s, not %s",
				answer.Client.WorkspaceID, answer.Client.TabID, to.id())
		}
		if !painted(pressWithin) {
			return backend.Errorf(backend.CodeUnexpected, "the client did not paint %s", to.id())
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(keyGap):
		}
	}
	at.set(to)
	return nil
}

// viewAckWait bounds how long the server is asked to wait for a client to
// apply its view: herdr's own default for the wait, well inside its limit.
// A client that has not applied it by then fails the go rather than have
// what was typed forwarded into a pane it may not address (§8.10).
const viewAckWait = 5 * time.Second

// showOnAckingClient is showOnClient on a server that reports when a client
// has applied its view (`client_view_ack`), for a client that acknowledges
// the snapshots it applies (§8.10).
//
// The zoom steps run first, as on the frame path, and then ONE request
// confirms both the zoom and the move: `client.view.focus` with `wait` where
// the client is not yet where the target is, `client.view.wait` where it
// is. Either answers only once the client has acknowledged a snapshot
// carrying its current view — workspace, tab, focused pane and zoom — which
// is the snapshot the client routes what it is sent through. Nothing the
// client paints is watched, so a late frame for where it was cannot end the
// wait early (§8.11). The client the server answers with must show the
// target, or the go fails.
func (h *Herdr) showOnAckingClient(ctx context.Context, tag string, to resolved, row clientRow, at *bareClient) error {
	_, zoomed, err := h.zoomSteps(ctx, to)
	if err != nil {
		return err
	}
	method := "client.view.wait"
	params := map[string]any{"client_tag": tag, "timeout_ms": viewAckWait.Milliseconds()}
	if row.WorkspaceID != to.workspace.WorkspaceID || (to.kind != kindWorkspace && row.TabID != to.tab.TabID) {
		method = "client.view.focus"
		params["workspace_id"] = to.workspace.WorkspaceID
		if to.kind != kindWorkspace {
			params["tab_id"] = to.tab.TabID
		}
		params["wait"] = true
	}
	// The request's own deadline sits past the server's, so a client that
	// never applies the view is answered as herdr's timeout, not as a socket
	// that stopped answering.
	callCtx, cancel := context.WithTimeout(ctx, viewAckWait+apiCallBudget)
	defer cancel()
	result, err := h.call(callCtx, method, params)
	if backend.CodeOf(err) == backend.CodeTimeout {
		return backend.Wrapf(backend.CodeTimeout, err, "the herdr client tagged %s did not apply its view of %s within %s", tag, to.id(), viewAckWait)
	}
	if err != nil {
		return err
	}
	var answer struct {
		Client clientRow `json:"client"`
	}
	if err := json.Unmarshal(result, &answer); err != nil {
		return backend.Wrapf(backend.CodeUnexpected, err, "reading the herdr answer to %s", method)
	}
	if !viewShows(answer.Client, to, zoomed) {
		c := answer.Client
		return backend.Errorf(backend.CodeUnexpected, "herdr reports the client on %s %s pane %s (zoomed %v, applied %v), not %s",
			c.WorkspaceID, c.TabID, c.PaneID, c.Zoomed, c.ViewApplied, to.id())
	}
	at.set(to)
	return nil
}

// focusOnClient focuses a pane for the tagged client as a click on it would:
// the client is put on the pane's tab, the tab is not zoomed, and the pane is
// the tab's focused one (§8.10). It needs a server with `client_view_pane`,
// since the pane is focused by the same `client.view.focus` that moves the
// client: the server's own `pane focus` moves the server's focus, which is
// every other client's too.
//
// A zoom left on the tab is taken off first, on the server, for the reason the
// zoom steps run before a go's move: the zoom is the tab's state, and the view
// the client is waited for must be built from a tab already unzoomed. The
// client is then waited for the way a go waits for it: its acknowledgement
// where it gives one, else the end of the frame the move paints.
//
// Afterwards the client is on the TAB, not the pane: the probe reads that tab,
// so closing the pane leaves the attach on the tab it was on.
func (h *Herdr) focusOnClient(ctx context.Context, tag string, to resolved, at *bareClient, expect backend.Expect) error {
	if to.kind != kindPane || to.pane.PaneID == "" {
		return backend.Errorf(backend.CodeUsage, "focus takes a pane, not %s", to.id())
	}
	caps, err := h.capabilities(ctx)
	if err != nil {
		return err
	}
	if !caps.viewPane {
		return backend.Errorf(backend.CodeUnsupported, "this herdr server does not focus a pane for one client (client_view_pane)")
	}
	row, err := h.waitForClient(ctx, tag)
	if err != nil {
		return err
	}
	if to.zoomed {
		if _, err := h.run(ctx, "pane", "zoom", "--pane", to.pane.PaneID, "--off"); err != nil {
			return err
		}
	}
	acking := caps.viewAck && row.SnapshotAcks
	params := map[string]any{
		"client_tag":   tag,
		"workspace_id": to.workspace.WorkspaceID,
		"tab_id":       to.tab.TabID,
		"pane_id":      to.pane.PaneID,
	}
	var painted func(time.Duration) bool
	if acking {
		params["wait"] = true
		params["timeout_ms"] = viewAckWait.Milliseconds()
	} else {
		painted = expect([]byte(frameEnd))
	}
	callCtx, cancel := context.WithTimeout(ctx, viewAckWait+apiCallBudget)
	defer cancel()
	result, err := h.call(callCtx, "client.view.focus", params)
	if backend.CodeOf(err) == backend.CodeTimeout {
		return backend.Wrapf(backend.CodeTimeout, err, "the herdr client tagged %s did not apply its view of %s within %s", tag, to.id(), viewAckWait)
	}
	if err != nil {
		return err
	}
	var answer struct {
		Client clientRow `json:"client"`
	}
	if err := json.Unmarshal(result, &answer); err != nil {
		return backend.Wrapf(backend.CodeUnexpected, err, "reading the herdr answer to client.view.focus")
	}
	c := answer.Client
	if c.WorkspaceID != to.workspace.WorkspaceID || c.TabID != to.tab.TabID || c.PaneID != to.pane.PaneID || c.Zoomed || (acking && !c.ViewApplied) {
		return backend.Errorf(backend.CodeUnexpected, "herdr reports the client on %s %s pane %s (zoomed %v, applied %v), not %s focused",
			c.WorkspaceID, c.TabID, c.PaneID, c.Zoomed, c.ViewApplied, to.id())
	}
	if painted != nil {
		if !painted(pressWithin) {
			return backend.Errorf(backend.CodeUnexpected, "the client did not paint %s", to.id())
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(keyGap):
		}
	}
	tab := to
	tab.kind = kindTab
	tab.pane = paneRow{}
	tab.zoomed = false
	at.set(tab)
	return nil
}

// viewShows reports whether a client whose view is applied shows a target:
// its workspace; for a tab or pane target, its tab; for a pane target, that
// pane focused, in a tab zoomed as the zoom step left it (`zoomed`). A lone
// pane is not zoomed by it: herdr answers `single_pane` and leaves the tab
// as it was (measured).
func viewShows(c clientRow, to resolved, zoomed bool) bool {
	if !c.ViewApplied || c.WorkspaceID != to.workspace.WorkspaceID {
		return false
	}
	if to.kind == kindWorkspace {
		return true
	}
	if c.TabID != to.tab.TabID {
		return false
	}
	return to.kind != kindPane || (c.PaneID == to.pane.PaneID && c.Zoomed == zoomed)
}

// zoomSteps runs the zoom steps of the steering table for a target on the
// server, and reports whether one moved the focus of the target's tab and
// whether the last left the tab zoomed.
func (h *Herdr) zoomSteps(ctx context.Context, to resolved) (refocused, zoomed bool, err error) {
	for _, args := range steeringArgs(to) {
		if args[0] != "pane" {
			continue
		}
		out, err := h.run(ctx, args...)
		if err != nil {
			return false, false, err
		}
		var zoom struct {
			Result struct {
				Zoom struct {
					FocusChanged bool `json:"focus_changed"`
					Zoomed       bool `json:"zoomed"`
				} `json:"zoom"`
			} `json:"result"`
		}
		if err := json.Unmarshal([]byte(out), &zoom); err != nil {
			return false, false, backend.Wrapf(backend.CodeUnexpected, err, "reading the herdr answer to %s", strings.Join(args, " "))
		}
		refocused = refocused || zoom.Result.Zoom.FocusChanged
		zoomed = zoom.Result.Zoom.Zoomed
	}
	return refocused, zoomed, nil
}

// clientArrival bounds the wait for a launched client to be listed. The
// engine settles a client once it has painted, by which point it has
// connected; the bound is for one that never does.
const clientArrival = 10 * time.Second

// waitForClient waits for the tagged client to appear in `client.list`.
func (h *Herdr) waitForClient(ctx context.Context, tag string) (clientRow, error) {
	deadline := time.Now().Add(clientArrival)
	for {
		row, ok, err := h.taggedClient(ctx, tag)
		if err != nil {
			return clientRow{}, err
		}
		if ok {
			return row, nil
		}
		if time.Now().After(deadline) {
			return clientRow{}, backend.Errorf(backend.CodeUnexpected,
				"the herdr client tagged %s did not connect within %s", tag, clientArrival)
		}
		select {
		case <-ctx.Done():
			return clientRow{}, backend.Wrapf(backend.CodeTimeout, ctx.Err(), "waiting for the herdr client tagged %s", tag)
		case <-time.After(walkLockPoll):
		}
	}
}

// probeClientView answers whether the target the tagged client is on still
// exists, reading where the client is from `client.list` (§8.10).
//
// The client is where this backend last put it unless the server moved it,
// and the server moves a client for two reasons: the workspace it showed
// closed, or somebody else moved it by its id. The first is the end of the
// attach and the second is not, and the target this backend put it on tells
// them apart: gone, the attach ends; still there, the client was moved, and
// the probe follows it to the workspace it is on now.
func (h *Herdr) probeClientView(ctx context.Context, tag string, at *bareClient) backend.State {
	was := at.get()
	row, listed, err := h.taggedClient(ctx, tag)
	if err != nil {
		return backend.StateError
	}
	snap, err := h.snapshot(ctx)
	if err != nil {
		return backend.StateError
	}
	if _, err := snap.resolve(was.id()); err != nil {
		if at.get().id() != was.id() {
			// A go moved the client while this was asked; the next
			// probe asks about where it went.
			return backend.StatePresent
		}
		return backend.StateAbsent
	}
	if listed && row.WorkspaceID != "" && row.WorkspaceID != was.workspace.WorkspaceID {
		if moved, err := snap.resolve(row.WorkspaceID); err == nil {
			at.follow(was, moved)
		}
	}
	return backend.StatePresent
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

// A bareClient is where a bare client IS, as far as this backend has moved
// it: the target it was attached for, each workspace a walk stepped it
// through, and each target a go took it to. The probe reads it from the
// engine's own goroutine, so the attach ends with the target the client is
// ON rather than the one it was made for (§8.10).
type bareClient struct {
	mu sync.Mutex
	at resolved
}

func (c *bareClient) id() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.at.id()
}

func (c *bareClient) workspace() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.at.workspace.WorkspaceID
}

func (c *bareClient) set(r resolved) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.at = r
}

// follow moves where the client is to r, unless a go has moved it since
// `from` was read: the probe runs beside the go, and a row it read before
// the go must not put the client back.
func (c *bareClient) follow(from resolved, r resolved) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.at.id() == from.id() {
		c.at = r
	}
}

func (c *bareClient) get() resolved {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.at
}

// walk brings a bare client from the workspace it is on (`from`) onto a
// target with the client's own keys: the workspace by stepping the shorter
// way round the ring the client's next- and previous-workspace keys walk
// (`ring`, read at the same moment as `from`, under the walk lock); then
// the tab and the pane, where the target names one, on the server, since
// the active tab and the zoom are the workspace's own state and a client on
// another workspace is not moved by them. Where the client is (`at`) moves
// with every press, so a walk cut short leaves it right for the next.
//
// The keys are F17 and F18 in the kitty spelling, which is what herdr's
// client reads once it has asked for that protocol (`CSI > 7 u`); the
// legacy `CSI 31 ~` went unread (measured). Every press is CONFIRMED: the
// client paints its window title as it lands on a workspace (`host:
// label`, measured on its own switches every time), so the title of the
// client paints as a press lands is expected before the press, and a press
// that goes unanswered within a beat is made once more, then given up as
// the walk's error. A beat between presses: two written at once were read
// as one. Under load a press in three went unread with no confirmation
// (measured 2026-09-13), and a marker typed after it landed in the
// workspace the client was still on.
func (h *Herdr) walk(ctx context.Context, ring []workspaceRow, from string, to resolved, keys io.Writer, at *bareClient, expect backend.Expect) error {
	ordered := append([]workspaceRow(nil), ring...)
	sort.SliceStable(ordered, func(i, j int) bool { return ordered[i].Number < ordered[j].Number })
	steps := workspaceSteps(ordered, from, to.workspace.WorkspaceID)
	key, dir := nextWorkspaceKey, 1
	if steps < 0 {
		key, dir, steps = previousWorkspaceKey, -1, -steps
	}
	idx := -1
	for i, w := range ordered {
		if w.WorkspaceID == from {
			idx = i
		}
	}
	// The last press's paint, asked for once its title is seen: the focus is
	// read after that, and the frame is out by the time it has moved.
	var painted func(time.Duration) bool
	for i := 0; i < steps; i++ {
		next := ordered[(idx+dir+len(ordered))%len(ordered)]
		landed := false
		for attempt := 0; attempt < pressAttempts && !landed; attempt++ {
			if i > 0 || attempt > 0 {
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-time.After(keyGap):
				}
			}
			// The server's focus is read before the press, since a press is
			// confirmed by the focus moving onto the next workspace.
			before, err := h.snapshot(ctx)
			if err != nil {
				return err
			}
			seen := expect(titleMark())
			if _, err := io.WriteString(keys, key); err != nil {
				return backend.Wrapf(backend.CodeUnexpected, err, "walking the client to %s", to.workspace.WorkspaceID)
			}
			landed = seen(pressWithin)
			painted = expect([]byte(frameEnd))
			// A title alone is not the press read: herdr 0.9.1's client
			// counts the step from the workspace its own last snapshot names,
			// and one that had not caught up focused the workspace it was
			// already on and painted that title (measured). Where the focus
			// was elsewhere, the walk waits for it to reach the next workspace,
			// and presses again if it does not.
			if landed && before.FocusedWorkspaceID != next.WorkspaceID {
				landed = h.focusReaches(ctx, next.WorkspaceID, pressWithin)
			}
		}
		if !landed {
			return backend.Errorf(backend.CodeUnexpected, "the client did not reach %s: its workspace key went unread", next.WorkspaceID)
		}
		idx = (idx + dir + len(ordered)) % len(ordered)
		at.set(resolved{kind: kindWorkspace, workspace: ordered[idx]})
	}
	if steps > 0 {
		// The title comes at the START of the switch; the client then
		// paints the workspace as one synchronized frame, and reads keys
		// for its pane once that frame is out. Bytes forwarded between
		// the two went nowhere (measured: a marker typed straight after
		// the title never echoed). So the frame's end is waited for,
		// and a beat after it.
		if !painted(pressWithin) {
			return backend.Errorf(backend.CodeUnexpected, "the client did not paint %s", to.workspace.WorkspaceID)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(keyGap):
		}
	}
	for _, args := range steeringArgs(to)[1:] {
		if _, err := h.run(ctx, args...); err != nil {
			return err
		}
	}
	at.set(to)
	return nil
}

// focusReaches reads the server's focus until it is on the workspace, for as
// long as within allows. A walked client's move is the server's focus moving
// (§8.10), so this is where the walk sees a press was read.
func (h *Herdr) focusReaches(ctx context.Context, workspaceID string, within time.Duration) bool {
	deadline := time.Now().Add(within)
	for {
		if snap, err := h.snapshot(ctx); err == nil && snap.FocusedWorkspaceID == workspaceID {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		select {
		case <-ctx.Done():
			return false
		case <-time.After(focusPoll):
		}
	}
}

// titleMark is what shows the client landed on a workspace: a window title,
// whatever it says. The label a workspace list gives is not the name its
// title paints: for a workspace nobody named, herdr names the list's row from
// the directory its process is in and the title from the directory its
// terminal last reported, and the two differ (measured: a Claude Code pane
// listed as `agamemnon` painted `host: ~`, so every walk onto it was given up
// as unread while the client sat on it). A press that goes unread paints no
// title at all, so any title still tells the two apart.
func titleMark() []byte {
	return []byte("\x1b]0;")
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
	// How long a press is given to be answered by the client's title, and
	// how many times it is made before the walk gives up.
	pressWithin   = 1500 * time.Millisecond
	pressAttempts = 2
	// How often the server's focus is read while a press is confirmed.
	focusPoll = 50 * time.Millisecond

	// The end of a synchronized-output frame (DEC 2026), which closes the
	// paint of a workspace the client has just switched to.
	frameEnd = "\x1b[?2026l"

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
func writeBareConfig(prefix string) (string, error) {
	f, err := os.CreateTemp("", "herdr-bare-*.toml")
	if err != nil {
		return "", backend.Wrapf(backend.CodeUnexpected, err, "creating a stripped herdr config for a bare session attach")
	}
	if _, err := f.WriteString(bareSessionConfig(prefix)); err != nil {
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
