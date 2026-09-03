package olympus

import (
	"context"
	"testing"

	"github.com/husniadil/olympus/backend"
)

// §8.10 Only a backend whose session client follows a server-side focus has
// anything to steer. The fake implements no Focuser, so the refusal fires on
// the interface, before any call.
func TestFocusIsUnsupportedWhereClientsSelectTheirOwnView(t *testing.T) {
	ol := fakeOlympus(&fakeBackend{caps: backend.Capabilities{Backend: backend.Zmx}})
	err := ol.Focus(context.Background(), "build")
	if backend.CodeOf(err) != backend.CodeUnsupported {
		t.Errorf("focus on a backend with no server-side focus is %q, want %q (err %v)", backend.CodeOf(err), backend.CodeUnsupported, err)
	}
}
