package zmx

import (
	"context"
	"io"
	"os/exec"

	"github.com/husniadil/olympus/backend"
)

// Follow streams the session's output using zmx's own tail primitive.
//
// The bytes arrive exactly as the session produced them, escape sequences and
// all — this is a tap on the stream, not a rendering of it.
func (z *Zmx) Follow(ctx context.Context, target string) (io.ReadCloser, error) {
	if err := z.requirePresent(ctx, target); err != nil {
		return nil, err
	}

	cmd := exec.CommandContext(ctx, "zmx", "tail", target)
	cmd.Env = z.env(spawnEnv())
	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil, backend.Wrapf(backend.CodeUnexpected, err, "following %s", target)
	}
	if err := cmd.Start(); err != nil {
		return nil, backend.Wrapf(backend.CodeUnexpected, err, "following %s", target)
	}
	return &followed{reader: out, stop: func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }}, nil
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
