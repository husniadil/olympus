package olympus

import (
	"context"
	"testing"

	"github.com/husniadil/olympus/backend"
)

// §2.11 Where names are fixed at creation there is nothing to rename. The fake
// implements no Renamer, so the refusal fires before any call.
func TestRenameIsUnsupportedWhereNamesAreFixed(t *testing.T) {
	ol := fakeOlympus(&fakeBackend{caps: backend.Capabilities{Backend: backend.Zmx}})
	err := ol.Rename(context.Background(), "build", "other")
	if backend.CodeOf(err) != backend.CodeUnsupported {
		t.Errorf("rename on a backend with fixed names is %q, want %q (err %v)", backend.CodeOf(err), backend.CodeUnsupported, err)
	}
}
