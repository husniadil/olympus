package zmx_test

import (
	"context"
	"strings"
	"testing"

	"github.com/husniadil/olympus/backend"
	"github.com/husniadil/olympus/backend/zmx"
)

// §4.3: zmx drops input that would take its pending queue past 256 KiB, with
// nothing said, so a write that large is refused as the caller's input before
// anything is run. The directory names no daemon: a refusal that ran zmx
// first would answer not-found instead.
func TestInputPastZmxsCapIsUsageBeforeAnythingRuns(t *testing.T) {
	b := zmx.New(zmx.WithDir(t.TempDir()))
	ctx := context.Background()
	const max = 256 << 10
	for _, c := range []struct {
		op  string
		err error
	}{
		{"type", b.Type(ctx, "oly-cap", strings.Repeat("x", max+1))},
		// A paste counts its bracket markers, framed or not.
		{"paste", b.Paste(ctx, "oly-cap", strings.Repeat("x", max-11))},
		// An atomic submit counts its terminator.
		{"atomic submit", b.SendAtomic(ctx, "oly-cap", strings.Repeat("x", max))},
	} {
		if backend.CodeOf(c.err) != backend.CodeUsage {
			t.Errorf("%s past the cap is %v, want %s", c.op, c.err, backend.CodeUsage)
			continue
		}
		if !strings.Contains(c.err.Error(), "262144") {
			t.Errorf("%s: the refusal %q does not state the limit", c.op, c.err)
		}
	}
	// At the cap itself nothing is refused, so the call goes on to zmx. A
	// context already done keeps zmx from running at all.
	done, cancel := context.WithCancel(ctx)
	cancel()
	if err := b.Type(done, "oly-cap", strings.Repeat("x", max)); backend.CodeOf(err) == backend.CodeUsage {
		t.Errorf("a text at the cap was refused: %v", err)
	}
}
