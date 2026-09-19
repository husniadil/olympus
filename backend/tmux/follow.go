package tmux

import (
	"context"
	"io"
	"os"
	"strings"
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
	// A pane has one pipe, and a second pipe-pane silently replaces the first:
	// that reader would wait forever, and closing either would turn off the
	// other's tap. So a pane already piped — by another follow or by the
	// operator — is refused rather than taken over.
	if out, err := t.run(ctx, nil, "display-message", "-p", "-t", pane, "#{pane_pipe}"); err != nil {
		_ = os.Remove(path)
		return nil, named(target, err)
	} else if strings.TrimSpace(out) == "1" {
		_ = os.Remove(path)
		return nil, backend.Errorf(backend.CodeConflict, "%s already has its output piped somewhere; tmux keeps one pipe per pane", target)
	}
	// -O taps output only; the shell fragment appends so nothing is lost
	// between the tap starting and this reader opening the file. The path is
	// quoted, since TMPDIR may hold a space.
	if _, err := t.run(ctx, nil, "pipe-pane", "-t", pane, "-O", "cat >> "+shellQuote(path)); err != nil {
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
		ended: func() bool {
			return t.Probe(context.WithoutCancel(ctx), target) == backend.StateAbsent
		},
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
	ctx  context.Context
	file *os.File
	// ended reports that the session is gone, so nothing more will come.
	ended  func() bool
	stop   func()
	closed bool
	gone   bool
	idle   int
}

// followEndEvery is how many idle polls pass between asks whether the session
// still exists: a probe is a subprocess, too dear for every 50ms.
const followEndEvery = 20

func (r *tailReader) Read(p []byte) (int, error) {
	for {
		n, err := r.file.Read(p)
		if n > 0 {
			r.idle = 0
			return n, nil
		}
		if err != nil && err != io.EOF {
			return 0, err
		}
		// EOF here means "nothing more YET", not "nothing more ever": the
		// session is still running and tmux may append at any moment.
		if r.closed || r.gone {
			return 0, io.EOF
		}
		if r.idle++; r.ended != nil && r.idle%followEndEvery == 0 && r.ended() {
			// One more pass for whatever tmux wrote before the pane went.
			r.gone = true
			continue
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

// shellQuote quotes a word for sh.
func shellQuote(word string) string {
	return "'" + strings.ReplaceAll(word, "'", `'\''`) + "'"
}
