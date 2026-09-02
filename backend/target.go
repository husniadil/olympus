package backend

import "strings"

// paneIDPrefix marks a target that addresses a pane rather than a session on
// backends that spell pane ids apart from session names.
const paneIDPrefix = "%"

// A PaneIDShape reports whether a target is spelled like a pane id.
//
// The SHAPE is what differs between backends; the rule that a pane id addresses
// its session does not. tmux writes "%0", meja writes a bare "1", and a backend
// with no pane concept has no shape at all. Passing the shape in keeps that one
// rule in one place instead of growing a copy per backend.
type PaneIDShape func(string) bool

// PrefixedPaneID is the shape used by backends that mark pane ids with "%".
func PrefixedPaneID(target string) bool { return strings.HasPrefix(target, paneIDPrefix) }

// NumericPaneID is the shape used by backends whose pane ids are bare integers.
//
// Safe only where a session name cannot be entirely numeric, or a target would
// be read as a pane when the caller meant a session of the same spelling. meja
// rejects such names outright — "session name must not be entirely numeric" —
// which is what makes the shape unambiguous there rather than merely unlikely.
func NumericPaneID(target string) bool { return allDigits(target) }

// IndexedPaneID is the shape used by backends whose pane ids name the window
// and the pane by number: "w<number>:p<number>".
//
// Each number is spelled the way herdr spells every public id: base 32 over the
// alphabet 123456789ABCDEFGHJKMNPQRSTVWXYZ0, digits for the first nine
// allocations and letters from the tenth. The tenth pane of a workspace is
// "w1:pA", the tenth workspace is "wA", and the workspace counter survives a
// server restart, so a predicate that reads digits alone silently stops
// recognising ids on any server that has lived long enough. Measured.
//
// Unlike the other two shapes this one is not structurally unambiguous — the
// backend that uses it will let a caller name a session anything, that spelling
// included. So the backend REJECTS such a name at creation rather than leaving
// resolution to guess, which is what turns "probably a pane" into "certainly a
// pane" here.
func IndexedPaneID(target string) bool {
	rest, ok := strings.CutPrefix(target, "w")
	if !ok {
		return false
	}
	window, pane, ok := strings.Cut(rest, ":p")
	if !ok {
		return false
	}
	return publicNumber(window) && publicNumber(pane)
}

// publicNumberAlphabet is the digit set of a herdr public id, in digit order.
// I, L, O and U are deliberately absent from it, and lowercase never appears.
const publicNumberAlphabet = "123456789ABCDEFGHJKMNPQRSTVWXYZ0"

func publicNumber(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if !strings.ContainsRune(publicNumberAlphabet, rune(s[i])) {
			return false
		}
	}
	return true
}

func allDigits(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// A PaneLister returns a full-server pane listing. Resolution takes one rather
// than a slice so the listing — a subprocess call — happens only for the
// targets that actually need it.
type PaneLister func() ([]Pane, error)

// ResolveTarget turns a caller's target into a session name (behavior §10).
//
// A target matching the backend's pane-id SHAPE addresses a pane, and is
// swapped for its owning session's name; any other target passes through
// unchanged. This exists in one
// place on purpose: every operation that compares a target against a session
// name — or keys a write lock on it — must resolve first, or a pane-id caller
// silently mismatches every name, which is the source of false "already gone"
// and false "died" reports.
//
// A backend with no pane-id concept passes a nil lister or a nil shape, which
// makes every target ordinary. On such a backend "%7" is simply an unknown
// session name under the normal lookup: still not-found downstream, and never a
// crash here.
func ResolveTarget(target string, shape PaneIDShape, panes PaneLister) (string, error) {
	if target == "" {
		return "", Errorf(CodeUsage, "no target given")
	}
	if panes == nil || shape == nil || !shape(target) {
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
