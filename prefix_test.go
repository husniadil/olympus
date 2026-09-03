package olympus

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/husniadil/olympus/backend"
	"github.com/husniadil/olympus/backend/meja"
)

// §13.3 meja's prefix is fixed by the program and documented as such, so the
// backend reports the constant rather than nothing.
func TestMejaReportsItsFixedPrefix(t *testing.T) {
	var b backend.Backend = meja.New()
	_ = b
	r, ok := b.(backend.PrefixReporter)
	if !ok {
		t.Fatal("meja does not report a prefix")
	}
	if v, err := r.Prefix(context.Background()); err != nil || v != "C-b" {
		t.Errorf("meja prefix = %q, %v; want C-b", v, err)
	}
}

// The present shape has its own marshaller, and it must carry the prefix: the
// first cut dropped it there while the struct field was set.
func TestInfoPresentShapeCarriesThePrefix(t *testing.T) {
	out, err := json.Marshal(Info{State: backend.StatePresent, Prefix: "C-b"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), `"prefix":"C-b"`) {
		t.Errorf("present info marshals without its prefix: %s", out)
	}
}

// A backend with no prefix — the fake, like zmx — leaves info's prefix out.
func TestInfoOmitsThePrefixWhereThereIsNone(t *testing.T) {
	ol := fakeOlympus(&fakeBackend{caps: backend.Capabilities{Backend: backend.Zmx},
		sessions: []backend.Session{{Name: "build", Liveness: backend.LivenessPresent}}})
	info, err := ol.Info(context.Background(), "build")
	if err != nil {
		t.Fatalf("Info: %v", err)
	}
	if info.Prefix != "" {
		t.Errorf("info reports prefix %q on a backend with none", info.Prefix)
	}
}
