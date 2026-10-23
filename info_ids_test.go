package olympus

import (
	"context"
	"testing"

	"github.com/husniadil/olympus/backend"
)

// Two herdr workspaces may share a label. The session row Info answers with
// is the one the panes name by id, never the first row with the same name.
func TestInfoFindsTheSessionByThePanesIDWhereNamesRepeat(t *testing.T) {
	f := &fakeBackend{
		caps: backend.Capabilities{Backend: backend.Herdr},
		sessions: []backend.Session{
			{Name: "demo", ID: "w1", CWD: "/root"},
			{Name: "demo", ID: "w2", CWD: "/tmp"},
		},
		panes: []backend.Pane{{ID: "w2:p1", SessionName: "demo", SessionID: "w2"}},
	}
	got, err := fakeOlympus(f).Info(context.Background(), "w2")
	if err != nil {
		t.Fatalf("Info: %v", err)
	}
	if got.Session == nil || got.Session.ID != "w2" || got.Session.CWD != "/tmp" {
		t.Fatalf("Info paired w2's panes with session %+v", got.Session)
	}
}

// A pane row with no session id still finds its session by name.
func TestInfoFallsBackToTheNameWithoutAnID(t *testing.T) {
	f := &fakeBackend{
		caps:     backend.Capabilities{Backend: backend.Herdr},
		sessions: []backend.Session{{Name: "demo", ID: "w1"}},
		panes:    []backend.Pane{{ID: "w1:p1", SessionName: "demo"}},
	}
	got, err := fakeOlympus(f).Info(context.Background(), "w1")
	if err != nil {
		t.Fatalf("Info: %v", err)
	}
	if got.Session == nil || got.Session.ID != "w1" {
		t.Fatalf("Info found session %+v", got.Session)
	}
}
