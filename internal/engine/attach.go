//go:build darwin || linux

package engine

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"

	"github.com/creack/pty"

	"github.com/husniadil/olympus/backend"
)

// resetSequence turns off everything an inner application may have switched on
// through the PTY (behavior §8.2).
//
// Restoring the saved termios is NOT enough on its own: termios is the outer
// terminal's own line discipline, while mouse, focus and bracketed-paste
// reporting are modes the inner application enabled by writing escape sequences
// OUT through the PTY. Those survive a termios restore, and the next shell
// prompt then receives \e[<...M and \e[I junk on every mouse move.
const resetSequence = "\x1b[?1006l\x1b[?1003l\x1b[?1002l\x1b[?1000l" + // mouse reporting off
	"\x1b[?1004l" + // focus reporting off
	"\x1b[?2004l" + // bracketed paste off
	"\x1b[?25h" // cursor shown

// resizeControl is the in-band resize request for a caller whose stdin is not a
// terminal (behavior §8.3, §17.1).
const (
	resizePrefix = "\x1b]olympus;resize;"
	resizeSuffix = "\x07"
)

// AttachIO is the outer terminal an attach streams through.
type AttachIO struct {
	In  *os.File
	Out *os.File
	Err io.Writer
}

// Attach runs a prepared attach client inside a PTY and streams it both ways
// until the client exits, returning the client's own exit code.
//
// The exit code follows the CLIENT's, not Olympus's vocabulary: once the
// presence gate has passed, this hands off, and an attach exiting 3 is not
// necessarily not-found (behavior §12.1).
func Attach(ctx context.Context, attachment backend.Attachment, io AttachIO, spec backend.AttachSpec, superseded <-chan struct{}) (int, error) {
	// The view session, or whatever else the backend created for this attach,
	// is reaped unconditionally. A client can exit on its own — its base died,
	// it was killed out from under us — with no explicit close ever running,
	// and cleanup that only happens on the tidy path leaks forever
	// (behavior §8.8).
	defer func() { _ = attachment.Close() }()

	restore := enterRawMode(io)
	// Exactly once, across every exit path. A process killed by an unhandled
	// signal runs no defers at all, which is how an operator's terminal gets
	// left in raw mode with mouse reporting on — so the signal paths are wired
	// below, not left to the deferred call alone.
	defer restore()

	terminalGone := make(chan os.Signal, 1)
	signal.Notify(terminalGone, syscall.SIGTERM, syscall.SIGHUP)
	defer signal.Stop(terminalGone)

	child := attachment.Cmd
	tty, err := pty.Start(child)
	if err != nil {
		return 0, backend.Wrapf(backend.CodeUnexpected, err, "starting the attach client")
	}
	defer tty.Close()

	// An explicit size applies when there is no window to inherit one from.
	// With a terminal on stdin the inherited size is the truth and wins; with
	// a pipe there is no other source, so a caller that knows how big its
	// consumer is has to be able to say so.
	if spec.Cols > 0 && spec.Rows > 0 && (io.In == nil || !isTerminal(io.In.Fd())) {
		_ = pty.Setsize(tty, &pty.Winsize{Cols: uint16(spec.Cols), Rows: uint16(spec.Rows)})
	}

	stopResizing := startResizing(tty, io, spec.Role)
	defer stopResizing()

	go func() {
		select {
		case <-terminalGone:
			// Restore before dying, then let the default disposition finish
			// the job. Without this the terminal is left raw.
			restore()
			_ = child.Process.Signal(syscall.SIGTERM)
		case <-superseded:
			// The message is generic on purpose. POSIX signal delivery carries
			// no sender pid portably, so the superseded side cannot honestly
			// name who stole from it — that framing belongs on the stealer's
			// side, where the holder's pid is genuinely known (behavior §8.6).
			if io.Err != nil {
				_, _ = fmt.Fprintln(io.Err, "detached: superseded")
			}
			restore()
			_ = child.Process.Signal(syscall.SIGTERM)
		case <-ctx.Done():
			_ = child.Process.Signal(syscall.SIGTERM)
		}
	}()

	// Outward: whatever the session paints.
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = stdcopy(io.Out, tty)
	}()

	// Inward: the operator's keystrokes, minus any in-band control lines.
	go func() {
		if spec.Role == backend.RoleViewer {
			// A viewer drops input entirely. On a backend with one shared PTY
			// per session, a viewer's keystrokes would land in everyone's
			// session (behavior §8.7).
			return
		}
		forwardInput(io.In, tty)
	}()

	err = child.Wait()
	<-done

	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return exit.ExitCode(), nil
	}
	if err != nil {
		return 0, backend.Wrapf(backend.CodeUnexpected, err, "the attach client failed")
	}
	return 0, nil
}

