package tmux

import "testing"

// §9.6: a cell selects the pane whose rectangle contains it, edges inclusive,
// and a border or a cell outside the window selects nothing.
//
// The rectangles are the ones tmux reports for an 80x24 window split in two:
// column 40 is the border between them.
func TestPaneAtSelectsByInclusiveRectangle(t *testing.T) {
	panes := []paneRect{
		{id: "%0", left: 0, top: 0, right: 39, bottom: 23},
		{id: "%1", left: 41, top: 0, right: 79, bottom: 23},
	}
	cases := []struct {
		col, row int
		want     string
	}{
		{0, 0, "%0"},
		{39, 23, "%0"},
		{40, 5, ""}, // the border
		{41, 0, "%1"},
		{79, 23, "%1"},
		{80, 0, ""},  // past the right edge
		{10, 24, ""}, // below the bottom
	}
	for _, c := range cases {
		if got := paneAt(panes, c.col, c.row); got != c.want {
			t.Errorf("paneAt(%d, %d) = %q, want %q", c.col, c.row, got, c.want)
		}
	}
	if got := paneAt(nil, 0, 0); got != "" {
		t.Errorf("paneAt with no panes = %q, want empty", got)
	}
}
