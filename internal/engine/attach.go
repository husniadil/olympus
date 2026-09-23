//go:build darwin || linux

package engine

import (
	"bytes"
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
	"sync/atomic"
	"syscall"
	"time"

	"github.com/creack/pty"

	"github.com/husniadil/olympus/backend"
)

// settleQuiet is the stretch of silence from the client after which it is
// taken to be up and reading keys; settleLatest bounds the wait for a
// client that never stops painting (behavior §8.10). Both measured against
// herdr 0.9.0: its connecting frames arrive within a few hundred
// milliseconds of the first byte. settleMark is the beat after the
// attachment's SettleAfter sequence, where it names one: herdr's client
// pushes the kitty protocol as it comes up and is ready to read the walk's
// keys a stretch later. Idle it is ready almost at once (a key landed 4 of
// 4 at 50ms), but under load — the app opening a set of tabs, several
// clients attaching to one server at once — it needs longer: the
// two-clients e2e failed 6 of 6 at a 250ms beat and passed 6 of 6 at 400ms
// (measured 2026-09-13). Quiet still fires first for a client that goes
// quiet before then; the mark is what walks a client on a workspace that
// never does (a streaming agent), where the wait ran to the two-second cap
// before (measured: 2.1s to the walked frame, 0.5s after).
const (
	settleQuiet  = 250 * time.Millisecond
	settleLatest = 2 * time.Second
	settleMark   = 400 * time.Millisecond
	// settleDrain bounds the wait for a settle step once the attach is over.
	settleDrain = 5 * time.Second
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

// The in-band controls a caller whose stdin is not a terminal can put in the
// stream (behavior §8.3, §17.1): `resize;<cols>;<rows>` sizes the PTY, and
// `go;<target>` moves a client that can be moved (Attachment.Go) onto
// another target on its server, and `focus;<pane>` focuses a pane on its tab
// without zooming it (Attachment.Focus). Each is stripped before the stream reaches
// the session.
const (
	controlPrefix = "\x1b]olympus;"
	controlSuffix = "\x07"
	resizePrefix  = controlPrefix + "resize;"
	resizeSuffix  = controlSuffix
	goVerb        = "go"
	focusVerb     = "focus"
	// controlMost bounds how long a control may run before an unterminated
	// one is taken for ordinary bytes and forwarded: a target name is short.
	controlMost = 512
)

// The cadence at which an attachment's Probe is asked whether the target still
// exists, and how long a client is given to leave on SIGTERM before SIGKILL
// once it has answered absent (behavior §8.10).
const (
	TargetPollInterval = 500 * time.Millisecond
	targetGoneGrace    = 2 * time.Second
)

// WatchTarget polls probe every interval and closes the returned channel the
// first time it answers absent. It stops, without closing, when ctx ends.
//
// Only absent counts. An error answer — the server could not be asked — is
// skipped rather than treated as gone, because a hiccup on the socket must not
// end a live terminal; a server that has actually gone away ends the client on
// its own (behavior §8.10).
func WatchTarget(ctx context.Context, probe func(context.Context) backend.State, interval time.Duration) <-chan struct{} {
	gone := make(chan struct{})
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
			if probe(ctx) == backend.StateAbsent {
				close(gone)
				return
			}
		}
	}()
	return gone
}

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

	// A settle step is ended, and waited for, before that cleanup runs: it
	// steers the server, and a backend's cleanup can drop what serialises that
	// steering (herdr's walk lock). Bounded, since a step that ignores its
	// context must not hold the attach open.
	settleCtx, cancelSettle := context.WithCancel(ctx)
	settleDone := make(chan struct{})
	var settleStarted atomic.Bool
	defer func() {
		cancelSettle()
		if settleStarted.Load() {
			select {
			case <-settleDone:
			case <-time.After(settleDrain):
			}
		}
	}()

	restore := enterRawMode(io)
	// Exactly once, across every exit path. A process killed by an unhandled
	// signal runs no defers at all, which is how an operator's terminal gets
	// left in raw mode with mouse reporting on — so the signal paths are wired
	// below, not left to the deferred call alone.
	defer restore()

	terminalGone := make(chan os.Signal, 1)
	signal.Notify(terminalGone, syscall.SIGTERM, syscall.SIGHUP)
	defer signal.Stop(terminalGone)

	// The size the client starts at, set on the PTY before the client runs.
	// With a terminal on stdin the inherited size is the truth and wins; with
	// a pipe there is no other source, so a caller that knows how big its
	// consumer is has to be able to say so. Sizing the PTY after the start
	// raced a client that reads its size as it starts: under load herdr's saw
	// 0x0 and exited with "terminal reported a zero-sized grid" (measured, 1 in
	// about 120 attaches with the CPU saturated).
	child := attachment.Cmd
	var size *pty.Winsize
	if io.In != nil && isTerminal(io.In.Fd()) {
		if ws, err := pty.GetsizeFull(io.In); err == nil && ws.Cols > 0 && ws.Rows > 0 {
			size = ws
		}
	} else if spec.Cols > 0 && spec.Rows > 0 {
		size = &pty.Winsize{Cols: uint16(spec.Cols), Rows: uint16(spec.Rows)}
	}
	tty, err := pty.StartWithSize(child, size)
	if err != nil {
		return 0, backend.Wrapf(backend.CodeUnexpected, err, "starting the attach client")
	}
	defer tty.Close()

	stopResizing := startResizing(tty, io, spec.Role)
	defer stopResizing()

	// The target watch runs only for as long as the client does: the client's
	// exit cancels it, so a probe never outlives the attach it was made for.
	watchCtx, stopWatching := context.WithCancel(ctx)
	defer stopWatching()
	var targetGone <-chan struct{}
	if attachment.Probe != nil {
		targetGone = WatchTarget(watchCtx, attachment.Probe, TargetPollInterval)
	}
	// Closed once the client has been reaped, so the grace timer below can
	// tell "left on SIGTERM" from "still here".
	exited := make(chan struct{})
	// Closed BEFORE the client is signalled, so by the time its exit is
	// reaped the reason is already on record.
	endedWithTarget := make(chan struct{})
	// The settle step's failure, kept for the return: the client was ended
	// for it, and its own exit status says nothing a caller can act on.
	settleFailed := make(chan error, 1)
	var settleErr error
	// Closed once the client is on its target: the settle step has run, or
	// there was none. Input waits for it, so a key typed, or a move asked
	// for, before the client is up lands where it was meant to.
	settled := make(chan struct{})
	// Closed when the signal goroutine is done, so its narration lands
	// before the attach returns rather than racing the caller's read of it.
	signalled := make(chan struct{})

	go func() {
		defer close(signalled)
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
		case <-targetGone:
			// The client is attached to the whole session and would sit
			// showing whatever the server focused next; the attach was onto
			// the target, so it ends with the target (behavior §8.10).
			close(endedWithTarget)
			if io.Err != nil {
				_, _ = fmt.Fprintln(io.Err, "detached: the target is gone")
			}
			restore()
			_ = child.Process.Signal(syscall.SIGTERM)
			select {
			case <-exited:
			case <-time.After(targetGoneGrace):
				_ = child.Process.Kill()
			}
		case err := <-settleFailed:
			settleErr = err
			if io.Err != nil {
				_, _ = fmt.Fprintln(io.Err, "detached: the client could not be brought onto its target")
			}
			restore()
			_ = child.Process.Signal(syscall.SIGTERM)
			select {
			case <-exited:
			case <-time.After(targetGoneGrace):
				_ = child.Process.Kill()
			}
		case <-ctx.Done():
			_ = child.Process.Signal(syscall.SIGTERM)
			// A client that ignores SIGTERM would otherwise hold a cancelled
			// attach open for as long as it chose to run.
			select {
			case <-exited:
			case <-time.After(targetGoneGrace):
				_ = child.Process.Kill()
			}
		case <-exited:
		}
	}()

	// Outward: whatever the session paints. A settle step drives the client
	// with its own keys, so it may run only once the client is connected and
	// reading them. herdr never announces that; what the client does is
	// paint — its terminal setup, then a frame or two as it connects and
	// picks a workspace — and then go quiet, so the step waits for the first
	// quiet stretch after the first byte, and at the latest a moment after
	// the first byte where the client never goes quiet. The first byte alone
	// was too early: the terminal setup is painted before the client has
	// connected (measured, behavior §8.10). Where the backend names a
	// sequence the client writes once it reads keys (SettleAfter), the
	// step runs a beat after that instead, since a client on a workspace
	// that never stops painting never goes quiet and waited out the cap on
	// every attach (measured). The step runs beside the copy rather than
	// in its way, and one that fails ends the attach the way a vanished
	// target does.
	// What the client paints, watched for the sequences a walk expects
	// back from it (backend.Expect).
	watch := &outputWatch{}
	done := make(chan struct{})
	go func() {
		defer close(done)
		if attachment.Settle == nil && attachment.Go == nil {
			close(settled)
			_, _ = stdcopy(io.Out, tty)
			return
		}
		ran := attachment.Settle == nil
		if ran {
			close(settled)
		}
		settle := func() {
			if ran {
				return
			}
			ran = true
			settleStarted.Store(true)
			go func() {
				defer close(settleDone)
				if err := attachment.Settle(settleCtx, tty, watch.expect); err != nil {
					select {
					case settleFailed <- err:
					default:
					}
					return
				}
				close(settled)
			}()
		}
		chunks := make(chan []byte, 8)
		go func() {
			defer close(chunks)
			buf := make([]byte, 32*1024)
			for {
				n, err := tty.Read(buf)
				if n > 0 {
					chunks <- append([]byte(nil), buf[:n]...)
				}
				if err != nil {
					return
				}
			}
		}()
		var quiet <-chan time.Time
		var latest <-chan time.Time
		var marked <-chan time.Time
		// The tail of what has been seen, long enough to hold the mark
		// across a chunk boundary.
		var tail []byte
		for {
			select {
			case chunk, ok := <-chunks:
				if !ok {
					return
				}
				_, _ = io.Out.Write(chunk)
				watch.feed(chunk)
				if !ran {
					quiet = time.After(settleQuiet)
					if latest == nil {
						latest = time.After(settleLatest)
					}
					if mark := attachment.SettleAfter; len(mark) > 0 && marked == nil {
						tail = append(tail, chunk...)
						if bytes.Contains(tail, mark) {
							marked = time.After(settleMark)
						} else if len(tail) > len(mark) {
							tail = tail[len(tail)-len(mark)+1:]
						}
					}
				}
			case <-marked:
				settle()
				marked = nil
			case <-quiet:
				settle()
				quiet = nil
			case <-latest:
				settle()
				latest = nil
			}
		}
	}()

	// Inward: the operator's keystrokes, minus any in-band control lines.
	go func() {
		if spec.Role == backend.RoleViewer {
			// A viewer drops input entirely. On a backend with one shared PTY
			// per session, a viewer's keystrokes would land in everyone's
			// session (behavior §8.7).
			return
		}
		move := func(target string) {
			if attachment.Go == nil {
				if io.Err != nil {
					_, _ = fmt.Fprintln(io.Err, "olympus: this attach cannot be moved; go ignored")
				}
				return
			}
			if err := attachment.Go(ctx, target, tty, watch.expect); err != nil {
				// Non-blocking: only the first failure is read, and a later
				// one must not hang this goroutine on a full channel.
				select {
				case settleFailed <- err:
				default:
				}
			}
		}
		focus := func(target string) {
			if attachment.Focus == nil {
				if io.Err != nil {
					_, _ = fmt.Fprintln(io.Err, "olympus: this attach cannot focus a pane; focus ignored")
				}
				return
			}
			err := attachment.Focus(ctx, target, tty, watch.expect)
			if backend.CodeOf(err) == backend.CodeUnsupported {
				if io.Err != nil {
					_, _ = fmt.Fprintf(io.Err, "olympus: %v; focus ignored\n", err)
				}
				return
			}
			if err != nil {
				select {
				case settleFailed <- err:
				default:
				}
			}
		}
		forwardInput(io.In, tty, settled, exited, move, focus)
	}()

	err = child.Wait()
	close(exited)
	stopWatching()
	<-done
	<-signalled

	if settleErr != nil {
		return 0, settleErr
	}
	select {
	case <-endedWithTarget:
		// Olympus ended the client, not the client itself, so the status is
		// Olympus's: the attach did what was asked and its subject ended.
		// The client's own status is the signal it was sent, which says
		// nothing a caller can act on.
		return 0, nil
	default:
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		// A client ended by a signal has no code of its own, and Go's -1
		// becomes 255 at a process exit. 128+n is what a shell reports.
		if status, ok := exit.Sys().(syscall.WaitStatus); ok && status.Signaled() {
			return 128 + int(status.Signal()), nil
		}
		return exit.ExitCode(), nil
	}
	if err != nil {
		return 0, backend.Wrapf(backend.CodeUnexpected, err, "the attach client failed")
	}
	return 0, nil
}

