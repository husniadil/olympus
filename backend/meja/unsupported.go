package meja

import (
	"context"

	"github.com/husniadil/olympus/backend"
)

// The operations meja has no mechanism for. Each refuses with UNSUPPORTED, and
// each is paired with a capability so a caller can feature-probe instead of
// branching on the error (§13).

// SetStatus is unsupported: meja has no option store of any kind — no
// set-option, no show-options — so there is nowhere a status could outlive the
// process that set it (§13.1).
func (m *Meja) SetStatus(ctx context.Context, target, status string) error {
	return backend.Errorf(backend.CodeUnsupported, "meja cannot carry a session status")
}

// Status refuses for the same reason. Refusing the read as well as the write is
// required: answering empty would be indistinguishable from a session that has
// simply not reported yet (§13.1).
func (m *Meja) Status(ctx context.Context, target string) (string, error) {
	return "", backend.Errorf(backend.CodeUnsupported, "meja cannot carry a session status")
}

// CreateView is unsupported. meja does have grouped sessions — new -d -t base
// -s mirror — but no read-only client, no key table to bind scrolling into, and
// no option store to make one passive with. A window a stray keypress can drive
// is not the read-only view §9 specifies, and offering it under that name would
// be worse than not offering it.
func (m *Meja) CreateView(ctx context.Context, base string, spec backend.ViewSpec) (backend.View, error) {
	return backend.View{}, backend.Errorf(backend.CodeUnsupported, "meja has no read-only views")
}

func (m *Meja) ScrollView(ctx context.Context, view string, lines int) error {
	return backend.Errorf(backend.CodeUnsupported, "meja has no read-only views")
}

func (m *Meja) Views(ctx context.Context, base string) ([]backend.View, error) {
	return nil, backend.Errorf(backend.CodeUnsupported, "meja has no read-only views")
}

// ServerEnv is unsupported: meja has no set-environment or show-environment.
func (m *Meja) ServerEnv(ctx context.Context, key string) (string, bool, error) {
	return "", false, backend.Errorf(backend.CodeUnsupported, "meja has no server environment")
}
