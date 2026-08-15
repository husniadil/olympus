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

// CreateView adds an independently-scrollable view onto an existing session.
//
// A backend with no view concept answers unsupported — distinct from
// unavailable, and distinct from an empty result. Feature-probe Capabilities
// rather than branching on the error.
func (o *Olympus) CreateView(ctx context.Context, base string) (backend.View, error) {
	resolved, err := o.resolveTarget(ctx, base)
	if err != nil {
		return backend.View{}, err
	}
	return o.backend.CreateView(ctx, resolved, viewName(resolved))
}

// ScrollView moves a view back into its history, leaving its base untouched.
func (o *Olympus) ScrollView(ctx context.Context, view string, lines int) error {
	return o.backend.ScrollView(ctx, view, lines)
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
