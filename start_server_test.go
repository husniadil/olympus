package olympus

import (
	"context"
	"strings"
	"testing"

	"github.com/husniadil/olympus/backend"
)

// A backend that can list its servers and start one, recording what it was
// asked to start. The recording is the point: §13.4's rule about a server that
// already answers is a rule about NOT calling this.
type startingBackend struct {
	*fakeBackend
	servers []backend.Server
	started []backend.Server
}

func (b *startingBackend) Servers(context.Context) ([]backend.Server, error) {
	return b.servers, nil
}

func (b *startingBackend) StartServer(_ context.Context, s backend.Server) error {
	b.started = append(b.started, s)
	return nil
}

// A backend that lists servers and cannot start one.
type listOnlyBackend struct {
	*fakeBackend
	servers []backend.Server
}

func (b *listOnlyBackend) Servers(context.Context) ([]backend.Server, error) {
	return b.servers, nil
}

func startable(t *testing.T, servers []backend.Server) (*Olympus, *startingBackend) {
	t.Helper()
	b := &startingBackend{
		fakeBackend: &fakeBackend{caps: backend.Capabilities{Backend: backend.Herdr}},
		servers:     servers,
	}
	return &Olympus{backend: b, resolution: Resolution{Backend: backend.Herdr, Reason: ReasonFlag}}, b
}

// §13.4 A server can be told to come up. With no name it is the backend's
// default row, and what comes back says which happened.
func TestStartServerStartsTheDefaultRowWhenNothingNamesOne(t *testing.T) {
	o, b := startable(t, []backend.Server{
		{Name: "work", SocketPath: "/s/work"},
		{Name: "default", SocketPath: "/s/default", Default: true},
	})

	started, err := o.StartServer(context.Background(), "")
	if err != nil {
		t.Fatalf("StartServer: %v", err)
	}
	if started.Name != "default" || started.Outcome != "started" {
		t.Errorf("started %+v, want the default row started", started)
	}
	if len(b.started) != 1 || b.started[0].SocketPath != "/s/default" {
		t.Fatalf("asked the backend to start %+v, want the default row", b.started)
	}
	// The row goes down, not the name alone: starting one needs the socket to
	// wait on and whether it is the backend's own default.
	if !b.started[0].Default {
		t.Errorf("the row handed down lost its default flag: %+v", b.started[0])
	}
}

// §13.4 A server that already answers MUST be left alone, and is reported
// running rather than refused.
func TestStartServerLeavesARunningServerAlone(t *testing.T) {
	o, b := startable(t, []backend.Server{{Name: "default", Default: true, Running: true}})

	started, err := o.StartServer(context.Background(), "default")
	if err != nil {
		t.Fatalf("StartServer: %v", err)
	}
	if started.Outcome != "running" {
		t.Errorf("outcome %q, want running", started.Outcome)
	}
	if len(b.started) != 0 {
		t.Errorf("a running server was started anyway: %+v", b.started)
	}
}

// §13.4 A name the listing does not hold is not-found, and so is asking for a
// default row on a backend that reports none.
func TestStartServerRefusesANameItCannotFind(t *testing.T) {
	o, b := startable(t, []backend.Server{{Name: "default", Default: true}})

	if _, err := o.StartServer(context.Background(), "nope"); err == nil {
		t.Fatal("starting an unknown name should fail")
	} else if code := backend.CodeOf(err); code != backend.CodeSessionNotFound {
		t.Errorf("code %v, want %v: %v", code, backend.CodeSessionNotFound, err)
	}

	o2, _ := startable(t, []backend.Server{{Name: "work"}})
	_, err := o2.StartServer(context.Background(), "")
	if err == nil {
		t.Fatal("starting a default that does not exist should fail")
	}
	if !strings.Contains(err.Error(), "no default server") {
		t.Errorf("the refusal does not say what was missing: %v", err)
	}
	if len(b.started) != 0 {
		t.Errorf("a refusal started something: %+v", b.started)
	}
}

// §13.4 A backend whose server has no independent existence answers
// unsupported, the way tmux and zmx do: theirs come up with a session.
func TestStartServerIsUnsupportedWhereABackendCannotStartOne(t *testing.T) {
	b := &listOnlyBackend{
		fakeBackend: &fakeBackend{caps: backend.Capabilities{Backend: backend.Tmux}},
		servers:     []backend.Server{{Name: "default", Default: true}},
	}
	o := &Olympus{backend: b, resolution: Resolution{Backend: backend.Tmux, Reason: ReasonFlag}}

	_, err := o.StartServer(context.Background(), "")
	if err == nil {
		t.Fatal("a backend that cannot start a server should refuse")
	}
	if code := backend.CodeOf(err); code != backend.CodeUnsupported {
		t.Errorf("code %v, want %v: %v", code, backend.CodeUnsupported, err)
	}
}
