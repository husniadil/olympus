package herdr

import (
	"context"
	"strings"

	"github.com/husniadil/olympus/backend"
)

// Type injects literal text. It never submits (§4.3).
//
// herdr's text injection writes the bytes straight into the pane's PTY with no
// framing and no terminator (src/app/api/panes.rs:1511-1516), which is exactly
// the primitive this needs.
func (h *Herdr) Type(ctx context.Context, target, text string) error {
	row, err := h.resolvePane(ctx, target)
	if err != nil {
		return err
	}
	return h.send(ctx, row.PaneID, text)
}

// Paste injects multi-line text without submitting it (§4.6).
//
// It is the same raw write as Type. herdr has a bracketed-paste-framing
// injection — the one `pane run` uses — but it is not reachable without the
// Enter it appends, so there is no way to lay bracketed text in the input line
// and leave it there. The cross-backend guarantee still holds, and the same
// caveat as zmx applies: with no framing, a line-editing consumer executes each
// intermediate line as it arrives, and only the FINAL line is guaranteed
// unsubmitted.
func (h *Herdr) Paste(ctx context.Context, target, text string) error {
	return h.Type(ctx, target, text)
}

func (h *Herdr) Press(ctx context.Context, target string, keys ...backend.Key) error {
	var payload strings.Builder
	for _, k := range keys {
		seq, ok := keySequence(k)
		if !ok {
			return backend.Errorf(backend.CodeUsage, "unknown key %q", string(k))
		}
		payload.WriteString(seq)
	}
	row, err := h.resolvePane(ctx, target)
	if err != nil {
		return err
	}
	return h.send(ctx, row.PaneID, payload.String())
}

func (h *Herdr) Submit(ctx context.Context, target string) error {
	return h.Press(ctx, target, backend.KeyEnter)
}

// SendAtomic delivers text and its terminator as one caller-visible unit (§4.7).
//
// herdr has a single-operation form for exactly this: one request carries the
// text and the Enter, and the server concatenates them into ONE write to the
// pane (src/app/api_helpers.rs:48-67). So atomicity is the backend's here
// rather than something the write lock has to provide, and there is no settle
// gap to pace because the terminator is not part of the text: the server
// encodes it as a keypress after framing the text as a paste, which is the
// distinction §4.5 is about.
func (h *Herdr) SendAtomic(ctx context.Context, target, text string) error {
	row, err := h.resolvePane(ctx, target)
	if err != nil {
		return err
	}
	_, err = h.run(ctx, "pane", "run", row.PaneID, text)
	return err
}

// Interrupt asks the foreground process to stop.
//
// It writes 0x03 into the pane, and on this backend that IS a signal: the pane
// is a real PTY with its line discipline intact, so the byte reaches the
// foreground process group as SIGINT. The contrast with zmx is the point —
// there the same write generates no signal at all and the interrupt has to be
// aimed at the process group by hand (§2.8.1, cause 1). Measured here by
// starting `sleep 30` from the session's shell: the foreground group returns to
// the shell and the next command runs.
func (h *Herdr) Interrupt(ctx context.Context, target string) error {
	return h.Press(ctx, target, backend.KeyCtrlC)
}

func (h *Herdr) send(ctx context.Context, paneID, text string) error {
	if text == "" {
		return nil
	}
	_, err := h.run(ctx, "pane", "send-text", paneID, text)
	return err
}
