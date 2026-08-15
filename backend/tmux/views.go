package tmux

import (
	"context"
	"strconv"
	"strings"

	"github.com/husniadil/olympus/backend"
)

const viewFormat = "#{session_name}\x1f#{session_id}\x1f#{session_group}\x1f#{session_attached}\x1f#{session_created}"

// passthroughTable is the reserved key table a view runs under (behavior
// §17.1). It is server-global rather than scoped to the view's session.
const passthroughTable = "olympus-passthrough"

// hyperlinkFeature is the terminal-features entry that keeps OSC 8 hyperlinks
// from being stripped.
const hyperlinkFeature = "xterm-256color:hyperlinks"

// CreateView adds a grouped session onto an existing one (behavior §9).
//
// The group is keyed on the base's immutable session ID, never on its name
// (§9.1): tmux resolves a -t target against GROUP names before session names,
// so a group named after its base makes the base's own name ambiguous, and a
// dead base's group name can outlive it inside a stale view — grouping by name
// then silently joins the wrong window set.
//
// This mutates SERVER-GLOBAL state (§9.3). That is self-contained while Olympus
// owns the socket, which is the default; pointed at an operator's real tmux
// server, the same mutations land there permanently and are visible to every
// other client until that server is killed.
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
	fields := strings.Split(strings.TrimSpace(out), "\x1f")
	if len(fields) != 3 || fields[0] == "" {
		return backend.View{}, backend.Errorf(backend.CodeUnexpected,
			"tmux described session %s as %q, which is not a session id, window and pane", base, strings.TrimSpace(out))
	}
	sessionID, windowIndex, paneID := fields[0], fields[1], fields[2]

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

	if err := t.enableHyperlinks(ctx); err != nil {
		return fail(err)
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
	// so the wheel would do nothing at all. Only the two wheel bindings are
	// re-added: enter copy-mode for a pane whose application ignores the mouse,
	// and forward the event when it grabs it. The table is server-global, so
	// re-binding on every view creation is idempotent.
	if _, err := t.run(ctx, nil, "bind-key", "-T", passthroughTable, "WheelUpPane",
		"if", "-F", "-t=", "#{mouse_any_flag}", "send -M",
		"if -F -t= '#{pane_in_mode}' 'send -M' 'copy-mode -e ; send -M'"); err != nil {
		return fail(err)
	}
	if _, err := t.run(ctx, nil, "bind-key", "-T", passthroughTable, "WheelDownPane", "send", "-M"); err != nil {
		return fail(err)
	}

	// Open the view on what the base is showing. A grouped session keeps its
	// own current WINDOW, but the current PANE belongs to the shared window —
	// so this select-pane also moves the BASE's active pane (§9.4). That is
	// unobservable on the single-pane sessions Olympus's own creation verbs
	// produce, and becomes visible only if a consumer splits a base themselves.
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

// enableHyperlinks opts the terminal into OSC 8 passthrough, idempotently.
//
// tmux strips OSC 8 hyperlink sequences for clients whose declared terminal
// lacks the hyperlinks capability, and a headless PTY client never answers
// tmux's runtime feature probe — so without this, hyperlinks silently vanish
// for every consumer, with no error anywhere.
//
// The read before the append is what makes it idempotent: the option is an
// array and a second view would otherwise grow it on every creation.
func (t *Tmux) enableHyperlinks(ctx context.Context) error {
	current, err := t.run(ctx, nil, "show-options", "-g", "terminal-features")
	if err != nil {
		// Deliberately not ignored. If this read failed silently the guard
		// below would see an empty string and append unconditionally, which is
		// exactly the unbounded growth it exists to prevent.
		return err
	}
	if strings.Contains(current, hyperlinkFeature) {
		return nil
	}
	_, err = t.run(ctx, nil, "set-option", "-ga", "terminal-features", ","+hyperlinkFeature)
	return err
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

// Views lists the views onto a base session, or onto every session when base is
// empty. The base itself is never a view of itself, so it is excluded.
func (t *Tmux) Views(ctx context.Context, base string) ([]backend.View, error) {
	out, err := t.run(ctx, nil, "list-sessions", "-F", viewFormat)
	if err != nil {
		if isNoServer(err) {
			return nil, nil
		}
		return nil, err
	}

	// The base of a group is its OLDEST member, decided by SESSION ID.
	//
	// Two wrong discriminators, both tried:
	//
	//   - List order. tmux lists sessions sorted by NAME, and the reserved view
	//     prefix sorts before most session names — so taking the first row as
	//     the base reports every group with the base and the view swapped.
	//   - Creation time. #{session_created} has one-second granularity, and a
	//     view is created milliseconds after its base, so the two tie and the
	//     tie-break falls back to name order — the same bug again, just rarer
	//     and therefore worse.
	//
	// Session ids are monotonic and assigned in creation order, so they order
	// the group exactly.
	rows := splitLines(out)
	type owner struct {
		name string
		id   int
	}
	groups := map[string]owner{}
	for _, line := range rows {
		f := strings.Split(line, "\x1f")
		if len(f) < 5 || f[2] == "" {
			continue
		}
		id := sessionOrder(f[1])
		if seen, ok := groups[f[2]]; !ok || id < seen.id {
			groups[f[2]] = owner{name: f[0], id: id}
		}
	}

	var views []backend.View
	for _, line := range rows {
		f := strings.Split(line, "\x1f")
		if len(f) < 5 || f[2] == "" {
			continue
		}
		group := groups[f[2]]
		if f[0] == group.name {
			continue
		}
		if base != "" && group.name != base {
			continue
		}
		views = append(views, backend.View{
			Name:     f[0],
			Base:     group.name,
			ID:       f[1],
			Attached: f[3] == "1",
		})
	}
	return views, nil
}

// sessionOrder turns a session id such as "$7" into its creation order.
//
// An unparseable id sorts last rather than first: guessing that an unknown
// shape is the oldest would make it the group's base, which is exactly the
// mistake this ordering exists to prevent.
func sessionOrder(id string) int {
	n, err := strconv.Atoi(strings.TrimPrefix(id, "$"))
	if err != nil {
		return int(^uint(0) >> 1)
	}
	return n
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
