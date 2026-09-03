package tmux

import (
	"context"
	"strconv"
	"strings"

	"github.com/husniadil/olympus/backend"
)

const viewFormat = "#{session_name}\x1f#{session_id}\x1f#{session_group}\x1f#{session_attached}"

// ViewPrefix is the reserved name prefix every view carries (behavior §17.1).
//
// It is load-bearing beyond cosmetics: enumerating views selects on it, so
// changing it orphans every view an older binary created.
const ViewPrefix = "olympus-view-"

// passthroughTable is the reserved key table a view runs under (behavior
// §17.1). It is server-global rather than scoped to the view's session.
const passthroughTable = "olympus-passthrough"

// CreateView adds a grouped session onto an existing one (behavior §9).
//
// The group is keyed on the base's immutable session ID, never on its name
// (§9.1): tmux resolves a -t target against GROUP names before session names,
// so a group named after its base makes the base's own name ambiguous, and a
// dead base's group name can outlive it inside a stale view — grouping by name
// then silently joins the wrong window set.
//
// This defines a server-global key table (§9.3). It is inert to every session
// that does not point at it, which is what keeps a view from changing what the
// operator's own sessions do when Olympus is aimed at a server they already
// run. Nothing else about that server is reconfigured — in particular not
// terminal-features, which has no per-session form and would alter rendering
// for every client of it.
func (t *Tmux) CreateView(ctx context.Context, base string, spec backend.ViewSpec) (backend.View, error) {
	if spec.Name == "" {
		return backend.View{}, backend.Errorf(backend.CodeUsage, "a view needs a name")
	}

	// Probed BEFORE anything is created, and this is load-bearing rather than
	// defensive: `new-session -t '=<base>'` SUCCEEDS when the base does not
	// exist — it simply starts a brand-new group under that name — so without
	// this check a typo'd or already-dead base silently produces an orphan
	// session and reports success.
	switch t.Probe(ctx, base) {
	case backend.StateAbsent:
		return backend.View{}, backend.Errorf(backend.CodeSessionNotFound, "no session %s to view", base)
	case backend.StateError:
		return backend.View{}, backend.Errorf(backend.CodeBackendUnavailable, "cannot reach tmux to view %s", base)
	}

	// The base's identity and what it is currently showing, read in one call.
	out, err := t.run(ctx, nil, "display-message", "-p", "-t", windowTarget(base),
		"#{session_id}\x1f#{window_index}\x1f#{pane_id}")
	if err != nil {
		return backend.View{}, named(base, err)
	}
	fields := SplitFields(strings.TrimSpace(out))
	if len(fields) != 3 || fields[0] == "" {
		return backend.View{}, backend.Errorf(backend.CodeUnexpected,
			"tmux described session %s as %q, which is not a session id, window and pane", base, strings.TrimSpace(out))
	}
	sessionID, windowIndex, paneID := fields[0], fields[1], fields[2]

	// A pinned window is checked against the base BEFORE the view exists, and
	// resolved to its index. `select-window -t <view>:<name>` would accept a
	// name too, but tmux matches window names by prefix and then by search
	// (fnmatch) — so "sec" would silently land on "second", and a caller who
	// typo'd a name would get a window rather than an error.
	if spec.Window != "" {
		index, err := t.windowIndex(ctx, base, spec.Window)
		if err != nil {
			return backend.View{}, err
		}
		windowIndex, paneID = index, ""
	}

	if _, err := t.run(ctx, nil, "new-session", "-d", "-t", sessionID, "-s", spec.Name); err != nil {
		return backend.View{}, named(base, err)
	}

	// Every step after the session exists cleans up on failure, or a partly
	// configured view leaks — a session with no status bar, no prefix and no
	// key table is worse than no view at all.
	fail := func(err error) (backend.View, error) {
		_, _ = t.run(context.WithoutCancel(ctx), nil, "kill-session", "-t", sessionTarget(spec.Name))
		return backend.View{}, err
	}

	mouse := "off"
	if spec.Mouse {
		mouse = "on"
	}
	// A read-only posture, session-local: no status bar so the view is pure
	// content, no prefix so the multiplexer's own key passes through to the
	// pane, and an empty key table so no root binding fires.
	for _, option := range [][]string{
		{"status", "off"},
		{"prefix", "None"},
		{"prefix2", "None"},
		{"key-table", passthroughTable},
		{"mouse", mouse},
	} {
		args := append([]string{"set-option", "-t", windowTarget(spec.Name)}, option...)
		if _, err := t.run(ctx, nil, args...); err != nil {
			return fail(err)
		}
	}

	// The empty pass-through table also strips tmux's default mouse bindings,
	// so the wheel would do nothing at all. The two wheel bindings are
	// re-added: enter copy-mode for a pane whose application ignores the mouse,
	// and forward the event when it grabs it; then a click binding below. The
	// table is server-global, so re-binding on every view creation is
	// idempotent.
	if _, err := t.run(ctx, nil, "bind-key", "-T", passthroughTable, "WheelUpPane",
		"if", "-F", "-t=", "#{mouse_any_flag}", "send -M",
		"if -F -t= '#{pane_in_mode}' 'send -M' 'copy-mode -e ; send -M'"); err != nil {
		return fail(err)
	}
	if _, err := t.run(ctx, nil, "bind-key", "-T", passthroughTable, "WheelDownPane", "send", "-M"); err != nil {
		return fail(err)
	}
	// A click selects the pane under it and is forwarded, as tmux's own root
	// binding does — a view attached interactively (§8.9) needs the pane to be
	// reachable by touch, where there is no keyboard shortcut to move focus.
	// The active pane is the shared window's (§9.4), so this moves the base
	// too, exactly as a click in the base would. No drag binding: copy-mode on
	// a shared pane would drag the base into it.
	if _, err := t.run(ctx, nil, "bind-key", "-T", passthroughTable, "MouseDown1Pane",
		"select-pane", "-t=", "\\;", "send-keys", "-M"); err != nil {
		return fail(err)
	}

	// Open the view on what the base is showing, or on the pinned window. A
	// grouped session keeps its own current WINDOW, so selecting one here moves
	// only the view — but the current PANE belongs to the shared window, so
	// the select-pane below also moves the BASE's active pane (§9.4). That is
	// unobservable on the single-pane sessions Olympus's own creation verbs
	// produce, and becomes visible only if a consumer splits a base themselves.
	// A pinned view therefore never selects a pane at all: the point of
	// pinning is to show one window without disturbing anyone, and the pane
	// is the one thing a view cannot choose privately.
	if _, err := t.run(ctx, nil, "select-window", "-t", sessionTarget(spec.Name)+":"+windowIndex); err != nil {
		return fail(err)
	}
	if paneID != "" {
		if _, err := t.run(ctx, nil, "select-pane", "-t", paneID); err != nil {
			return fail(err)
		}
	}

	viewID, err := t.sessionID(ctx, spec.Name)
	if err != nil {
		return fail(err)
	}
	return backend.View{Name: spec.Name, Base: base, ID: viewID}, nil
}

