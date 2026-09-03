package herdr

import (
	"context"
	"fmt"
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

// §8.10 Focus steers the server without attaching: after it, the server's
// focused workspace is the target's, which is what every session client on
// that server will show. Steered onto the second workspace and back, so each
// step has to change the server's answer.
func TestFocusSteersTheServerOntoAWorkspace(t *testing.T) {
	requireHerdrRunnable(t)
	h := liveBackend(t)
	ctx := context.Background()
	first, err := h.Create(ctx, backend.CreateSpec{Name: "first"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	second, err := h.Create(ctx, backend.CreateSpec{Name: "second"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	focused := func() string {
		t.Helper()
		snap, err := h.snapshot(ctx)
		if err != nil {
			t.Fatalf("snapshot: %v", err)
		}
		return snap.FocusedWorkspaceID
	}
	for _, ws := range []backend.Session{second, first} {
		r, err := h.resolve(ctx, ws.Name)
		if err != nil {
			t.Fatalf("resolving %s: %v", ws.Name, err)
		}
		if focused() == r.workspace.WorkspaceID {
			t.Fatalf("the server already focuses %s, so steering onto it would prove nothing", ws.Name)
		}
		if err := h.Focus(ctx, ws.Name); err != nil {
			t.Fatalf("Focus %s: %v", ws.Name, err)
		}
		if got := focused(); got != r.workspace.WorkspaceID {
			t.Errorf("after Focus %s the server focuses %q, want %q", ws.Name, got, r.workspace.WorkspaceID)
		}
	}
	if err := h.Focus(ctx, "w0Z"); backend.CodeOf(err) != backend.CodeSessionNotFound {
		t.Errorf("focusing a workspace that does not exist is %q, want %q (err %v)", backend.CodeOf(err), backend.CodeSessionNotFound, err)
	}
}

// §2.11 Rename relabels the level the target names, and the listing shows the
// new label: a workspace answers to its label, so the session is then found
// under the new name and not the old. A label shaped like an id is refused.
func TestRenameRelabelsAWorkspaceAndAPane(t *testing.T) {
	requireHerdrRunnable(t)
	h := liveBackend(t)
	ctx := context.Background()
	if _, err := h.Create(ctx, backend.CreateSpec{Name: "before"}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := h.Rename(ctx, "before", "after"); err != nil {
		t.Fatalf("Rename: %v", err)
	}
	if h.Probe(ctx, "after") != backend.StatePresent || h.Probe(ctx, "before") != backend.StateAbsent {
		t.Errorf("after the rename the workspace answers to before=%s after=%s, want absent/present",
			h.Probe(ctx, "before"), h.Probe(ctx, "after"))
	}
	panes, err := h.Panes(ctx, "after")
	if err != nil || len(panes) == 0 {
		t.Fatalf("Panes: %v (%d panes)", err, len(panes))
	}
	if err := h.Rename(ctx, panes[0].ID, "shell"); err != nil {
		t.Errorf("renaming a pane: %v", err)
	}
	tabID := fmt.Sprintf("%s:t%d", panes[0].SessionID, panes[0].WindowIndex)
	if err := h.Rename(ctx, tabID, "main"); err != nil {
		t.Errorf("renaming a tab: %v", err)
	}
	// Both names come back on the pane row.
	panes, err = h.Panes(ctx, "after")
	if err != nil || len(panes) == 0 {
		t.Fatalf("Panes after rename: %v (%d panes)", err, len(panes))
	}
	if panes[0].Title != "shell" || panes[0].WindowName != "main" {
		t.Errorf("the pane row reports title %q window %q, want shell/main", panes[0].Title, panes[0].WindowName)
	}
	if err := h.Rename(ctx, "after", "w9:p9"); backend.CodeOf(err) != backend.CodeUsage {
		t.Errorf("a label shaped like a pane id is %q, want %q (err %v)", backend.CodeOf(err), backend.CodeUsage, err)
	}
	if err := h.Rename(ctx, "nope", "x"); backend.CodeOf(err) != backend.CodeSessionNotFound {
		t.Errorf("renaming a missing target is %q, want %q (err %v)", backend.CodeOf(err), backend.CodeSessionNotFound, err)
	}
}
