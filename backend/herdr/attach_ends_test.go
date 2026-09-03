package herdr

import (
	"context"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/husniadil/olympus/backend"
	"github.com/husniadil/olympus/internal/engine"
)

// §8.10 A session-client attach ends with its target. The client is attached
// to the whole session, so when the workspace it was steered onto closes,
// herdr moves it to another workspace rather than ending it; the attach must
// end anyway, and the client process must be gone. Run through the engine
// with a pipe for stdin and the size given explicitly, the way a caller with
// no terminal attaches.
//
// A second workspace exists so the server has somewhere to move the focus —
// the shape the defect was measured in.
func TestSessionClientAttachEndsWhenTheWorkspaceCloses(t *testing.T) {
	requireHerdrRunnable(t)
	b := liveBackend(t)
	ctx := context.Background()
	if _, err := b.Create(ctx, backend.CreateSpec{Name: "elsewhere"}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	created, err := b.Create(ctx, backend.CreateSpec{Name: "doomed"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	att, err := b.Attach(ctx, created.Name, backend.AttachSpec{
		Role:          backend.RoleController,
		Supersede:     true,
		SessionClient: true,
		Bare:          true,
	})
	if err != nil {
		t.Fatalf("bare session-client Attach: %v", err)
	}
	if att.Probe == nil {
		t.Fatal("a session-client attach carries no Probe, so nothing would end it with its target")
	}
	if state := att.Probe(ctx); state != backend.StatePresent {
		t.Fatalf("the attachment's Probe answers %q for a workspace that exists", state)
	}

	stdinR, stdinW, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	defer stdinW.Close()
	defer stdinR.Close()
	devnull, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		t.Fatalf("opening %s: %v", os.DevNull, err)
	}
	defer devnull.Close()
	errOut := &strings.Builder{}

	type result struct {
		code int
		err  error
	}
	results := make(chan result, 1)
	go func() {
		code, err := engine.Attach(ctx, att, engine.AttachIO{In: stdinR, Out: devnull, Err: errOut},
			backend.AttachSpec{Role: backend.RoleController, Cols: 80, Rows: 24}, nil)
		results <- result{code, err}
	}()

	// The attach stays up while its workspace exists. The engine returns the
	// moment the client exits, so an attach still running IS a running
	// client; the process itself is not touched here because the engine
	// owns its Cmd until the attach returns.
	select {
	case r := <-results:
		t.Fatalf("the attach ended before the workspace closed: %d, %v", r.code, r.err)
	case <-time.After(3 * time.Second):
	}

	raw(t, b, "workspace", "close", created.ID)

	var r result
	select {
	case r = <-results:
	case <-time.After(10 * time.Second):
		t.Fatal("the attach did not end within 10s of its workspace closing")
	}
	if r.err != nil || r.code != 0 {
		t.Errorf("an attach ended by its target exits %d, %v; want 0 with no error", r.code, r.err)
	}
	if !strings.Contains(errOut.String(), "detached: the target is gone") {
		t.Errorf("no notice about the target on the narration channel: %q", errOut.String())
	}
	// Asked of the kernel rather than of the Cmd, which already believes
	// its process is done once it has reaped it.
	pid := strconv.Itoa(att.Cmd.Process.Pid)
	if out, _ := exec.Command("ps", "-p", pid, "-o", "pid=").Output(); strings.TrimSpace(string(out)) != "" {
		t.Errorf("the client (pid %s) is still alive after the attach ended:\n%s", pid, out)
	}
	if b.Probe(ctx, "elsewhere") != backend.StatePresent {
		t.Error("ending the attach took the other workspace with it")
	}
}

// §8.10 The raw pane attach already ends with its pane — herdr's own client
// exits when the terminal it streams closes — so it carries no Probe.
func TestRawAttachCarriesNoProbe(t *testing.T) {
	requireHerdrRunnable(t)
	b := liveBackend(t)
	ctx := context.Background()
	created, err := b.Create(ctx, backend.CreateSpec{Name: "raw"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	att, err := b.Attach(ctx, created.Name, backend.AttachSpec{Role: backend.RoleController, Supersede: true})
	if err != nil {
		t.Fatalf("Attach: %v", err)
	}
	if att.Probe != nil {
		t.Error("the raw pane attach carries a Probe it does not need")
	}
}