// forwardInput streams the caller's input into the PTY, honouring the in-band
// controls.
//
// A caller whose stdin is a pipe has no window and therefore no resize signal,
// so the only way to tell the session how big to be is a control line in the
// stream itself; the same stream is how a caller moves a movable client onto
// another target, and a caller driving this attach under a PTY of its own (a
// consumer's bridge) has that stream and no other. So the controls are read
// on a terminal stdin too: the sequences are Olympus's own, and nothing a
// person types spells one. Each control is stripped before forwarding and
// never written into the session (behavior §8.3). Nothing is forwarded before
// the client has settled on its target, and a move runs HERE, in the stream's
// own order: bytes before it went before, bytes after it wait until the
// client is on the new target, so nothing typed lands mid-walk (§8.10).
func forwardInput(in *os.File, tty *os.File, settled, stop <-chan struct{}, move, focus func(target string)) {
	if in == nil {
		return
	}

	select {
	case <-settled:
	case <-stop:
		return
	}
	buffer := make([]byte, 4096)
	// What has been read but not yet forwarded: a control that has begun
	// and not ended within one read, kept until its end arrives.
	var held string
	for {
		n, err := in.Read(buffer)
		if n > 0 {
			input := held + string(buffer[:n])
			held = ""
			for input != "" {
				verb, payload, before, after, state := ParseControl(input)
				switch state {
				case ControlNone:
					// A read that filled the buffer was cut by it, and may
					// have ended inside a control's opening bytes: those are
					// held for the next read. Only then, so a key typed on its
					// own, an Escape above all, is never held back.
					if n == len(buffer) {
						for k := len(controlPrefix) - 1; k > 0; k-- {
							if strings.HasSuffix(input, controlPrefix[:k]) {
								held = input[len(input)-k:]
								input = input[:len(input)-k]
								break
							}
						}
					}
					if _, writeErr := tty.WriteString(input); writeErr != nil {
						return
					}
					input = ""
				case ControlPartial:
					if _, writeErr := tty.WriteString(before); writeErr != nil {
						return
					}
					held = after
					input = ""
				case ControlFound:
					if _, writeErr := tty.WriteString(before); writeErr != nil {
						return
					}
					switch verb {
					case "resize":
						// A malformed payload is ignored rather than fatal: a
						// bad control sequence must not kill the session.
						if cols, rows, ok := parseSize(payload); ok {
							_ = pty.Setsize(tty, &pty.Winsize{Cols: uint16(cols), Rows: uint16(rows)})
						}
					case goVerb:
						if payload != "" {
							move(payload)
						}
					case focusVerb:
						if payload != "" {
							focus(payload)
						}
					}
					input = after
				}
			}
		}
		if err != nil {
			return
		}
	}
}

