package meja

import (
	"context"
	"strconv"

	"github.com/husniadil/olympus/backend"
)

// Screen captures a session's visible screen, and optionally its scrollback.
func (m *Meja) Screen(ctx context.Context, target string, opts backend.ScreenOpts) (backend.Capture, error) {
	args := []string{"capture-pane", "-p", "-t", target}
	if opts.Colors {
		args = append(args, "-e")
	}
	if opts.HistoryLines > 0 {
		// -J is dropped whenever history is requested, for the same reason as
		// on tmux: joining is right on the live viewport, where nothing has
		// been re-flowed since, but across scrollback it merges a long line
		// with its own historical continuation into one line that never
		// appeared on screen (§5.1).
		args = append(args, "-S", "-"+strconv.Itoa(opts.HistoryLines))
	} else {
		args = append(args, "-J")
	}

	out, err := m.run(ctx, nil, args...)
	if err != nil {
		return backend.Capture{}, named(target, err)
	}
	return backend.Capture{Text: out}, nil
}

// ScreenMeta reports capture metadata without capturing.
//
// meja exposes no format field for the alternate screen, so the flag is always
// its zero value here — which is exactly why this backend declares
// TracksAltScreen false rather than letting a caller read the zero value as
// "not on the alternate screen" (§5.3).
func (m *Meja) ScreenMeta(ctx context.Context, target string) (backend.ScreenMeta, error) {
	if state := m.Probe(ctx, target); state != backend.StatePresent {
		if state == backend.StateAbsent {
			return backend.ScreenMeta{}, backend.Errorf(backend.CodeSessionNotFound, "no session %s", target)
		}
		return backend.ScreenMeta{}, backend.Errorf(backend.CodeBackendUnavailable, "cannot reach meja for %s", target)
	}
	return backend.ScreenMeta{}, nil
}
