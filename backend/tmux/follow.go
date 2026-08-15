package tmux

import (
	"context"
	"io"
	"os"
	"time"

	"github.com/husniadil/olympus/backend"
)

// followPoll is how long to wait for more bytes before checking again. The tap
// itself is push-based; only this reader's end of it polls.
const followPoll = 50 * time.Millisecond

// Follow streams the pane's output using tmux's own pipe-pane primitive.
//
// tmux pipes into a COMMAND rather than into a file descriptor Olympus holds,
// so the tap is pointed at a temporary file which this reader then follows. The
// alternative — polling capture-pane and diffing — loses anything printed and
// scrolled away between two polls, which is exactly the output a caller
// following a build wants most.
func (t *Tmux) Follow(ctx context.Context, target string) (io.ReadCloser, error) {
	if state := t.Probe(ctx, target); state != backend.StatePresent {
		if state == backend.StateAbsent {
			return nil, backend.Errorf(backend.CodeSessionNotFound, "no session %s", target)
		}
		return nil, backend.Errorf(backend.CodeBackendUnavailable, "cannot reach tmux to follow %s", target)
	}

	sink, err := os.CreateTemp("", "olympus-follow-*")
	if err != nil {
		return nil, backend.Wrapf(backend.CodeUnexpected, err, "following %s", target)
	}
	path := sink.Name()
	_ = sink.Close()

	pane := paneTarget(target)
	// -O taps output only; the shell fragment appends so nothing is lost
	// between the tap starting and this reader opening the file.
	if _, err := t.run(ctx, nil, "pipe-pane", "-t", pane, "-O", "cat >> "+path); err != nil {
		_ = os.Remove(path)
		return nil, named(target, err)
	}

	file, err := os.Open(path)
	if err != nil {
		_, _ = t.run(context.WithoutCancel(ctx), nil, "pipe-pane", "-t", pane)
		_ = os.Remove(path)
		return nil, backend.Wrapf(backend.CodeUnexpected, err, "following %s", target)
	}

	return &tailReader{
		ctx:  ctx,
		file: file,
		stop: func() {
			// Turning the tap off is what stops tmux writing, so it happens
			// before the file goes: a pipe-pane left on writes to a path that
			// no longer exists for as long as the pane lives.
			_, _ = t.run(context.WithoutCancel(ctx), nil, "pipe-pane", "-t", pane)
			_ = os.Remove(path)
		},
	}, nil
}

// tailReader reads a file that is still being written, waiting at the end
// rather than reporting EOF.
type tailReader struct {
	ctx    context.Context
	file   *os.File
	stop   func()
	closed bool
}

func (r *tailReader) Read(p []byte) (int, error) {
	for {
		n, err := r.file.Read(p)
		if n > 0 {
			return n, nil
		}
		if err != nil && err != io.EOF {
			return 0, err
		}
		// EOF here means "nothing more YET", not "nothing more ever": the
		// session is still running and tmux may append at any moment.
		if r.closed {
			return 0, io.EOF
		}
		select {
		case <-r.ctx.Done():
			return 0, io.EOF
		case <-time.After(followPoll):
		}
	}
}

func (r *tailReader) Close() error {
	r.closed = true
	err := r.file.Close()
	if r.stop != nil {
		r.stop()
	}
	return err
}