// windowIndex resolves a window the caller named — by index or by name — to
// its index on the base, or reports not-found naming both.
//
// Read from list-windows rather than trusting tmux's own target matching: an
// exact match on the index or the whole name is the only one accepted here.
func (t *Tmux) windowIndex(ctx context.Context, base, window string) (string, error) {
	out, err := t.run(ctx, nil, "list-windows", "-t", windowTarget(base), "-F", "#{window_index}\x1f#{window_name}")
	if err != nil {
		return "", named(base, err)
	}
	for _, line := range splitLines(out) {
		f := SplitFields(line)
		if len(f) < 2 {
			continue
		}
		if f[0] == window || f[1] == window {
			return f[0], nil
		}
	}
	return "", backend.Errorf(backend.CodeSessionNotFound, "session %s has no window %s", base, window)
}

// ScrollView scrolls a view into its history, leaving the base untouched.
//
// Positive lines scroll up into scrollback, negative back toward the live tail.
// Scrolling past either end is clamped by tmux and leaves the pane in copy
// mode, so there is nothing to detect or recover from; entering copy mode when
// already in it preserves the position, so successive calls accumulate.
func (t *Tmux) ScrollView(ctx context.Context, view string, lines int) error {
	if lines == 0 {
		return nil
	}
	pane := paneTarget(view)
	if _, err := t.run(ctx, nil, "copy-mode", "-t", pane); err != nil {
		return named(view, err)
	}
	command, count := "scroll-up", lines
	if lines < 0 {
		command, count = "scroll-down", -lines
	}
	_, err := t.run(ctx, nil, "send-keys", "-t", pane, "-X", "-N", itoa(count), command)
	return named(view, err)
}

// paneRect is one pane's rectangle within its window, in cells, both edges
// inclusive — exactly as tmux's #{pane_left} #{pane_top} #{pane_right}
// #{pane_bottom} report it.
type paneRect struct {
	id                       string
	left, top, right, bottom int
}

