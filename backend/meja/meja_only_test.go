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
func TestConcurrentPastesDeliverTheirOwnText(t *testing.T) {
	requireMeja(t)
	b, socket := newBackend(t)
	ctx := context.Background()
	for _, name := range []string{"pa", "pb"} {
		if _, err := b.Create(ctx, backend.CreateSpec{Name: name, Dir: t.TempDir(), Cols: 120, Rows: 40, Command: []string{"cat"}}); err != nil {
			t.Fatalf("Create %s: %v", name, err)
		}
	}
	const rounds = 6
	var wg sync.WaitGroup
	errs := make(chan error, 2*rounds)
	for i := 0; i < rounds; i++ {
		for _, p := range []struct{ target, text string }{{"pa", "AAA" + strconv.Itoa(i) + " "}, {"pb", "BBB" + strconv.Itoa(i) + " "}} {
			wg.Add(1)
			go func() {
				defer wg.Done()
				errs <- meja.New(meja.WithSocketPath(socket)).Paste(ctx, p.target, p.text)
			}()
		}
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
		a, errA := b.Screen(ctx, "pa", backend.ScreenOpts{})
		c, errB := b.Screen(ctx, "pb", backend.ScreenOpts{})
		if errA == nil && errB == nil && strings.Count(a.Text, "AAA")+strings.Count(a.Text, "BBB") == rounds &&
			strings.Count(c.Text, "AAA")+strings.Count(c.Text, "BBB") == rounds {
			if strings.Contains(a.Text, "BBB") || strings.Contains(c.Text, "AAA") {
				t.Fatalf("a paste landed in the other session:\npa: %s\npb: %s", a.Text, c.Text)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the pastes never all arrived (errs %v %v):\npa: %s\npb: %s", errA, errB, a.Text, c.Text)
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
