//go:build darwin || linux

package olympus_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
	"unsafe"

	"github.com/creack/pty"
)

// §8.2: an attach that ends MUST hand the terminal back as it found it, in both
// layers — the termios it changed, and the modes the inner application switched
// on by writing out through the PTY.
//
// This is the highest-consequence path in the repo and nothing was watching it.
// A failure leaves an operator with no echo and no line editing, which is not a
// wrong answer they can read: it is a terminal they have to fix by typing
// `reset` blind.
//
// The test is the outer terminal. It owns a PTY, hands the slave to an attach
// client as its stdin and stdout, and reads the line discipline from its own
// descriptor before, during and after — so it observes what a human's terminal
// would actually be left in rather than what the code believes it did.
func TestAttachHandsTheTerminalBack(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is not installed")
	}
	binary := buildOlympus(t)

	dir, err := os.MkdirTemp(os.TempDir(), "olyt")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	socket := filepath.Join(dir, "s.sock")
	t.Cleanup(func() { _ = exec.Command("tmux", "-S", socket, "kill-server").Run() })

	where := []string{"--backend", "tmux", "--socket-path", socket}
	if out, err := exec.Command(binary, append(where, "start", "restore")...).CombinedOutput(); err != nil {
		t.Fatalf("start: %v\n%s", err, out)
	}

	ptmx, tty, err := pty.Open()
	if err != nil {
		t.Fatalf("opening a terminal to be the outer one: %v", err)
	}
	t.Cleanup(func() { _ = ptmx.Close(); _ = tty.Close() })

	before, err := termiosOf(tty.Fd())
	if err != nil {
		t.Fatalf("reading the terminal's line discipline: %v", err)
	}
	if before.Lflag&syscall.ECHO == 0 {
		t.Fatalf("the test's own terminal did not start in cooked mode, so restoring to it proves nothing")
	}

	client := exec.Command(binary, append(where, "attach", "restore")...)
	client.Stdin, client.Stdout, client.Stderr = tty, tty, tty
	// Deliberately NOT Setsid/Setctty. Making the client the session leader
	// means its death revokes this terminal, and every read afterwards fails
	// with ENOTTY — which reads exactly like "the terminal was destroyed"
	// rather than "the state was not restored". A real terminal outlives the
	// command run in it.
	if err := client.Start(); err != nil {
		t.Fatalf("starting the attach client: %v", err)
	}

	var mu sync.Mutex
	var streamed []byte
	go func() {
		buf := make([]byte, 32*1024)
		for {
			n, err := ptmx.Read(buf)
			if n > 0 {
				mu.Lock()
				streamed = append(streamed, buf[:n]...)
				mu.Unlock()
			}
			if err != nil {
				return
			}
		}
	}()

	// Raw mode is what proves the client took the terminal over; without it,
	// "restored" afterwards would be true of a client that did nothing.
	deadline := time.Now().Add(10 * time.Second)
	for {
		during, err := termiosOf(tty.Fd())
		if err == nil && during.Lflag&syscall.ECHO == 0 && during.Lflag&syscall.ICANON == 0 {
			break
		}
		if time.Now().After(deadline) {
			_ = client.Process.Kill()
			t.Fatal("the attach never put the terminal into raw mode")
		}
		time.Sleep(50 * time.Millisecond)
	}

	if err := client.Process.Signal(os.Interrupt); err != nil {
		t.Fatalf("interrupting the client: %v", err)
	}
	done := make(chan struct{})
	go func() { _, _ = client.Process.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		_ = client.Process.Kill()
		t.Fatal("the attach client did not exit when interrupted")
	}

	after, err := termiosOf(tty.Fd())
	if err != nil {
		t.Fatalf("reading the terminal back: %v", err)
	}
	if after.Lflag != before.Lflag || after.Iflag != before.Iflag || after.Oflag != before.Oflag {
		t.Errorf("the terminal was not handed back:\n  before: iflag=%#x oflag=%#x lflag=%#x\n  after:  iflag=%#x oflag=%#x lflag=%#x",
			before.Iflag, before.Oflag, before.Lflag, after.Iflag, after.Oflag, after.Lflag)
	}

	// The second layer. Termios alone leaves mouse, focus and bracketed-paste
	// reporting on, and the next shell prompt then receives escape junk on
	// every mouse move — a restored line discipline that still looks broken.
	mu.Lock()
	out := string(streamed)
	mu.Unlock()
	for _, off := range []struct {
		what     string
		sequence string
	}{
		{"mouse reporting", "\x1b[?1000l"},
		{"focus reporting", "\x1b[?1004l"},
		{"bracketed paste", "\x1b[?2004l"},
		{"the cursor", "\x1b[?25h"},
	} {
		if !strings.Contains(out, off.sequence) {
			t.Errorf("%s was never turned back off: the sequence %q is not in what the client wrote",
				off.what, off.sequence)
		}
	}
}

// termiosOf reads a terminal's line discipline.
//
// Read from the test's OWN descriptor rather than asked of the attach client,
// because the question is what a human at this terminal would be left with, and
// only the terminal can answer that.
func termiosOf(fd uintptr) (syscall.Termios, error) {
	var t syscall.Termios
	if _, _, errno := syscall.Syscall6(syscall.SYS_IOCTL, fd, ioctlGetTermios,
		uintptr(unsafe.Pointer(&t)), 0, 0, 0); errno != 0 {
		return t, errno
	}
	return t, nil
}
