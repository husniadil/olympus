package olympus

import (
	"context"
	"io"

	"github.com/husniadil/olympus/backend"
)

// Watch streams a session's output to a writer until the context is cancelled
// or the session ends.
//
// This is not Screen in a loop. A capture shows the pane as it looks NOW, so
// anything printed and scrolled away between two polls is gone — which is
// precisely the output someone following a long build cares about. Watching
// taps the byte stream instead.
//
// What arrives is raw terminal output, escape sequences included. It is a
// stream rather than a picture: a caller that wants to match on content should
// capture or wait instead, and one that wants to render it should pass it to
// something that understands a terminal.
func (s *Session) Watch(ctx context.Context, out io.Writer) error {
	stream, err := s.ol.backend.Follow(ctx, s.name)
	if err != nil {
		return err
	}
	defer stream.Close()

	_, err = io.Copy(out, stream)
	if err != nil && ctx.Err() == nil {
		return backend.Wrapf(backend.CodeUnexpected, err, "following %s", s.name)
	}
	// A cancelled context is how a caller says "stop", not a failure to report.
	return nil
}
