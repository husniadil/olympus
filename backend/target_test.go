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

// §10: herdr spells each id segment as a base-32 "public number" over the alphabet
// 123456789ABCDEFGHJKMNPQRSTVWXYZ0: digits only for the first nine
// allocations, letters from the tenth. A predicate that reads digits alone
// stops matching real ids the moment a server has seen its tenth workspace,
// tab or pane. Measured on a private server: the tenth pane is w1:pA and the
// tenth workspace wA, and the workspace counter survives a restart.
func TestIndexedPaneIDAcceptsHerdrsPublicNumbers(t *testing.T) {
	t.Parallel()
	for _, id := range []string{"w1:p2", "w12:p3", "w4Y:p1", "w1:pA", "wA:pB", "w10:p0", "wZZ:pZZ"} {
		if !backend.IndexedPaneID(id) {
			t.Errorf("%q is a herdr pane id and was not recognised as one", id)
		}
	}
	for _, name := range []string{
		"", "w", "w1", "w1:", "w:p1", "w1:p", "w1p1", "work:p1", "w1:pane", "wp:1",
		// Lowercase and the four letters the alphabet omits (I, L, O, U) never
		// appear in an id, so a name carrying one is an ordinary name.
		"wa:p1", "w1:pa", "w1I:p1", "wL:p1", "wO:p1", "w1:pU",
		// A tab id is not a pane id.
		"w1:t2",
	} {
		if backend.IndexedPaneID(name) {
			t.Errorf("%q is not a herdr pane id and was recognised as one", name)
		}
	}
}

// §3.4: a herdr tab number is a public number too, so the tenth tab is "tA".
// A window index read with a decimal parser is 0 from there on, which is a
// plausible wrong answer rather than a loud one.
func TestPublicNumberDecodesHerdrsAlphabet(t *testing.T) {
	t.Parallel()
	for in, want := range map[string]int{"1": 1, "9": 9, "A": 10, "Z": 31, "0": 32, "11": 33, "4Y": 158} {
		got, ok := backend.PublicNumber(in)
		if !ok || got != want {
			t.Errorf("PublicNumber(%q) = %d, %v; want %d, true", in, got, ok, want)
		}
	}
	for _, in := range []string{"", "a", "I", "1I", "-1", "t1"} {
		if got, ok := backend.PublicNumber(in); ok {
			t.Errorf("PublicNumber(%q) = %d, true; want false", in, got)
		}
	}
}
