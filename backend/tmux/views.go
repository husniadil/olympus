package tmux

import (
	"context"
	"strings"

	"github.com/husniadil/olympus/backend"
)

const viewFormat = "#{session_name}\x1f#{session_id}\x1f#{session_group}\x1f#{session_attached}"

// CreateView adds a grouped session onto an existing one (behavior §9).
//
// The group is keyed on the base's immutable session ID, never on its name
// (§9.1): tmux resolves a -t target against GROUP names before session names,
// so a group named after its base makes the base's own name ambiguous, and a
// later operation on the base can land on the group instead.
func (t *Tmux) CreateView(ctx context.Context, base, name string) (backend.View, error) {
	if name == "" {
		return backend.View{}, backend.Errorf(backend.CodeUsage, "a view needs a name")
	}
	id, err := t.sessionID(ctx, base)
	if err != nil {
		return backend.View{}, err
	}
	if _, err := t.run(ctx, nil, "new-session", "-d", "-t", id, "-s", name); err != nil {
		return backend.View{}, err
	}
	viewID, err := t.sessionID(ctx, name)
	if err != nil {
		return backend.View{}, err
	}
	return backend.View{Name: name, Base: base, ID: viewID}, nil
}

// ScrollView moves a view back into its history, leaving the base untouched.
func (t *Tmux) ScrollView(ctx context.Context, view string, lines int) error {
	if lines == 0 {
		return nil
	}
	pane := paneTarget(view)
	if _, err := t.run(ctx, nil, "copy-mode", "-t", pane); err != nil {
		return err
	}
	command := "scroll-up"
	if lines < 0 {
		command, lines = "scroll-down", -lines
	}
	_, err := t.run(ctx, nil, "send-keys", "-t", pane, "-X", "-N", itoa(lines), command)
	return err
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

	rows := splitLines(out)
	groups := map[string]string{} // group -> base session name
	for _, line := range rows {
		f := strings.Split(line, "\x1f")
		if len(f) < 4 || f[2] == "" {
			continue
		}
		// The oldest member of a group is its base, and tmux lists in
		// creation order, so the first sighting wins.
		if _, seen := groups[f[2]]; !seen {
			groups[f[2]] = f[0]
		}
	}

	var views []backend.View
	for _, line := range rows {
		f := strings.Split(line, "\x1f")
		if len(f) < 4 || f[2] == "" {
			continue
		}
		owner := groups[f[2]]
		if f[0] == owner {
			continue
		}
		if base != "" && owner != base {
			continue
		}
		views = append(views, backend.View{
			Name:     f[0],
			Base:     owner,
			ID:       f[1],
			Attached: f[3] == "1",
		})
	}
	return views, nil
}

// sessionID reads a session's immutable id from the listing.
//
// display-message would be the obvious call and is the wrong one: with no
// attached client it has no target to render against and yields the empty
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
