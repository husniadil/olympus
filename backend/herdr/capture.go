package herdr

import (
	"context"
	"strconv"

	"github.com/husniadil/olympus/backend"
)

// readLineCap is the deepest history herdr will return.
//
// A larger request is not an error: the server clamps it silently
// (src/app/api_helpers.rs:117). Clamping here too keeps the number Olympus
// asked for and the number it could have received the same, so a caller reading
// this code is not told one thing and given another.
const readLineCap = 1000

// Screen captures one target (§5.1, §5.2).
func (h *Herdr) Screen(ctx context.Context, target string, opts backend.ScreenOpts) (backend.Capture, error) {
	row, err := h.resolvePane(ctx, target)
	if err != nil {
		return backend.Capture{}, err
	}

	args := []string{"pane", "read", row.PaneID}
	if opts.HistoryLines > 0 {
		// "recent" is the visible screen with scrollback above it, to the
		// requested depth. It is not the default here because the default has
		// a depth of its own — 80 lines — and a caller who asked for the
		// visible screen would silently get history they did not want.
		lines := opts.HistoryLines
		if lines > readLineCap {
			lines = readLineCap
		}
		args = append(args, "--source", "recent", "--lines", strconv.Itoa(lines))
	} else {
		args = append(args, "--source", "visible")
	}
	if opts.Colors {
		// Not a no-op: the default read strips every ANSI escape byte, and
		// this preserves them.
		args = append(args, "--format", "ansi")
	} else {
		args = append(args, "--format", "text")
	}

	out, err := h.run(ctx, args...)
	if err != nil {
		return backend.Capture{}, err
	}
	// Built from the row this capture already resolved through, rather than by
	// calling ScreenMeta: the metadata comes out of the pane listing, and
	// asking for it again would put a second subprocess on a path callers poll
	// (§5).
	return backend.Capture{Text: out, Meta: metaOf(row)}, nil
}

// ScreenMeta reports capture metadata without capturing (§5.5).
//
// The alt-screen flag is always false and no request is made to check it: the
// terminal tracks the alternate screen internally
// (src/terminal/runtime.rs:343-346) but nothing in the socket API reports it.
// That is an honest answer — "not tracked" — rather than an unsupported-class
// error, and the tracks_alt_screen capability is what tells a caller the false
// is a declaration and not an observation (§5.3, §13).
//
// The scroll position IS real, unlike on zmx and meja: a pane row carries how
// far its viewport has been scrolled up from the live bottom, which is the same
// quantity §5.5 defines.
func (h *Herdr) ScreenMeta(ctx context.Context, target string) (backend.ScreenMeta, error) {
	row, err := h.resolvePane(ctx, target)
	if err != nil {
		return backend.ScreenMeta{}, err
	}
	return metaOf(row), nil
}

func metaOf(row paneRow) backend.ScreenMeta {
	meta := backend.ScreenMeta{}
	if row.Scroll != nil {
		meta.ScrollPosition = row.Scroll.OffsetFromBottom
	}
	return meta
}
