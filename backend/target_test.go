package backend_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/husniadil/olympus/backend"
)

func lister(panes ...backend.Pane) backend.PaneLister {
	return func() ([]backend.Pane, error) { return panes, nil }
}

// §10: any target not beginning with % passes through unchanged — and without
// a listing, since listing costs a subprocess call on every operation.
func TestOrdinaryTargetPassesThroughWithoutListing(t *testing.T) {
	called := false
	got, err := backend.ResolveTarget("build", backend.PrefixedPaneID, func() ([]backend.Pane, error) {
		called = true
		return nil, nil
	})
	if err != nil {
		t.Fatalf("ResolveTarget: %v", err)
	}
	if got != "build" {
		t.Errorf("resolved to %q, want %q", got, "build")
	}
	if called {
		t.Error("listed panes for a target that needed no resolution")
	}
}

// §10.1: a pane id is an address for the SESSION that owns the pane, not for
// the pane. After a second window exists, an operation still runs against the
// session's active window.
func TestPaneIDResolvesToItsOwningSession(t *testing.T) {
	got, err := backend.ResolveTarget("%7", backend.PrefixedPaneID, lister(
		backend.Pane{ID: "%3", SessionName: "other"},
		backend.Pane{ID: "%7", SessionName: "build"},
	))
	if err != nil {
		t.Fatalf("ResolveTarget: %v", err)
	}
	if got != "build" {
		t.Errorf("resolved to %q, want %q", got, "build")
	}
}

// §10: resolution failing is not-found naming the PANE ID, not a session name —
// there was never a session to name, since resolution never happened.
func TestUnknownPaneIDIsNotFoundNamingThePaneID(t *testing.T) {
	_, err := backend.ResolveTarget("%99", backend.PrefixedPaneID, lister(backend.Pane{ID: "%7", SessionName: "build"}))
	if !errors.Is(err, backend.ErrNotFound) {
		t.Fatalf("error is %v, want ErrNotFound", err)
	}
	if !strings.Contains(err.Error(), "%99") {
		t.Errorf("message %q does not name the pane id", err.Error())
	}
	if strings.Contains(err.Error(), "build") {
		t.Errorf("message %q names a session, but resolution never happened", err.Error())
	}
}

// §3.4: a base session and its views share the same underlying pane, so a pane
// id is not unique across rows. Resolution must land on the BASE — the earliest
// created_at — or a pane-id caller silently operates on a view, and killing it
// leaves the real session running.
func TestPaneIDSharedWithViewsResolvesToTheBase(t *testing.T) {
	got, err := backend.ResolveTarget("%7", backend.PrefixedPaneID, lister(
		backend.Pane{ID: "%7", SessionName: "olympus-view-build-a1b2", CreatedAt: 200},
		backend.Pane{ID: "%7", SessionName: "build", CreatedAt: 100},
		backend.Pane{ID: "%7", SessionName: "olympus-view-build-c3d4", CreatedAt: 300},
	))
	if err != nil {
		t.Fatalf("ResolveTarget: %v", err)
	}
	if got != "build" {
		t.Errorf("resolved to %q, want the base session %q", got, "build")
	}
}

// A corpse pane (remain-on-exit) is still a listed pane whose session exists.
// Resolution answers "which session owns this pane", not "is it healthy" —
// collapsing the two would turn every died-session question into not-found
// before the caller's own death handling could report it properly.
func TestCorpsePaneStillResolves(t *testing.T) {
	got, err := backend.ResolveTarget("%7", backend.PrefixedPaneID, lister(
		backend.Pane{ID: "%7", SessionName: "build", Dead: true},
	))
	if err != nil {
		t.Fatalf("ResolveTarget: %v", err)
	}
	if got != "build" {
		t.Errorf("resolved to %q, want %q", got, "build")
	}
}

// A listing that cannot be taken is not "no such pane": reporting not-found
// would tell a reconciling caller the session is definitely gone, which is the
// one thing §3.2 forbids concluding on doubt.
func TestListingFailurePropagatesRatherThanBecomingNotFound(t *testing.T) {
	cause := backend.Errorf(backend.CodeBackendUnavailable, "no server running")
	_, err := backend.ResolveTarget("%7", backend.PrefixedPaneID, func() ([]backend.Pane, error) { return nil, cause })
	if errors.Is(err, backend.ErrNotFound) {
		t.Error("a listing failure became not-found")
	}
	if !errors.Is(err, backend.ErrUnavailable) {
		t.Errorf("error is %v, want ErrUnavailable", err)
	}
}

// An empty target reaching resolution is a caller bug, and a silent
// pass-through would let it key a write lock — §10's named failure mode.
func TestEmptyTargetIsUsage(t *testing.T) {
	_, err := backend.ResolveTarget("", backend.PrefixedPaneID, lister())
	if !errors.Is(err, backend.ErrUsage) {
		t.Errorf("error is %v, want ErrUsage", err)
	}
}

// §10: on zmx there is no pane-id concept, so a %-prefixed target is an
// ordinary unknown session name. Passing no lister is how a backend says that,
// and it must not crash.
func TestBackendWithoutPaneIDsPassesEverythingThrough(t *testing.T) {
	got, err := backend.ResolveTarget("%7", backend.PrefixedPaneID, nil)
	if err != nil {
		t.Fatalf("ResolveTarget: %v", err)
	}
	if got != "%7" {
		t.Errorf("resolved to %q, want it unchanged", got)
	}
}
