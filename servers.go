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
