package meja

import (
	"context"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/creack/pty"

	"github.com/husniadil/olympus/backend"
)

// clientWait bounds how long an injection waits for its transient client.
const clientWait = 5 * time.Second

// clientRetry is how often the operation is retried while the client arrives.
const clientRetry = 10 * time.Millisecond

// withClient runs an input operation with a client attached to target.
//
// This exists because meja routes every input command THROUGH a client and
// refuses when a session has none: send-keys and paste-buffer both answer
// "command requires an attached client" even with an explicit -t. Observation
// needs no client; driving does (§2.10).
//
// The client is transient. Olympus's CLI is a process that runs once and exits,
// so there is no place to keep a durable one, and keeping one would be the
// daemon this project does not have. Measured cost of attach-inject-detach:
// 68ms cold, 23ms warm.
//
// If a client is ALREADY attached — a human sitting in the session — no second
// one is created. That is not only an optimisation: attaching is not free, as
// the size rule below explains.
func (m *Meja) withClient(ctx context.Context, target string, fn func() error) error {
	if err := fn(); err == nil || !needsClient(err) {
		return err
	}

	stop, err := m.attachTransient(ctx, target)
	if err != nil {
		return err
	}
	defer stop()

	deadline := time.Now().Add(clientWait)
	for {
		// Retried against the real operation rather than polled against a
		// status field: the question is whether this command works yet, and
		// asking it directly cannot disagree with itself.
		err := fn()
		if err == nil || !needsClient(err) {
			return err
		}
		if time.Now().After(deadline) {
			return err
		}
		select {
		case <-ctx.Done():
			return backend.Wrapf(backend.CodeTimeout, ctx.Err(), "waiting for a client on %s", target)
		case <-time.After(clientRetry):
		}
	}
}

func needsClient(err error) bool {
	return err != nil && strings.Contains(strings.ToLower(err.Error()), "requires an attached client")
}

// attachTransient brings up a headless client and returns its teardown.
//
// It attaches at the session's CURRENT size, plus one row. meja sizes a session
// to its smallest client and does NOT restore the size when that client leaves,
// so a client of any other size silently and permanently reshapes the terminal
// of whoever else is attached. Measured: a human at 200x50 gives a 200x49 pane;
// a client at 80x24 shrinks it to 80x23 and it stays there after that client
// exits. The extra row is meja's status bar, which is subtracted from every
// client's height.
func (m *Meja) attachTransient(ctx context.Context, target string) (func(), error) {
	cols, rows := m.paneSize(ctx, target)

	cmd := exec.CommandContext(ctx, "meja", append(m.addressing(), "attach", "-t", target)...)
	cmd.Env = append(os.Environ(), "TERM=xterm-256color")
	f, err := pty.StartWithSize(cmd, &pty.Winsize{Cols: uint16(cols), Rows: uint16(rows + 1)})
	if err != nil {
		return nil, backend.Wrapf(backend.CodeBackendUnavailable, err, "attaching a client to %s", target)
	}
	// Drained, not ignored: a client whose output nobody reads fills its pipe
	// and blocks the server rendering to it.
	go func() { _, _ = io.Copy(io.Discard, f) }()

	return func() {
		_ = f.Close()
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	}, nil
}

// paneSize reads the session's current geometry, falling back to a default.
func (m *Meja) paneSize(ctx context.Context, target string) (int, int) {
	out, err := m.run(ctx, nil, "list-panes", "-t", target, "-F", "#{pane_width}\t#{pane_height}")
	if err != nil {
		return 80, 24
	}
	fields := strings.Split(strings.TrimSpace(strings.Split(out, "\n")[0]), "\t")
	if len(fields) < 2 {
		return 80, 24
	}
	cols, errC := strconv.Atoi(fields[0])
	rows, errR := strconv.Atoi(fields[1])
	if errC != nil || errR != nil || cols <= 0 || rows <= 0 {
		return 80, 24
	}
	return cols, rows
}

// Type delivers literal text with no terminator.
func (m *Meja) Type(ctx context.Context, target, text string) error {
	if text == "" {
		return nil
	}
	return m.withClient(ctx, target, func() error {
		_, err := m.run(ctx, nil, "send-keys", "-t", target, "-l", "--", text)
		return named(target, err)
	})
}

// Paste delivers multi-line text through a paste buffer.
//
// Through a buffer rather than as literal keys so a consumer that detects
// bracketed paste sees one paste rather than a burst of typing (§4.4).
func (m *Meja) Paste(ctx context.Context, target, text string) error {
	if text == "" {
		return nil
	}
	return m.withClient(ctx, target, func() error {
		if _, err := m.run(ctx, nil, "set-buffer", "--", text); err != nil {
			return named(target, err)
		}
		// -d deletes the buffer after pasting, so a failed or repeated call
		// cannot leave one behind for the next caller to paste by accident.
		_, err := m.run(ctx, nil, "paste-buffer", "-t", target, "-d")
		return named(target, err)
	})
}

// Press sends named keys.
func (m *Meja) Press(ctx context.Context, target string, keys ...backend.Key) error {
	if len(keys) == 0 {
		return nil
	}
	names := make([]string, 0, len(keys))
	for _, k := range keys {
		name, ok := keyName(k)
		if !ok {
			// Raised before meja is invoked: an unknown key is the caller's
			// input, not the backend's failure.
			return backend.Errorf(backend.CodeUsage, "unknown key %q", string(k))
		}
		names = append(names, name)
	}
	return m.withClient(ctx, target, func() error {
		_, err := m.run(ctx, nil, append([]string{"send-keys", "-t", target}, names...)...)
		return named(target, err)
	})
}

// Submit presses the terminator on its own.
func (m *Meja) Submit(ctx context.Context, target string) error {
	return m.Press(ctx, target, backend.KeyEnter)
}

// SendAtomic delivers text and its terminator without releasing the client
// between them.
//
// The two remain SEPARATE writes: a terminator folded into the text becomes a
// literal newline inside a consumer's input box instead of registering as a
// keypress (§4.5). What is atomic is the client, not the bytes — holding one
// client across both is what stops another caller's injection landing between
// them.
func (m *Meja) SendAtomic(ctx context.Context, target, text string) error {
	return m.withClient(ctx, target, func() error {
		if text != "" {
			if _, err := m.run(ctx, nil, "send-keys", "-t", target, "-l", "--", text); err != nil {
				return named(target, err)
			}
		}
		_, err := m.run(ctx, nil, "send-keys", "-t", target, "Enter")
		return named(target, err)
	})
}

// Interrupt asks the foreground program to stop.
func (m *Meja) Interrupt(ctx context.Context, target string) error {
	return m.Press(ctx, target, backend.KeyCtrlC)
}
