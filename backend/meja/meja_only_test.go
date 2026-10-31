package meja_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/husniadil/olympus/backend"
	"github.com/husniadil/olympus/backend/meja"
)

func newBackend(t *testing.T) (backend.Backend, string) {
	t.Helper()
	dir, err := os.MkdirTemp(os.TempDir(), "olym")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	socket := filepath.Join(dir, "m.sock")
	t.Cleanup(func() { _ = exec.Command("meja", "-S", socket, "kill-server").Run() })
	return meja.New(meja.WithSocketPath(socket)), socket
}

// §8: the attach client must address the server it was configured with, and a
// viewer must be refused rather than silently handed a client that can type.
//
// meja has no read-only client. Accepting a viewer attach and giving it a full
// one is the dangerous failure: a watcher who believes they cannot type, and
// can, will eventually type into somebody else's session.
// §2.10: through 0.0.25 meja routes every input command through an attached
// client and refuses outright without one — the structural difference this
// backend was built around, narrowed to copy mode alone from 0.0.26.
func TestAttachAddressesItsServerAndRefusesAViewer(t *testing.T) {
	requireMeja(t)
	b, socket := newBackend(t)
	ctx := context.Background()

	if _, err := b.Create(ctx, backend.CreateSpec{Name: "att", Dir: t.TempDir()}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	att, err := b.Attach(ctx, "att", backend.AttachSpec{Role: backend.RoleController})
	if err != nil {
		t.Fatalf("preparing an attach: %v", err)
	}
	args := strings.Join(att.Cmd.Args, " ")
	if !strings.Contains(args, "-S "+socket) {
		t.Errorf("attach argv does not address the configured server:\n  %s", args)
	}
	if !strings.Contains(args, "attach -t att") {
		t.Errorf("attach argv does not target the session:\n  %s", args)
	}

	// §8.7: meja has no read-only client, so a viewer attach is refused rather
	// than downgraded to one that can type.
	if _, err := b.Attach(ctx, "att", backend.AttachSpec{Role: backend.RoleViewer}); backend.CodeOf(err) != backend.CodeUnsupported {
		t.Errorf("a viewer attach reports %v, want UNSUPPORTED — meja has no read-only client", backend.CodeOf(err))
	}

	// §8.4: meja has no supersede mechanism either, and a silent no-op would
	// leave the caller believing prior clients were displaced when they are
	// still there — and meja sizes the session to the smallest of them.
	superseded, err := b.Attach(ctx, "att", backend.AttachSpec{Role: backend.RoleController, Supersede: true})
	if err != nil {
		t.Fatalf("preparing a superseding attach: %v", err)
	}
	if len(superseded.Notices) == 0 {
		t.Error("a superseding attach on meja says nothing, but nothing was superseded")
	}

	// §10: attaching onto nothing is not-found, decided before the client runs.
	if _, err := b.Attach(ctx, "never-existed", backend.AttachSpec{}); backend.CodeOf(err) != backend.CodeSessionNotFound {
		t.Errorf("attaching to an absent session reports %v, want SESSION_NOT_FOUND", backend.CodeOf(err))
	}
}

// §0.1: input the backend rejects is USAGE, not UNEXPECTED.
//
// The two say opposite things to a program. UNEXPECTED means something went
// wrong and retrying will not help; USAGE means one corrected argument fixes
// it. meja rejects a session name that is entirely numeric — a rule tmux and
// zmx do not have, so the same call succeeds on them — and Olympus was
// reporting that as UNEXPECTED, telling a caller their input was fine and
// something else had broken.
func TestARejectedNameIsUsage(t *testing.T) {
	requireMeja(t)
	b, _ := newBackend(t)

	_, err := b.Create(context.Background(), backend.CreateSpec{Name: "1", Dir: t.TempDir()})
	if backend.CodeOf(err) != backend.CodeUsage {
		t.Errorf("a name meja rejects reports %v, want USAGE: %v", backend.CodeOf(err), err)
	}
	// The reason has to survive: a usage error that does not say what was wrong
	// with the argument leaves the caller to guess which of its rules was hit.
	if err == nil || !strings.Contains(err.Error(), "numeric") {
		t.Errorf("the message loses meja's reason: %v", err)
	}
}

// §4.1 Each paste goes through a buffer of its own. paste-buffer without -b
// pastes the most recent buffer, so two pastes into different sessions — which
// hold different locks — could each deliver the other's text. No buffer is
// left behind either. Every paste goes through a handle of its own, as the MCP
// door builds one per tool call, so the name must be unique per process rather
// than per handle.
//
// The concurrency is ACROSS sessions, one goroutine each, and within a session
// the pastes run in turn, as the per-session write lock runs them (§11.1).
// Overlapping pastes into ONE session are a different case: on 0.0.25 each
// attaches a transient client, meja routes a session's commands through its
// one current client (§2.10), and one paste tearing its client down drops
// another's command in flight. That race is still reachable and is recorded in
// known-issues.md; it is not what this case is about.
func TestConcurrentPastesDeliverTheirOwnText(t *testing.T) {
	requireMeja(t)
	b, socket := newBackend(t)
	ctx := context.Background()
	const sessions, rounds = 6, 6
	names := make([]string, sessions)
	for i := range names {
		names[i] = "p" + strconv.Itoa(i)
		if _, err := b.Create(ctx, backend.CreateSpec{Name: names[i], Dir: t.TempDir(), Cols: 120, Rows: 40, Command: []string{"cat"}}); err != nil {
			t.Fatalf("Create %s: %v", names[i], err)
		}
	}
	// A marker no other session's marker contains, so a count of one
	// session's marker in another's screen is exactly the crossed pastes.
	marker := func(i int) string { return "S" + strconv.Itoa(i) + "X" }
	var wg sync.WaitGroup
	errs := make(chan error, sessions*rounds)
	for i, name := range names {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for r := 0; r < rounds; r++ {
				errs <- meja.New(meja.WithSocketPath(socket)).Paste(ctx, name, marker(i)+strconv.Itoa(r)+" ")
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("Paste: %v", err)
		}
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		screens := make([]string, sessions)
		arrived := true
		for i, name := range names {
			scr, err := b.Screen(ctx, name, backend.ScreenOpts{})
			screens[i] = scr.Text
			total := 0
			for j := range names {
				total += strings.Count(scr.Text, marker(j))
			}
			if err != nil || total != rounds {
				arrived = false
			}
		}
		if arrived {
			for i, text := range screens {
				if n := strings.Count(text, marker(i)); n != rounds {
					t.Fatalf("a paste landed in another session: %s holds %d of its own\n%s", names[i], n, strings.Join(screens, "\n"))
				}
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the pastes never all arrived:\n%s", strings.Join(screens, "\n"))
		}
		time.Sleep(50 * time.Millisecond)
	}
	if out, _ := exec.Command("meja", "-S", socket, "list-buffers").CombinedOutput(); strings.TrimSpace(string(out)) != "" {
		t.Errorf("pastes left buffers behind:\n%s", out)
	}
}

// §1.3 The attach client strips the other multiplexers' identity and defaults
// LANG, and keeps the operator's own TERM.
func TestTheAttachClientGetsTheAttachEnvironment(t *testing.T) {
	requireMeja(t)
	b, _ := newBackend(t)
	ctx := context.Background()
	if _, err := b.Create(ctx, backend.CreateSpec{Name: "attenv", Dir: t.TempDir()}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	t.Setenv("TMUX", "/tmp/ambient,1,0")
	t.Setenv("ZMX_SESSION", "ambient")
	t.Setenv("LANG", "")
	t.Setenv("TERM", "operator-term")
	att, err := b.Attach(ctx, "attenv", backend.AttachSpec{Role: backend.RoleController})
	if err != nil {
		t.Fatalf("Attach: %v", err)
	}
	env := att.Cmd.Env
	if env == nil {
		t.Fatal("the attach client inherits the process environment unsanitized")
	}
	for _, kv := range env {
		if strings.HasPrefix(kv, "TMUX=") || strings.HasPrefix(kv, "ZMX_SESSION=") {
			t.Errorf("%s reached the attach client", kv)
		}
	}
	if !slices.Contains(env, "LANG=en_US.UTF-8") {
		t.Errorf("LANG was not defaulted for the attach client")
	}
	if !slices.Contains(env, "TERM=operator-term") {
		t.Errorf("the attach client lost the operator's TERM")
	}
}

// §2.8 Killing what is already gone is success, as on tmux and zmx: a session
// that is not there, on a server that is, is the desired state already.
func TestKillingAMissingSessionIsSuccess(t *testing.T) {
	requireMeja(t)
	b, _ := newBackend(t)
	ctx := context.Background()
	if _, err := b.Create(ctx, backend.CreateSpec{Name: "keep", Dir: t.TempDir()}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := b.Kill(ctx, "never-was"); err != nil {
		t.Errorf("killing a session that is not there is %v, want success", err)
	}
}

// §4.3: injected text stays off meja's argv, where every local user reads it in
// the process list while the call runs. The text reaches the server through a
// pipe that load-buffer reads.
//
// A meja ahead of the real one on PATH records every argument it is given, and
// each injection carries a text no argument may hold. The pipe's directory is
// gone after every call, the ones that failed included.
func TestInjectedTextIsOnNoArgv(t *testing.T) {
	requireMeja(t)
	log := recordArgv(t, "meja")
	// The socket's directory is made before TMPDIR moves to the test's own,
	// whose long path would take the socket past the 103 bytes one may have.
	b, _ := newBackend(t)
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	ctx := context.Background()
	const name = "argv"
	if _, err := b.Create(ctx, backend.CreateSpec{Name: name, Dir: t.TempDir(), Cols: 120, Rows: 40, Command: []string{"cat"}}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Short, so the line the three build does not wrap on a narrow pane.
	secret := "s" + strconv.FormatInt(time.Now().UnixNano()%1e6, 10)
	for _, step := range []struct {
		op string
		fn func() error
	}{
		{"type", func() error { return b.Type(ctx, name, secret+"-t ") }},
		{"paste", func() error { return b.Paste(ctx, name, secret+"-p ") }},
		{"atomic submit", func() error { return b.SendAtomic(ctx, name, secret+"-a") }},
	} {
		if err := step.fn(); err != nil {
			t.Fatalf("%s: %v", step.op, err)
		}
	}
	// cat echoes the submitted line once as typed and once as its output.
	want := secret + "-t " + secret + "-p " + secret + "-a"
	deadline := time.Now().Add(10 * time.Second)
	for {
		scr, err := b.Screen(ctx, name, backend.ScreenOpts{})
		if err == nil && strings.Count(scr.Text, want) == 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the text never arrived. Screen was:\n%s", scr.Text)
		}
		time.Sleep(50 * time.Millisecond)
	}
	if err := b.Type(ctx, "absent", secret); backend.CodeOf(err) != backend.CodeSessionNotFound {
		t.Errorf("typing into an absent session is %v, want not-found", err)
	}

	argv, err := os.ReadFile(log)
	if err != nil {
		t.Fatalf("reading the recorded argv: %v", err)
	}
	if strings.Contains(string(argv), secret) {
		t.Errorf("the injected text was on meja's argv:\n%s", argv)
	}
	if !strings.Contains(string(argv), "load-buffer") {
		t.Fatalf("the recording meja never loaded a buffer, so this proves nothing. It recorded:\n%s", argv)
	}
	if left, _ := filepath.Glob(filepath.Join(tmp, "olympus-meja-*")); len(left) > 0 {
		t.Errorf("a pipe's directory outlived its call: %v", left)
	}
}

// §4.3: Type and the atomic submit write the text's bytes as they are, as
// send-keys -l did: an ESC is not drawn as ^[ and a newline is not made a
// carriage return. The reader takes exactly the bytes typed and prints them. Its
// spacing is squeezed, since od pads its columns differently on macOS and Linux.
func TestTypedBytesArriveUnchanged(t *testing.T) {
	requireMeja(t)
	b, _ := newBackend(t)
	ctx := context.Background()
	const name = "bytes"
	if _, err := b.Create(ctx, backend.CreateSpec{Name: name, Dir: t.TempDir(), Cols: 80, Rows: 24,
		Command: []string{"sh", "-c", `stty -icanon -icrnl -echo; printf 'reading-%d\n' 4; head -c 4 | od -An -tx1 | tr -s ' '; exec cat`}}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	deadline := time.Now().Add(10 * time.Second)
	wait := func(want string) {
		t.Helper()
		for {
			scr, err := b.Screen(ctx, name, backend.ScreenOpts{})
			if err == nil && strings.Contains(scr.Text, want) {
				return
			}
			if time.Now().After(deadline) {
				t.Fatalf("waiting for %q: never appeared. Screen was:\n%s", want, scr.Text)
			}
			time.Sleep(50 * time.Millisecond)
		}
	}
	wait("reading-4")
	if err := b.Type(ctx, name, "x\x1by\n"); err != nil {
		t.Fatalf("Type: %v", err)
	}
	wait("78 1b 79 0a")
}

// §4.3: meja holds at most 8 MiB in a paste buffer, and says so. A text past it
// is the caller's input to fix, so it is usage, naming the limit, and leaves no
// buffer behind.
func TestATextPastThePasteBufferCapIsUsage(t *testing.T) {
	requireMeja(t)
	b, socket := newBackend(t)
	ctx := context.Background()
	const name = "cap"
	if _, err := b.Create(ctx, backend.CreateSpec{Name: name, Dir: t.TempDir(), Cols: 80, Rows: 24, Command: []string{"cat"}}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	err := b.Type(ctx, name, strings.Repeat("x", 8<<20+1))
	if backend.CodeOf(err) != backend.CodeUsage {
		t.Fatalf("a text past the paste buffer's cap is %v, want %s", err, backend.CodeUsage)
	}
	if !strings.Contains(err.Error(), "8388608") {
		t.Errorf("the refusal %q does not state the limit", err)
	}
	if out, _ := exec.Command("meja", "-S", socket, "list-buffers").CombinedOutput(); strings.TrimSpace(string(out)) != "" {
		t.Errorf("the refused text left a buffer behind:\n%s", out)
	}
}

// recordArgv puts a bin ahead of the real one on PATH that appends each
// argument it is given to a file, one per line, and then runs the real one.
// It returns the file's path.
func recordArgv(t *testing.T, bin string) string {
	t.Helper()
	real, err := exec.LookPath(bin)
	if err != nil {
		t.Skipf("%s is not installed", bin)
	}
	dir := t.TempDir()
	log := filepath.Join(dir, "argv")
	script := "#!/bin/sh\nfor a in \"$@\"; do printf '%s\\n' \"$a\" >> '" + log + "'; done\nexec '" + real + "' \"$@\"\n"
	if err := os.WriteFile(filepath.Join(dir, bin), []byte(script), 0o755); err != nil {
		t.Fatalf("writing the recording %s: %v", bin, err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return log
}
