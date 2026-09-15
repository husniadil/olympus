package olympus

import (
	"context"

	"github.com/husniadil/olympus/backend"
)

// A ClientsOption narrows the client listing.
type ClientsOption func(*clientsOpts)

type clientsOpts struct{ tag string }

// WithClientTag narrows the listing to the client carrying a tag: the one a
// caller gave its own bare attach with BareClientTag. No client carrying it
// is not-found, so a caller can tell "my client is not there" from "it is
// there, showing nothing".
func WithClientTag(tag string) ClientsOption { return func(o *clientsOpts) { o.tag = tag } }

// Clients lists the clients attached to the server this handle addresses, and
// what each shows: the session, window and pane (behavior §13.5).
//
// It answers where a server keeps a view per client and says where each is.
// A backend without a listing, and a server that cannot say (a herdr that
// does not advertise `client_view_focus`), answer unsupported, distinct from
// an empty list. No server running is an empty list.
func (o *Olympus) Clients(ctx context.Context, opts ...ClientsOption) ([]backend.Client, error) {
	var options clientsOpts
	for _, opt := range opts {
		opt(&options)
	}
	if options.tag != "" {
		if err := backend.CheckClientTag(options.tag); err != nil {
			return nil, err
		}
	}
	lister, ok := o.backend.(backend.ClientLister)
	if !ok {
		return nil, backend.Errorf(backend.CodeUnsupported,
			"%s cannot say which client shows what", o.resolution.Backend)
	}
	clients, err := lister.Clients(ctx)
	if err != nil {
		return nil, err
	}
	if options.tag == "" {
		if clients == nil {
			clients = []backend.Client{}
		}
		return clients, nil
	}
	for _, c := range clients {
		if c.Tag == options.tag {
			return []backend.Client{c}, nil
		}
	}
	return nil, backend.Errorf(backend.CodeSessionNotFound,
		"no client tagged %q on this %s server", options.tag, o.resolution.Backend)
}
