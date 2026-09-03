package olympus

import (
	"context"
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