// forwardInput streams the caller's input into the PTY, honouring the in-band
// resize control when there is no SIGWINCH to carry it.
//
// A caller whose stdin is a pipe has no window and therefore no resize signal,
// so the only way to tell the session how big to be is a control line in the
// stream itself. It is stripped before forwarding and never written into the
// session (behavior §8.3).
func forwardInput(in *os.File, tty *os.File) {
	if in == nil {
		return
	}
	if isTerminal(in.Fd()) {
		// A real terminal carries its size out of band, and its bytes are the
		// operator's keystrokes — nothing in them is addressed to us.
		_, _ = io.Copy(tty, in)
		return
	}

	buffer := make([]byte, 4096)
	for {
		n, err := in.Read(buffer)
		if n > 0 {
			cols, rows, rest, found := ParseResizeControl(string(buffer[:n]))
			if found {
				// A malformed payload is ignored rather than fatal: a bad
				// control sequence must not kill the session.
				_ = pty.Setsize(tty, &pty.Winsize{Cols: uint16(cols), Rows: uint16(rows)})
			}
			if rest != "" {
				if _, writeErr := tty.WriteString(rest); writeErr != nil {
					return
				}
			}
		}
		if err != nil {
			return
		}
	}
}

func stdcopy(dst io.Writer, src io.Reader) (int64, error) { return io.Copy(dst, src) }

// enterRawMode puts the outer terminal into raw mode and returns a restore
// function that runs at most once.
//
// A failure to enter raw mode degrades to cooked-mode behaviour rather than
// aborting: a usable attach with awkward key handling beats no attach at all.
//
// Piped stdin is deliberately untouched. There is no raw mode to restore, and
// writing reset bytes into a stream a programmatic consumer is parsing would
// corrupt it — that consumer owns its own terminal state (behavior §8.2).
func enterRawMode(streams AttachIO) func() {
	if streams.In == nil || !isTerminal(streams.In.Fd()) {
		return func() {}
	}

	saved, err := getTermios(streams.In.Fd())
	if err != nil {
		return func() {}
	}
	if err := setTermios(streams.In.Fd(), rawMode(saved)); err != nil {
		return func() {}
	}

	var once sync.Once
	return func() {
		once.Do(func() {
			_ = setTermios(streams.In.Fd(), saved)
			// Both layers. Termios alone leaves the modes the inner
			// application switched on.
			if streams.Out != nil {
				_, _ = streams.Out.WriteString(resetSequence)
			}
		})
	}
}

func isTerminal(fd uintptr) bool {
	_, err := getTermios(fd)
	return err == nil
}

// startResizing keeps the PTY's size in step with the caller's.
func startResizing(tty *os.File, streams AttachIO, role backend.Role) func() {
	if role == backend.RoleViewer {
		// Dropping input is not enough on a backend with one shared PTY: a
		// viewer's resize physically resizes the DRIVER's terminal, which is a
		// real disruption rather than a self-contained no-op (behavior §8.7).
		return func() {}
	}

	if streams.In != nil && isTerminal(streams.In.Fd()) {
		resizes := make(chan os.Signal, 1)
		signal.Notify(resizes, syscall.SIGWINCH)
		go func() {
			for range resizes {
				_ = pty.InheritSize(streams.In, tty)
			}
		}()
		// Synced immediately, then on every subsequent signal: without the
		// first sync the session keeps whatever size it was created with until
		// the operator happens to resize their window.
		_ = pty.InheritSize(streams.In, tty)
		return func() { signal.Stop(resizes); close(resizes) }
	}

	return func() {}
}

// ParseResizeControl matches the in-band resize request a non-TTY caller uses,
// returning the requested size and the input with the control stripped.
//
// The control MUST be stripped before forwarding and never written into the
// session. A malformed payload is ignored rather than fatal: a bad control
// sequence must not kill the session (behavior §8.3).
func ParseResizeControl(input string) (cols, rows int, rest string, found bool) {
	start := strings.Index(input, resizePrefix)
	if start < 0 {
		return 0, 0, input, false
	}
	tail := input[start+len(resizePrefix):]
	end := strings.Index(tail, resizeSuffix)
	if end < 0 {
		return 0, 0, input, false
	}

	stripped := input[:start] + tail[end+len(resizeSuffix):]
	fields := strings.Split(tail[:end], ";")
	if len(fields) != 2 {
		// Malformed, but still stripped: it was addressed to us, so it must
		// not reach the session either way.
		return 0, 0, stripped, false
	}
	cols, colsErr := strconv.Atoi(fields[0])
	rows, rowsErr := strconv.Atoi(fields[1])
	if colsErr != nil || rowsErr != nil || cols <= 0 || rows <= 0 {
		return 0, 0, stripped, false
	}
	return cols, rows, stripped, true
}
