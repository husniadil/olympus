package herdr

import (
	"context"

	"github.com/husniadil/olympus/backend"
)

// statusSource identifies Olympus as the writer of the metadata it sets
// (behavior §17.1). herdr scopes reported metadata by source so two reporters
// cannot silently overwrite each other.
const statusSource = "olympus"

// statusToken is the metadata key a session's status is kept under (§17.1).
const statusToken = "status"

// SetStatus records an opaque label on a session (§13.1).
//
// herdr keeps display-only metadata on the server for a workspace and for a
// pane alike, and hands it back on the row, which is a store that outlives the
// process that wrote it — the property that makes a status possible at all,
// since the reporter is inside the session and the reader is outside and they
// never run at the same moment.
//
// The status lives on the level the target names: a workspace's status is the
// workspace's, so every pane in it reads the same one; a pane's is its own. A
// tab has no metadata of its own, so a tab target's status is its focused
// pane's (§3.6). The value is stored and returned exactly as given. Olympus
// does not interpret it and defines no vocabulary of states.
func (h *Herdr) SetStatus(ctx context.Context, target, status string) error {
	r, err := h.resolve(ctx, target)
	if err != nil {
		return err
	}
	var args []string
	if r.kind == kindWorkspace {
		args = []string{"workspace", "report-metadata", r.workspace.WorkspaceID, "--source", statusSource}
	} else {
		if r.pane.PaneID == "" {
			return backend.Errorf(backend.CodeSessionNotFound, "%s %s has no pane to act on", r.kind, target)
		}
		args = []string{"pane", "report-metadata", r.pane.PaneID, "--source", statusSource}
	}
	if status == "" {
		// Clearing rather than storing an empty value, so a status that was
		// unset and one that was set to nothing read the same way.
		args = append(args, "--clear-token", statusToken)
	} else {
		args = append(args, "--token", statusToken+"="+status)
	}
	_, err = h.run(ctx, args...)
	return err
}

// Status reports the label, empty when the session has never been given one.
//
// Empty is a real answer, not an error: a caller must be able to tell "has
// reported nothing" from "could not ask" (§3.5, §13.1).
func (h *Herdr) Status(ctx context.Context, target string) (string, error) {
	r, err := h.resolve(ctx, target)
	if err != nil {
		return "", err
	}
	if r.kind == kindWorkspace {
		return r.workspace.Tokens[statusToken], nil
	}
	return r.pane.Tokens[statusToken], nil
}

// The grouped-view concept does not exist here: a pane belongs to one tab of
// one workspace, and nothing makes a second independently-scrollable window
// onto it. Refusing is the contract — emulating one badly would give a caller a
// view whose scroll position is somebody else's (§9).
func (h *Herdr) CreateView(ctx context.Context, base string, spec backend.ViewSpec) (backend.View, error) {
	return backend.View{}, backend.Errorf(backend.CodeUnsupported, "herdr has no views")
}

func (h *Herdr) ScrollView(ctx context.Context, view string, lines int) error {
	return backend.Errorf(backend.CodeUnsupported, "herdr has no views")
}

func (h *Herdr) Views(ctx context.Context, base string) ([]backend.View, error) {
	return nil, backend.Errorf(backend.CodeUnsupported, "herdr has no views")
}

// ServerEnv is unsupported: herdr has no command that reads or writes a
// server-global environment. A pane's environment is chosen when the pane is
// created and is never readable back, which is not the same question (§12).
func (h *Herdr) ServerEnv(ctx context.Context, key string) (string, bool, error) {
	return "", false, backend.Errorf(backend.CodeUnsupported,
		"herdr has no server environment: there is no command that reads or sets one")
}
