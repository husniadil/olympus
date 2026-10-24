package herdr

import "testing"

// §6.4 herdr's cap: the server counts its 1,000 lines from the bottom of the
// grid, the screen included, so a depth of history is asked for with the
// viewport added, and a request the cap cuts says how much history is left.
// The cut is judged on history plus viewport: a depth under 1,000 is still cut
// when the screen takes it past.
func TestAHistoryRequestIsCutWithTheScreenCounted(t *testing.T) {
	for _, c := range []struct {
		depth, viewport, lines, history int
		cut                             bool
	}{
		{depth: 200, viewport: 40, lines: 240},
		{depth: 960, viewport: 40, lines: 1000},
		{depth: 961, viewport: 40, lines: 1000, history: 960, cut: true},
		{depth: 990, viewport: 50, lines: 1000, history: 950, cut: true},
		{depth: 10000, viewport: 24, lines: 1000, history: 976, cut: true},
		// No viewport known: the depth is asked for as it is.
		{depth: 1200, lines: 1000, history: 1000, cut: true},
	} {
		lines, history, cut := historyRequest(c.depth, c.viewport)
		if lines != c.lines || history != c.history || cut != c.cut {
			t.Errorf("depth %d over %d rows = (%d lines, %d history, cut %v), want (%d, %d, %v)",
				c.depth, c.viewport, lines, history, cut, c.lines, c.history, c.cut)
		}
	}
}
