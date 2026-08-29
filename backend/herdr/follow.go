package herdr

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"

	"github.com/husniadil/olympus/backend"
)

// Follow streams the session's output as it is produced (§5.6).
//
// herdr's tap is a read-only terminal session stream, and it is not a byte pipe
// the way tmux's pipe-pane or zmx's tail are: it emits one JSON envelope per
// frame carrying base64 ANSI (src/client/mod.rs:1059-1078). This decodes those
// envelopes back into the byte stream the interface promises, in process, so a
// consumer still receives raw terminal output rather than a wrapping it would
// have to know about.
//
// What arrives is the SERVER'S RENDERING of the pane rather than the exact
// bytes the program wrote — a repaint reaches a follower as the cursor
// addressing that redraws it. That is a real difference from the other
// backends and is disclosed as one (§0.8); what it does not change is the
// property following exists for, which is that output produced and scrolled
// past between two captures is still delivered.
//
// Observing does NOT resize the pane, so the frames' geometry is the
// observer's alone and nobody else's terminal is reshaped by following them.
// Measured: a pane at 70x22 stayed 70x22 while an observer read it at 100x30.
func (h *Herdr) Follow(ctx context.Context, target string) (io.ReadCloser, error) {
	row, err := h.resolvePane(ctx, target)
	if err != nil {
		return nil, err
	}

	cmd := h.command(ctx, "terminal", "session", "observe", row.PaneID)
	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil, backend.Wrapf(backend.CodeUnexpected, err, "following %s", target)
	}
	if err := cmd.Start(); err != nil {
		return nil, backend.Wrapf(backend.CodeUnexpected, err, "following %s", target)
	}

	reader, writer := io.Pipe()
	go decodeFrames(out, writer)
	return &followed{
		reader: reader,
		stop: func() {
			_ = out.Close()
			if cmd.Process != nil {
				_ = cmd.Process.Kill()
			}
			_ = cmd.Wait()
		},
	}, nil
}

// decodeFrames turns the envelope stream into the bytes the frames carry.
//
// A frame that does not decode is skipped rather than fatal: the stream also
// carries non-frame envelopes (a server shutdown notice, for one), and killing
// a follow because it saw one would drop output the caller is still owed.
func decodeFrames(source io.Reader, sink *io.PipeWriter) {
	scanner := bufio.NewScanner(source)
	// A full repaint of a large pane is far past bufio's default 64 KiB line,
	// and a scanner that stops at one silently truncates the stream.
	scanner.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	for scanner.Scan() {
		var frame struct {
			Type  string `json:"type"`
			Bytes string `json:"bytes"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &frame); err != nil {
			continue
		}
		if frame.Type != "terminal.frame" {
			continue
		}
		decoded, err := base64.StdEncoding.DecodeString(frame.Bytes)
		if err != nil {
			continue
		}
		if _, err := sink.Write(decoded); err != nil {
			_ = sink.CloseWithError(err)
			return
		}
	}
	_ = sink.CloseWithError(scanner.Err())
}

// followed couples a stream to whatever has to be torn down when the caller
// stops reading.
type followed struct {
	reader io.ReadCloser
	stop   func()
}

func (f *followed) Read(p []byte) (int, error) { return f.reader.Read(p) }

func (f *followed) Close() error {
	err := f.reader.Close()
	if f.stop != nil {
		f.stop()
	}
	return err
}
