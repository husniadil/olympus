package herdr

import (
	"context"
	"os"
	"os/exec"

	"github.com/husniadil/olympus/backend"
)

// bareSessionConfig strips a herdr session client down to a plain pane: every
// keybinding that mutates layout is unbound, every piece of chrome (sidebar,
// tab bar, pane borders and scrollbars, agent labels, window title, mobile
// header) is hidden, and copy-on-select is left on so a selection still copies.
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
	if h.startedTheServer() {
		// Our server, our configuration directory: it is the one that server
		// was booted against, and there is nothing of anybody else's in it.
		cmd.Env = h.env(attachEnv())
	} else {
		// A server this handle did not start belongs to whoever runs it, and
		// the attach client is the one invocation whose behaviour its
		// configuration decides. Imposing a directory of Olympus's own would
		// hand a human their own terminal configured like a fresh install
		// (src/client/mod.rs:1225-1234).
		cmd.Env = h.socketEnv(attachEnv())
	}
	return backend.Attachment{Cmd: cmd}, nil
}

// attachSessionClient runs `herdr session attach <name>` — the multiplexer's
// own session client, which unlike the raw terminal stream carries selection,
// scrollback and copy.
//
// It deliberately does NOT resolvePane: herdr's session client resolves its
// server from the CONFIGURATION DIRECTORY and ignores HERDR_SOCKET_PATH (env.go
// §), so the target is a herdr SESSION name a caller supplies, not one of the
// socket-addressed panes this backend otherwise drives. That decoupling is the
// whole reason this is a separate client — Olympus's pane registry is simply
// not the source of the name, and re-pointing it would be a backend rewrite.
//
// The ambient configuration is the right one to read (the operator's real
// sessions live in their own config directory), so attachEnv is used unchanged
// rather than env(): imposing Olympus's directory would look for the session in
// a directory that never had it. attachEnv already strips the ambient HERDR_*
// identity (HERDR_SESSION, HERDR_PANE_ID, HERDR_TAB_ID, HERDR_WORKSPACE_ID,
// HERDR_CLIENT_SOCKET_PATH, HERDR_SOCKET_PATH), so the client does not detect
// it is being launched from inside a herdr pane.
//
// herdr's `session attach` takes only a NAME — no --viewer and no --takeover
// (verified against the binary) — so there is no read-only or supersession
// control to pass. A viewer attach is already refused above; an explicit
// opt-out of supersession is reported as unhonored rather than silently
// dropped.
func (h *Herdr) attachSessionClient(ctx context.Context, target string, spec backend.AttachSpec) (backend.Attachment, error) {
	cmd := exec.CommandContext(ctx, "herdr", "session", "attach", target)
	env := attachEnv()

	att := backend.Attachment{Cmd: cmd}
	if spec.Bare {
		// A stripped config that hides the client's chrome. HERDR_CONFIG_PATH
		// overrides the config FILE without changing the config directory the
		// session resolves against (verified: the session still resolves), so
		// the client renders as a plain pane while still attaching the same
		// session. The temp file is reaped when the attach ends.
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
