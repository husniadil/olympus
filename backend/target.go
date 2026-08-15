package backend

import "strings"

// paneIDPrefix marks a target that addresses a pane rather than a session.
const paneIDPrefix = "%"

// A PaneLister returns a full-server pane listing. Resolution takes one rather
// than a slice so the listing — a subprocess call — happens only for the
// targets that actually need it.
type PaneLister func() ([]Pane, error)

// ResolveTarget turns a caller's target into a session name (behavior §10).
//
// A target beginning with "%" addresses a pane, and is swapped for its owning
// session's name; any other target passes through unchanged. This exists in one
// place on purpose: every operation that compares a target against a session
// name — or keys a write lock on it — must resolve first, or a pane-id caller
// silently mismatches every name, which is the source of false "already gone"
// and false "died" reports.
//
// A backend with no pane-id concept passes a nil lister, which makes every
// target ordinary. On such a backend "%7" is simply an unknown session name
// under the normal lookup: still not-found downstream, and never a crash here.
func ResolveTarget(target string, panes PaneLister) (string, error) {
	if target == "" {
		return "", Errorf(CodeUsage, "no target given")
	}
	if panes == nil || !strings.HasPrefix(target, paneIDPrefix) {
		return target, nil
	}

	listed, err := panes()
	if err != nil {
		// Deliberately propagated rather than flattened into not-found. "I
		// could not ask" and "it is definitely gone" must stay different
		// answers, or a reconciling caller finalizes a live session on doubt
		// (§3.2).
		return "", err
	}

	// A pane id is not unique across rows: a base session and its views share
	// the same underlying pane (§3.4). The base is the earliest row, so the
	// oldest match wins — resolving to a view would mean operating on the
	// wrong session, and killing one would leave the real session running.
	var owner *Pane
	for i := range listed {
		if listed[i].ID != target {
			continue
		}
		if owner == nil || listed[i].CreatedAt < owner.CreatedAt {
			owner = &listed[i]
		}
	}
	if owner == nil {
		// Named by pane id, not by a session name: there was never a session
		// to name, since resolution never happened.
		return "", Errorf(CodeSessionNotFound, "no pane %s", target)
	}
	return owner.SessionName, nil
}
