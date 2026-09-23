package tmux

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
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
	//
	// The one exception is a tap a follow left behind when it was killed before
	// it could turn it off (§5.6): the pane carries the tag naming its
	// follower, that process is gone, and nobody reads the pipe any more.
	out, err := t.run(ctx, nil, "display-message", "-p", "-t", pane, "#{pane_pipe}"+fieldSeparator+"#{"+followTag+"}")
	if err != nil {
		_ = os.Remove(path)
		return nil, named(target, err)
	}
	fields := SplitFields(strings.TrimRight(out, "\n"))
	piped := fields[0] == "1"
	var stale string
	if len(fields) > 1 {
		if owner, sink, ok := parseFollowTag(fields[1]); ok && !processAlive(owner) {
			stale = sink
		}
	}
	if piped && stale == "" {
		_ = os.Remove(path)
		return nil, backend.Errorf(backend.CodeConflict, "%s already has its output piped somewhere; tmux keeps one pipe per pane", target)
	}
	// -O taps output only; the shell fragment appends so nothing is lost
	// between the tap starting and this reader opening the file. The path is
	// quoted, since TMPDIR may hold a space. The tag is chained into the same
	// invocation, so no tap of ours is ever left untagged.
	if _, err := t.run(ctx, nil,
		"pipe-pane", "-t", pane, "-O", "cat >> "+shellQuote(path),
		";", "set-option", "-p", "-t", pane, followTag, strconv.Itoa(os.Getpid())+" "+path,
	); err != nil {
		_, _ = t.run(context.WithoutCancel(ctx), nil, "pipe-pane", "-t", pane, ";", "set-option", "-p", "-u", "-t", pane, followTag)
		_ = os.Remove(path)
		return nil, named(target, err)
	}
	if stale != "" {
		removeFollowSink(stale)
	}

	file, err := os.Open(path)
	if err != nil {
		_, _ = t.run(context.WithoutCancel(ctx), nil, "pipe-pane", "-t", pane, ";", "set-option", "-p", "-u", "-t", pane, followTag)
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
			_, _ = t.run(context.WithoutCancel(ctx), nil,
				"pipe-pane", "-t", pane, ";", "set-option", "-p", "-u", "-t", pane, followTag)
			_ = os.Remove(path)
		},
	}, nil
}

// followTag is the pane option a follow records itself in while its tap is on:
// the follower's process id and the file the tap writes to (§5.6). tmux does
// not report what a pane is piped into, so without it a tap left by a killed
// follower is indistinguishable from the operator's own pipe.
const followTag = "@olympus-follow"

// parseFollowTag reads a follow tag back.
func parseFollowTag(tag string) (owner int, sink string, ok bool) {
	pid, sink, found := strings.Cut(strings.TrimSpace(tag), " ")
	if !found || sink == "" {
		return 0, "", false
	}
	owner, err := strconv.Atoi(pid)
	if err != nil || owner <= 0 {
		return 0, "", false
	}
	return owner, sink, true
}

// processAlive reports whether a process exists. One that exists under another
// user still counts: the tap is somebody's.
func processAlive(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}

// removeFollowSink removes a dead follower's file, but only one shaped like a
// file a follow makes: the tag is a pane option anybody can set, and it must
// not become a way to delete an arbitrary file.
func removeFollowSink(path string) {
	if filepath.IsAbs(path) && strings.HasPrefix(filepath.Base(path), "olympus-follow-") {
		_ = os.Remove(path)
	}
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
