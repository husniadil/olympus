package engine_test

import (
	"context"
	"errors"
	"testing"

	"github.com/husniadil/olympus/backend"
	"github.com/husniadil/olympus/internal/engine"
)

func spec(name string) backend.CreateSpec {
	return backend.CreateSpec{Name: name, Dir: "/repo", Cols: 80, Rows: 24}
}

func TestEnsureCreatesAnAbsentSession(t *testing.T) {
	f := &fakeBackend{}
	got, err := engine.Ensure(context.Background(), f, spec("build"))
	if err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	if got.Outcome != backend.OutcomeCreated {
		t.Errorf("outcome %q, want %q", got.Outcome, backend.OutcomeCreated)
	}
	if len(f.created) != 1 {
		t.Errorf("created %d sessions, want 1", len(f.created))
	}
}

// Options other than the name are ignored on a live session, and are NOT
// applied retroactively: they belong to the create path.
func TestEnsureReusesALiveSessionAndIgnoresOptions(t *testing.T) {
	f := &fakeBackend{
		sessions: []backend.Session{{Name: "build", ID: "$1", Liveness: backend.LivenessPresent, CWD: "/original"}},
	}
	changed := spec("build")
	changed.Dir = "/somewhere-else"

	got, err := engine.Ensure(context.Background(), f, changed)
	if err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	if got.Outcome != backend.OutcomeReused {
		t.Errorf("outcome %q, want %q", got.Outcome, backend.OutcomeReused)
	}
	if len(f.created) != 0 {
		t.Errorf("a live session was recreated")
	}
	if got.CWD != "/original" {
		t.Errorf("cwd %q, want the existing session's %q — options must not apply retroactively", got.CWD, "/original")
	}
}

// Unreachable on both shipped backends, and implemented anyway so a backend
// that does leave dead rows reaps rather than handing back a corpse.
func TestEnsureReapsAPresentButDeadSession(t *testing.T) {
	f := &fakeBackend{
		sessions: []backend.Session{{Name: "build", Dead: true, Liveness: backend.LivenessPresent}},
	}
	got, err := engine.Ensure(context.Background(), f, spec("build"))
	if err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	if got.Outcome != backend.OutcomeReaped {
		t.Errorf("outcome %q, want %q", got.Outcome, backend.OutcomeReaped)
	}
	if len(f.kills) != 1 {
		t.Errorf("killed %d times, want 1 — the corpse was not reaped", len(f.kills))
	}
	if len(f.created) != 1 {
		t.Errorf("created %d sessions, want 1", len(f.created))
	}
}

// §2.7's exact failure mode: if the rejection lived inside create only, a fresh
// name would correctly reject while an already-alive session took the reuse
// branch, never reached create, and silently accepted and ignored the flag.
// The contract would then depend on session state.
func TestTheCorpseFlagIsRejectedBeforeBranchingOnState(t *testing.T) {
	unsupported := backend.Capabilities{Backend: backend.Zmx, RemainOnExit: false}

	absent := &fakeBackend{caps: unsupported}
	withSpec := spec("build")
	withSpec.RemainOnExit = true
	if _, err := engine.Ensure(context.Background(), absent, withSpec); !errors.Is(err, backend.ErrUnsupported) {
		t.Errorf("against an absent name: error is %v, want unsupported", err)
	}

	alive := &fakeBackend{
		caps:     unsupported,
		sessions: []backend.Session{{Name: "build", Liveness: backend.LivenessPresent}},
	}
	if _, err := engine.Ensure(context.Background(), alive, withSpec); !errors.Is(err, backend.ErrUnsupported) {
		t.Errorf("against a live session: error is %v, want unsupported — the reuse branch swallowed the flag", err)
	}
	if len(alive.created) != 0 {
		t.Error("the rejected ensure still created a session")
	}
}

// A backend that supports it must accept it, or the capability is a lie.
func TestTheCorpseFlagIsAcceptedWhereItIsSupported(t *testing.T) {
	f := &fakeBackend{caps: backend.Capabilities{Backend: backend.Tmux, RemainOnExit: true}}
	withSpec := spec("build")
	withSpec.RemainOnExit = true
	if _, err := engine.Ensure(context.Background(), f, withSpec); err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	if len(f.created) != 1 || !f.created[0].RemainOnExit {
		t.Error("the flag did not reach the create path")
	}
}
