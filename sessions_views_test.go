package olympus

import (
	"context"
	"strings"
	"testing"

	"github.com/husniadil/olympus/backend"
)

// A view is a session to the multiplexer, but not to a caller listing what it
// can address (behavior §9.5): the reserved name shape keeps it out of ls.
func TestSessionsLeaveViewsOut(t *testing.T) {
	f := &fakeBackend{sessions: []backend.Session{
		{Name: "work", ID: "$1"},
		{Name: viewName("work"), ID: "$2"},
		{Name: "olympus-view-work-deadbeef", ID: "$3"},
		{Name: "olympus-viewer", ID: "$4"},
	}}
	got, err := fakeOlympus(f).Sessions(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, s := range got {
		names = append(names, s.Name)
	}
	if len(names) != 2 || names[0] != "work" || names[1] != "olympus-viewer" {
		t.Fatalf("ls listed %v; want [work olympus-viewer] (views hidden, a look-alike kept)", names)
	}
}

// §0.8: a session listing on a herdr that keeps one view for every client
// owes the caller what its `focused` flag means there, once for the listing.
// A listing with no row focused, every other backend's and a newer herdr's,
// owes nothing.
func TestASharedFocusIsDisclosedOnTheSessionListing(t *testing.T) {
	ol := fakeOlympus(&fakeBackend{caps: backend.Capabilities{Backend: backend.Herdr}})
	shared := []backend.Session{{Name: "a", Focused: true}, {Name: "b"}}
	if got := ol.SessionWarnings(shared); len(got) != 1 || !strings.Contains(got[0].Message, "every client") {
		t.Errorf("warnings = %v, want one saying focused is what every client shows", got)
	}
	if got := ol.SessionWarnings([]backend.Session{{Name: "a"}, {Name: "b"}}); got != nil {
		t.Errorf("warnings = %v, want none where no row is focused", got)
	}
}
