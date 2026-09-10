package olympus

import (
	"context"

	"github.com/husniadil/olympus/backend"
)

// WithServer selects a server BY NAME for this handle — the level above
// sessions, where every backend can run several, each behind its own socket
// (behavior §13.2). Servers lists the names a backend answers to.
//
// What a name resolves to is backend-local, and this option is the one place
// the resolution is decided: on tmux it is a socket name (`--socket`); on
// herdr it is one of the named sessions `herdr session list` reports, whose
// socket is looked up and addressed WITHOUT redirecting herdr's configuration
// and state; on zmx only "default" exists, since there is one directory; on
// meja it is unsupported, since nothing enumerates its profiles.
//
// It is exclusive with WithSocket, WithSocketPath and WithZmxDir: a name and
// an explicit address are two answers to the same question, and letting one
// win silently would leave a caller on a server they did not mean.
func WithServer(name string) Option {
	return func(c *config) { c.server = name }
}

// Servers lists the resolved backend's servers.
//
// A backend that cannot enumerate them answers unsupported — distinct from
// unavailable, and distinct from an empty list. Feature-probe
// Capabilities.Servers rather than branching on the error.
func (o *Olympus) Servers(ctx context.Context) ([]backend.Server, error) {
	lister, ok := o.backend.(backend.ServerLister)
	if !ok {
		return nil, backend.Errorf(backend.CodeUnsupported,
			"%s cannot enumerate its servers", o.resolution.Backend)
	}
	servers, err := lister.Servers(ctx)
	if servers == nil && err == nil {
		servers = []backend.Server{}
	}
	return servers, err
}

// A StoppedServer reports what stopping a server actually did.
type StoppedServer struct {
	Name string `json:"name"`
	// Outcome is gone (it was not running) or killed. Both are successes,
	// mirroring Stop's vocabulary for a session.
	Outcome string `json:"outcome"`
}

// StopServer stops a server by name, with every session on it.
//
// The name is checked against the listing first, so an unknown name is
// not-found rather than whatever the multiplexer prints, and a server that is
// not running is reported gone without being told anything — the same
// idempotence Stop gives a session (behavior §2.8).
func (o *Olympus) StopServer(ctx context.Context, name string) (StoppedServer, error) {
	stopper, ok := o.backend.(backend.ServerStopper)
	if !ok {
		return StoppedServer{}, backend.Errorf(backend.CodeUnsupported,
			"%s cannot stop a server; stop its sessions instead", o.resolution.Backend)
	}
	servers, err := o.Servers(ctx)
	if err != nil {
		return StoppedServer{}, err
	}
	var found *backend.Server
	for i := range servers {
		if servers[i].Name == name {
			found = &servers[i]
			break
		}
	}
	if found == nil {
		return StoppedServer{}, backend.Errorf(backend.CodeSessionNotFound,
			"no %s server named %s; `olympus servers` lists the ones there are", o.resolution.Backend, name)
	}
	if !found.Running {
		return StoppedServer{Name: name, Outcome: "gone"}, nil
	}
	if err := stopper.StopServer(ctx, name); err != nil {
		return StoppedServer{}, err
	}
	return StoppedServer{Name: name, Outcome: "killed"}, nil
}

// A StartedServer reports what starting a server actually did.
type StartedServer struct {
	Name string `json:"name"`
	// Outcome is running (it was already up and was left alone) or started.
	// Both are successes, mirroring StopServer's vocabulary.
	Outcome string `json:"outcome"`
}

// StartServer brings one server up, without creating a session on it. An empty
// name means the backend's default row.
//
// It exists for the machine that has just come back: a backend that restores
// what it was running does that when its server boots, and every other verb
// here refuses to boot one on purpose — a listing that started what it was
// asked to list would answer with a thing it had made (§13.4). This is the one
// verb that says come up, and it says only that.
//
// A server that already answers is reported running and is left entirely
// alone: not restarted, not reconfigured, not claimed.
func (o *Olympus) StartServer(ctx context.Context, name string) (StartedServer, error) {
	starter, ok := o.backend.(backend.ServerStarter)
	if !ok {
		return StartedServer{}, backend.Errorf(backend.CodeUnsupported,
			"%s cannot start a server; create a session on it instead", o.resolution.Backend)
	}
	servers, err := o.Servers(ctx)
	if err != nil {
		return StartedServer{}, err
	}
	var found *backend.Server
	for i := range servers {
		if (name == "" && servers[i].Default) || (name != "" && servers[i].Name == name) {
			found = &servers[i]
			break
		}
	}
	if found == nil {
		if name == "" {
			return StartedServer{}, backend.Errorf(backend.CodeSessionNotFound,
				"%s reports no default server to start; name one, as `olympus servers` lists them",
				o.resolution.Backend)
		}
		return StartedServer{}, backend.Errorf(backend.CodeSessionNotFound,
			"no %s server named %s; `olympus servers` lists the ones there are", o.resolution.Backend, name)
	}
	if found.Running {
		return StartedServer{Name: found.Name, Outcome: "running"}, nil
	}
	if err := starter.StartServer(ctx, *found); err != nil {
		return StartedServer{}, err
	}
	return StartedServer{Name: found.Name, Outcome: "started"}, nil
}
