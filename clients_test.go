package olympus

import (
	"context"
	"testing"

	"github.com/husniadil/olympus/backend"
)

// A clientsBackend is the fake with a client listing, for the rules this
// layer puts around one.
type clientsBackend struct {
	*fakeBackend
	clients []backend.Client
	err     error
	asked   int
}

func (c *clientsBackend) Clients(context.Context) ([]backend.Client, error) {
	c.asked++
	return c.clients, c.err
}

func clientsOlympus(c *clientsBackend) *Olympus {
	return &Olympus{backend: c, resolution: Resolution{Backend: backend.Herdr, Reason: ReasonFlag}}
}

// §13.5 A backend with no client listing answers unsupported, distinct from
// an empty list, which would claim there are no clients.
func TestClientsIsUnsupportedWhereTheBackendCannotListThem(t *testing.T) {
	ol := fakeOlympus(&fakeBackend{caps: backend.Capabilities{Backend: backend.Tmux}})
	if _, err := ol.Clients(context.Background()); backend.CodeOf(err) != backend.CodeUnsupported {
		t.Errorf("Clients on a backend without a listing is %q (%v), want %q", backend.CodeOf(err), err, backend.CodeUnsupported)
	}
}

// §13.5, api §2 An empty listing is [], never null.
func TestAnEmptyClientListingIsNotNil(t *testing.T) {
	ol := clientsOlympus(&clientsBackend{fakeBackend: &fakeBackend{}})
	got, err := ol.Clients(context.Background())
	if err != nil || got == nil || len(got) != 0 {
		t.Errorf("Clients = %#v, %v; want an empty, non-nil slice", got, err)
	}
}

// §13.5 A tag filter answers the one client carrying it, as a listing of
// one, and not-found where no client carries it: a caller asking where its
// own client is must be able to tell "not there" from "there, showing
// nothing". A tag no server would accept is usage, before the server is
// asked.
func TestAClientTagFilterAnswersThatClientOrNotFound(t *testing.T) {
	ctx := context.Background()
	fake := &clientsBackend{fakeBackend: &fakeBackend{}, clients: []backend.Client{
		{ID: "1", SessionID: "w1"},
		{ID: "2", Tag: "mine", SessionID: "w2", WindowID: "w2:t1", PaneID: "w2:p3"},
		{ID: "3", Tag: "other", SessionID: "w1"},
	}}
	ol := clientsOlympus(fake)

	got, err := ol.Clients(ctx, WithClientTag("mine"))
	if err != nil || len(got) != 1 || got[0].ID != "2" || got[0].PaneID != "w2:p3" {
		t.Errorf("Clients(WithClientTag(mine)) = %+v, %v; want client 2 alone", got, err)
	}
	if all, err := ol.Clients(ctx); err != nil || len(all) != 3 {
		t.Errorf("Clients() = %+v, %v; want all three", all, err)
	}
	if _, err := ol.Clients(ctx, WithClientTag("nobody")); backend.CodeOf(err) != backend.CodeSessionNotFound {
		t.Errorf("a tag no client carries is %q (%v), want %q", backend.CodeOf(err), err, backend.CodeSessionNotFound)
	}

	before := fake.asked
	if _, err := ol.Clients(ctx, WithClientTag("bad\ttag")); backend.CodeOf(err) != backend.CodeUsage {
		t.Errorf("a tag with a control character is %q (%v), want %q", backend.CodeOf(err), err, backend.CodeUsage)
	}
	if fake.asked != before {
		t.Error("the server was asked for its clients under a tag it could never hold")
	}

	unsupported := clientsOlympus(&clientsBackend{fakeBackend: &fakeBackend{}, err: backend.Errorf(backend.CodeUnsupported, "no")})
	if _, err := unsupported.Clients(ctx, WithClientTag("mine")); backend.CodeOf(err) != backend.CodeUnsupported {
		t.Errorf("a filter on a server that cannot list clients is %q (%v), want %q", backend.CodeOf(err), err, backend.CodeUnsupported)
	}
}
