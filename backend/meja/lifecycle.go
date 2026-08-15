package meja

import (
	"context"
	"strconv"
	"strings"

	"github.com/husniadil/olympus/backend"
)

// sessionFormat and paneFormat are the only shapes parsed here.
//
// meja supports tmux-style -F formats, but NOT the same set on both verbs:
// #{session_root} and #{session_windows} come back as their own literal text
// from list-sessions, so a session's directory has to be read from its pane
// instead. Asking for a field a verb does not know is silent — the literal is
// returned as data — so every field here was checked against a live server.
const (
	sessionFormat = "#{session_name}\t#{session_id}\t#{session_created}"
	paneFormat    = "#{pane_id}\t#{window_index}\t#{pane_dead}\t#{pane_current_command}\t#{pane_current_path}"
)

func (m *Meja) Create(ctx context.Context, spec backend.CreateSpec) (backend.Session, error) {
	if spec.Name == "" {
		return backend.Session{}, backend.Errorf(backend.CodeUsage, "a session needs a name")
	}
	if spec.RemainOnExit {
		return backend.Session{}, backend.Errorf(backend.CodeUnsupported,
			"meja closes a pane when its command exits and keeps no corpse")
	}

	args := []string{"new-session", "-d", "-s", spec.Name}
	if spec.Dir != "" {
		args = append(args, "-r", spec.Dir)
	}
	// No -x/-y: meja sizes a session from its first client, and a detached
	// session takes a default. Cols and Rows are therefore honoured on attach
	// rather than at creation (§2.10).
	if len(spec.Command) > 0 {
		// Everything after -- is the first pane's process, spawned directly
		// rather than typed into a shell (§2.3).
		args = append(args, "--")
		args = append(args, spec.Command...)
	}

	if _, err := m.run(ctx, nil, args...); err != nil {
		return backend.Session{}, err
	}

	// Read the row back rather than synthesising one: a created session's id
	// and creation time are the server's to state, and a synthesised row would
	// be a claim about a session we have not actually seen.
	sessions, err := m.Sessions(ctx)
	if err != nil {
		return backend.Session{}, err
	}
	for _, s := range sessions {
		if s.Name == spec.Name {
			return s, nil
		}
	}
	// Created, then gone before it could be read — an instantly-exiting command
	// does exactly this. Absence here is a real outcome, not a failure (§2.4).
	return backend.Session{Name: spec.Name, Liveness: backend.LivenessGone}, nil
}

func (m *Meja) Sessions(ctx context.Context) ([]backend.Session, error) {
	out, err := m.run(ctx, nil, "list-sessions", "-F", sessionFormat)
	if err != nil {
		if noServer(err) {
			// No server means no sessions, which is an answer. Reporting it as
			// an outage would make an empty listing indistinguishable from an
			// unreachable one (§3.2).
			return nil, nil
		}
		return nil, err
	}

	var sessions []backend.Session
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Split(strings.TrimRight(line, "\r"), "\t")
		if len(fields) < 3 || fields[0] == "" {
			continue
		}
		sessions = append(sessions, backend.Session{
			Name:     fields[0],
			ID:       fields[1],
			Liveness: backend.LivenessPresent,
			CWD:      m.cwdOf(ctx, fields[0]),
		})
	}
	return sessions, nil
}

// createdAt reads a session's creation time, zero when it cannot be had.
func (m *Meja) createdAt(ctx context.Context, target string) int64 {
	if target == "" {
		return 0
	}
	out, err := m.run(ctx, nil, "list-sessions", "-F", "#{session_name}\t#{session_created}")
	if err != nil {
		return 0
	}
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Split(strings.TrimRight(line, "\r"), "\t")
		if len(fields) == 2 && fields[0] == target {
			n, convErr := strconv.ParseInt(strings.TrimSpace(fields[1]), 10, 64)
			if convErr == nil {
				return n
			}
		}
	}
	return 0
}

// cwdOf reads a session's directory from its first pane.
//
// list-sessions cannot report it — #{session_root} is not a field that verb
// knows — so this is the only place the answer exists. A failure here yields an
// empty directory rather than failing the listing: a row with an unknown
// directory is far more useful than no rows at all.
func (m *Meja) cwdOf(ctx context.Context, name string) string {
	panes, err := m.Panes(ctx, name)
	if err != nil || len(panes) == 0 {
		return ""
	}
	return panes[0].CurrentPath
}

func (m *Meja) Panes(ctx context.Context, target string) ([]backend.Pane, error) {
	args := []string{"list-panes", "-F", paneFormat}
	if target == "" {
		args = append(args, "-a")
	} else {
		args = append(args, "-t", target)
	}

	out, err := m.run(ctx, nil, args...)
	if err != nil {
		if target == "" && noServer(err) {
			return nil, nil
		}
		return nil, named(target, err)
	}

	// meja has no #{pane_created} field, so a pane's creation time is its
	// SESSION's. That is not an approximation here: every session Olympus
	// creates is single-window and single-pane, so the pane and the session
	// were created by the same command at the same moment.
	created := m.createdAt(ctx, target)

	var panes []backend.Pane
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Split(strings.TrimRight(line, "\r"), "\t")
		if len(fields) < 5 || fields[0] == "" {
			continue
		}
		window, _ := strconv.Atoi(fields[1])
		panes = append(panes, backend.Pane{
			ID:             fields[0],
			SessionName:    target,
			WindowIndex:    window,
			Dead:           fields[2] == "1",
			CurrentCommand: fields[3],
			CurrentPath:    fields[4],
			CreatedAt:      created,
			Liveness:       backend.LivenessPresent,
		})
	}
	return panes, nil
}

// Probe answers presence as a tri-state, never finalising on doubt.
func (m *Meja) Probe(ctx context.Context, target string) backend.State {
	sessions, err := m.Sessions(ctx)
	if err != nil {
		// Could not ask. Reporting absent here would let a caller reconciling
		// state destroy a session that is merely unreachable (§3.5).
		return backend.StateError
	}
	for _, s := range sessions {
		if s.Name == target {
			return backend.StatePresent
		}
	}
	return backend.StateAbsent
}

func (m *Meja) Kill(ctx context.Context, target string) error {
	_, err := m.run(ctx, nil, "kill-session", "-t", target)
	if err != nil && backend.CodeOf(err) == backend.CodeBackendUnavailable {
		// Nothing is listening, so the session is gone by any definition a
		// caller cares about. Killing what is already dead is success (§2.8).
		return nil
	}
	return named(target, err)
}

// SessionOf maps a session target onto the session's NAME.
//
// It exists because meja identifies a pane's own session by ID: it puts
// MEJA_SESSION_TARGET=@1 in every pane's environment, while Olympus addresses
// sessions by name everywhere. The "@" is meja's own spelling for a target and
// is absent from the #{session_id} the server reports, so it is stripped before
// the comparison rather than matched against.
//
// Both spellings do address the same session, so this is a translation for
// Olympus's vocabulary rather than a repair of meja's.
func (m *Meja) SessionOf(ctx context.Context, target string) (string, error) {
	want := strings.TrimPrefix(target, "@")
	out, err := m.run(ctx, nil, "list-sessions", "-F", "#{session_id}\t#{session_name}")
	if err != nil {
		return "", err
	}
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Split(strings.TrimRight(line, "\r"), "\t")
		if len(fields) == 2 && fields[0] == want {
			return fields[1], nil
		}
	}
	return "", backend.Errorf(backend.CodeSessionNotFound, "no session %s on this server", target)
}