// paneAt reports the pane whose rectangle contains the cell, or "" when none
// does: a border between panes belongs to no pane, and neither does a cell
// outside the window.
//
// Measured on an 80x24 window split in two: %0 spans 0..39, %1 spans 41..79,
// and column 40 is the border.
func paneAt(panes []paneRect, col, row int) string {
	for _, p := range panes {
		if col >= p.left && col <= p.right && row >= p.top && row <= p.bottom {
			return p.id
		}
	}
	return ""
}

// FocusView selects the pane under a cell of the view's current window
// (behavior §9.6).
//
// A view attached with mouse reporting off — so the terminal keeps its own text
// selection — never delivers a click to tmux, so the §9.3 click binding cannot
// fire. The caller knows the cell and hands it here instead. The active pane is
// the shared window's (§9.4), so the base follows, exactly as the binding
// would have moved it.
//
// A cell on a border or outside every pane selects nothing: "" is a real
// answer rather than an error, since the caller's coordinate was legitimate
// and there is simply no pane there.
func (t *Tmux) FocusView(ctx context.Context, view string, col, row int) (string, error) {
	if col < 0 || row < 0 {
		return "", backend.Errorf(backend.CodeUsage, "a cell is non-negative, not (%d, %d)", col, row)
	}
	// The view's own current window, which is why the target is the view
	// rather than its base (§9.4: the window is per-session even in a group).
	out, err := t.run(ctx, nil, "list-panes", "-t", windowTarget(view), "-F",
		"#{pane_id}\x1f#{pane_left}\x1f#{pane_top}\x1f#{pane_right}\x1f#{pane_bottom}")
	if err != nil {
		return "", named(view, err)
	}
	var panes []paneRect
	for _, line := range splitLines(out) {
		f := SplitFields(line)
		if len(f) != 5 {
			continue
		}
		var p paneRect
		p.id = f[0]
		edges := []*int{&p.left, &p.top, &p.right, &p.bottom}
		ok := true
		for i, edge := range edges {
			n, err := strconv.Atoi(f[i+1])
			if err != nil {
				ok = false
				break
			}
			*edge = n
		}
		if !ok {
			return "", backend.Errorf(backend.CodeUnexpected,
				"tmux described a pane of %s as %q, which is not an id and four edges", view, line)
		}
		panes = append(panes, p)
	}
	pane := paneAt(panes, col, row)
	if pane == "" {
		return "", nil
	}
	if _, err := t.run(ctx, nil, "select-pane", "-t", pane); err != nil {
		return "", named(view, err)
	}
	return pane, nil
}

// Views lists the views this backend owns, optionally for one base.
//
// The base comes straight from tmux's own #{session_group}. Because §9.1 groups
// a view onto its base by the BASE'S SESSION ID rather than a synthetic name,
// tmux's group-name answer for ANY member of that group already IS the base's
// real session name — measured: a base `zzz-base` and a view grouped onto it
// both answer `zzz-base`. No lookup, no bookkeeping, and no inference from list
// order (§9.5).
//
// Rows are selected by the reserved prefix, which is what makes this "views
// this backend owns" rather than "every grouped session". An operator who
// groups two sessions of their own has not created an Olympus view, and
// reporting one would invite a sweep to kill their session.
func (t *Tmux) Views(ctx context.Context, base string) ([]backend.View, error) {
	out, err := t.run(ctx, nil, "list-sessions", "-F", viewFormat)
	if err != nil {
		if isNoServer(err) {
			return nil, nil
		}
		return nil, err
	}

	var views []backend.View
	for _, line := range splitLines(out) {
		f := SplitFields(line)
		if len(f) < 4 {
			continue
		}
		name, id, group, attached := f[0], f[1], f[2], f[3]
		if !strings.HasPrefix(name, ViewPrefix) {
			continue
		}
		if base != "" && group != base {
			continue
		}
		views = append(views, backend.View{
			Name:     name,
			Base:     group,
			ID:       id,
			Attached: attached == "1",
		})
	}
	return views, nil
}

// sessionID reads a session's immutable id from the listing.
//
// display-message would be the obvious call and is the wrong one for this: with
// no attached client it has no target to render against and yields the empty
// string with exit 0 — the same silent-empty failure mode §3.4 warns about for
// format variables that do not exist.
func (t *Tmux) sessionID(ctx context.Context, name string) (string, error) {
	sessions, err := t.Sessions(ctx)
	if err != nil {
		return "", err
	}
	for _, s := range sessions {
		if s.Name == name {
			return s.ID, nil
		}
	}
	return "", backend.Errorf(backend.CodeSessionNotFound, "no session %s", name)
}
