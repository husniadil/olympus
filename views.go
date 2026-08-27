package olympus

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"

	"github.com/husniadil/olympus/backend"
)

// viewPrefix is reserved (behavior §17.1).
//
// It is load-bearing beyond cosmetics: enumerating views selects on it, so
// changing it orphans every view created by an older binary.
const viewPrefix = "olympus-view-"

// viewName builds the reserved name for a new view.
//
// Generated HERE rather than in a backend, so the reserved shape lives in one
// place and every backend produces names an older or newer binary can still
// recognise.
func viewName(base string) string {
	var nonce [4]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		nonce = [4]byte{}
	}
	return fmt.Sprintf("%s%s-%s", viewPrefix, base, hex.EncodeToString(nonce[:]))
}

// A ViewOption configures CreateView.
type ViewOption func(*backend.ViewSpec)

// WithoutMouse creates a view whose wheel does not scroll into history.
//
// Mouse is on by default because a view is for reading and a wheel that scrolls
// is the point of one — but it is per-view, since on another it is an unwanted
// mode change.
func WithoutMouse() ViewOption {
	return func(s *backend.ViewSpec) { s.Mouse = false }
}

// WithViewName sets the view's name instead of generating one. The reserved
// prefix is still required: enumerating views selects on it.
func WithViewName(name string) ViewOption {
	return func(s *backend.ViewSpec) { s.Name = name }
}

// CreateView adds an independently-scrollable view onto an existing session.
//
// A backend with no view concept answers unsupported — distinct from
// unavailable, and distinct from an empty result. Feature-probe Capabilities
// rather than branching on the error.
//
// On a backend that does support views this is NOT a side-effect-free read: it
// defines a server-global key table (behavior §9.3). That table is inert to
// every session not pointing at it, so it does not change what an operator's
// own sessions do on a server Olympus is merely aimed at.
func (o *Olympus) CreateView(ctx context.Context, base string, opts ...ViewOption) (backend.View, error) {
	resolved, err := o.resolveTarget(ctx, base)
	if err != nil {
		return backend.View{}, err
	}
	spec := backend.ViewSpec{Name: viewName(resolved), Mouse: true}
	for _, opt := range opts {
		opt(&spec)
	}
	return o.backend.CreateView(ctx, resolved, spec)
}

// ScrollView moves a view back into its history, leaving its base untouched.
//
// The view is resolved like every other target (behavior §10). A view is a
// session, so a caller holding its pane id can address it the way they address
// any other — and this was the one target method that handed its argument
// straight to the backend instead.
func (o *Olympus) ScrollView(ctx context.Context, view string, lines int) error {
	resolved, err := o.resolveTarget(ctx, view)
	if err != nil {
		return err
	}
	return o.backend.ScrollView(ctx, resolved, lines)
}

// Views lists views, for one base or for every session when base is empty.
func (o *Olympus) Views(ctx context.Context, base string) ([]backend.View, error) {
	if base == "" {
		return o.backend.Views(ctx, "")
	}
	resolved, err := o.resolveTarget(ctx, base)
	if err != nil {
		return nil, err
	}
	return o.backend.Views(ctx, resolved)
}

// ServerEnv reads a key from the multiplexer server's global environment.
//
// An unset key is a real negative answer — present false, no error — and is not
// the same as a backend with no such concept, which is unsupported
// (behavior §12).
func (o *Olympus) ServerEnv(ctx context.Context, key string) (string, bool, error) {
	return o.backend.ServerEnv(ctx, key)
}
