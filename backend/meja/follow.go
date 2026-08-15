package meja

import (
	"context"
	"io"
	"os"
	"os/exec"

	"github.com/creack/pty"

	"github.com/husniadil/olympus/backend"
)

// Follow streams a session's output as it is produced.
//
// meja has no output tap — no pipe-pane, no streaming command — but it does not
// need one: a client IS the tap. Everything the session renders is written to
// every attached client, so a headless client of our own, whose PTY we hand
// back, is a faithful copy of the stream rather than a reconstruction of it.
//
// What arrives is the RENDERED stream, escape sequences and all, which is the
// same shape zmx's follow delivers (§5.6). Polling a capture instead would be a
// different thing under the same name: it drops whatever is overwritten between
// two reads, and a follow that silently loses output is worse than none.
//
// The client is sized to the session's current geometry so that following
// cannot reshape a terminal somebody else is sitting in — see attachTransient.
func (m *Meja) Follow(ctx context.Context, target string) (io.ReadCloser, error) {
	// Probed first, so following nothing is not-found rather than a stream that
	// simply never produces anything (§10).
	if state := m.Probe(ctx, target); state != backend.StatePresent {
		if state == backend.StateAbsent {
			return nil, backend.Errorf(backend.CodeSessionNotFound, "no session %s", target)
		}
		return nil, backend.Errorf(backend.CodeBackendUnavailable, "cannot reach meja to follow %s", target)
	}

	cols, rows := m.paneSize(ctx, target)
	cmd := exec.CommandContext(ctx, "meja", append(m.addressing(), "attach", "-t", target)...)
	cmd.Env = append(os.Environ(), "TERM=xterm-256color")
	f, err := pty.StartWithSize(cmd, &pty.Winsize{Cols: uint16(cols), Rows: uint16(rows + 1)})
	if err != nil {
		return nil, backend.Wrapf(backend.CodeBackendUnavailable, err, "following %s", target)
	}
	return &followClient{pty: f, cmd: cmd}, nil
}

// A followClient is the tap and the client that produces it, closed together.
//
// Both are needed: closing only the PTY leaves a client attached, and meja
// sizes a session to its smallest client, so a leaked one keeps a session
// shaped by a follow that ended long ago.
type followClient struct {
	pty *os.File
	cmd *exec.Cmd
}

func (f *followClient) Read(p []byte) (int, error) { return f.pty.Read(p) }

func (f *followClient) Close() error {
	err := f.pty.Close()
	_ = f.cmd.Process.Kill()
	_, _ = f.cmd.Process.Wait()
	return err
}