// A ControlState says what ParseControl found.
type ControlState int

const (
	// ControlNone: no control in the input; all of it is the session's.
	ControlNone ControlState = iota
	// ControlPartial: a control has begun and not ended; `before` is the
	// session's and `after` is to be held for the rest of it.
	ControlPartial
	// ControlFound: one control, parsed; `before` and `after` are the
	// session's, `after` possibly holding another.
	ControlFound
)

// ParseControl finds the first in-band control in input. An unterminated
// control longer than controlMost is not one, and is handed back as bytes.
func ParseControl(input string) (verb, payload, before, after string, state ControlState) {
	start := strings.Index(input, controlPrefix)
	if start < 0 {
		return "", "", input, "", ControlNone
	}
	tail := input[start+len(controlPrefix):]
	end := strings.Index(tail, controlSuffix)
	if end < 0 {
		if len(tail) > controlMost {
			return "", "", input, "", ControlNone
		}
		return "", "", input[:start], input[start:], ControlPartial
	}
	body := tail[:end]
	verb, payload, _ = strings.Cut(body, ";")
	return verb, payload, input[:start], tail[end+len(controlSuffix):], ControlFound
}

func parseSize(payload string) (cols, rows int, ok bool) {
	fields := strings.Split(payload, ";")
	if len(fields) != 2 {
		return 0, 0, false
	}
	cols, colsErr := strconv.Atoi(fields[0])
	rows, rowsErr := strconv.Atoi(fields[1])
	if colsErr != nil || rowsErr != nil || cols <= 0 || rows <= 0 {
		return 0, 0, false
	}
	return cols, rows, true
}

