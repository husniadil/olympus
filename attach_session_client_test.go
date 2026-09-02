//go:build darwin || linux

package olympus

import (
	"context"
	"testing"

	"github.com/husniadil/olympus/backend"
)

// Only herdr has a separate session client; asking for one on any other backend
// is a caller mistake, refused as unsupported before anything is spawned. The
// guard fires on the spec, so the nil terminal files are never reached.
func TestSessionClientAttachIsRefusedOnNonHerdrBackend(t *testing.T) {
	ol := fakeOlympus(&fakeBackend{caps: backend.Capabilities{Backend: backend.Tmux}})
	s := ol.OpenSessionName("whatever")

	_, err := s.Attach(context.Background(), nil, nil, nil, WithSessionClient())
	if err == nil {
		t.Fatal("a session-client attach on tmux succeeded")
	}
	if backend.CodeOf(err) != backend.CodeUnsupported {
		t.Errorf("session-client attach on tmux is %q, want %q", backend.CodeOf(err), backend.CodeUnsupported)
	}
}