func stdcopy(dst io.Writer, src io.Reader) (int64, error) { return io.Copy(dst, src) }

// An outputWatch hands out backend.Expect over the client's output: each
// expectation sees the bytes written after it was registered, and is met
// once its mark is among them. A waiter's buffer is kept to the tail that
// could still hold a mark across a chunk boundary.
type outputWatch struct {
	mu      sync.Mutex
	waiters []*outputWaiter
}

type outputWaiter struct {
	mark []byte
	buf  []byte
	met  chan struct{}
}

const outputWaiterTail = 8 * 1024

func (w *outputWatch) feed(chunk []byte) {
	w.mu.Lock()
	defer w.mu.Unlock()
	kept := w.waiters[:0]
	for _, x := range w.waiters {
		x.buf = append(x.buf, chunk...)
		if bytes.Contains(x.buf, x.mark) {
			close(x.met)
			continue
		}
		if len(x.buf) > outputWaiterTail {
			x.buf = x.buf[len(x.buf)-outputWaiterTail:]
		}
		kept = append(kept, x)
	}
	w.waiters = kept
}

func (w *outputWatch) expect(mark []byte) func(within time.Duration) bool {
	x := &outputWaiter{mark: mark, met: make(chan struct{})}
	w.mu.Lock()
	w.waiters = append(w.waiters, x)
	w.mu.Unlock()
	return func(within time.Duration) bool {
		select {
		case <-x.met:
			return true
		case <-time.After(within):
			w.mu.Lock()
			defer w.mu.Unlock()
			// The mark may have landed between the timer and the lock, or
			// with the timer at once, where select picks either.
			select {
			case <-x.met:
				return true
			default:
			}
			for i, y := range w.waiters {
				if y == x {
					w.waiters = append(w.waiters[:i], w.waiters[i+1:]...)
					break
				}
			}
			return false
		}
	}
}

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
// returning the requested size and the input with the control stripped: the
// first control in the input, over ParseControl, kept for the callers and
// tests that read a resize alone.
//
// The control MUST be stripped before forwarding and never written into the
// session. A malformed payload is ignored rather than fatal: a bad control
// sequence must not kill the session (behavior §8.3).
func ParseResizeControl(input string) (cols, rows int, rest string, found bool) {
	verb, payload, before, after, state := ParseControl(input)
	if state != ControlFound || verb != "resize" {
		return 0, 0, input, false
	}
	cols, rows, ok := parseSize(payload)
	return cols, rows, before + after, ok
}
