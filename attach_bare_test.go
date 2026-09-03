//go:build darwin || linux

package olympus

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/husniadil/olympus/backend"
)

// A bare attach has two meanings and two homes — herdr's chrome-hidden session
// client and tmux's throwaway view — and none anywhere else. zmx and meja draw
// no chrome to hide and have no views, so the refusal fires on the spec, before
// anything is spawned; the nil terminal files are never reached.
func TestBareAttachIsRefusedWhereNothingCanBeBare(t *testing.T) {
	for _, name := range []backend.Name{backend.Zmx, backend.Meja} {
		ol := fakeOlympus(&fakeBackend{caps: backend.Capabilities{Backend: name}})
		s := ol.OpenSessionName("whatever")

		_, err := s.Attach(context.Background(), nil, nil, nil, AsBare())
		if err == nil {
			t.Fatalf("a bare attach on %s succeeded", name)
		}
		if backend.CodeOf(err) != backend.CodeUnsupported {
			t.Errorf("bare attach on %s is %q, want %q", name, backend.CodeOf(err), backend.CodeUnsupported)
		}
	}
}

// tmux rewrites a colon out of any session name it is given, so the first
// colon is the session's end and everything after it is the window, whole — a
// window name may itself carry one.
func TestSplitBareTargetCutsAtTheFirstColon(t *testing.T) {
	cases := []struct{ target, session, window string }{
		{"build", "build", ""},
		{"build:1", "build", "1"},
		{"build:second", "build", "second"},
		{"build:a:b", "build", "a:b"},
		{"build:", "build", ""},
	}
	for _, c := range cases {
		session, window := splitBareTarget(c.target)
		if session != c.session || window != c.window {
			t.Errorf("splitBareTarget(%q) = (%q, %q), want (%q, %q)", c.target, session, window, c.session, c.window)
		}
	}
}

// §17.1: a view's name is `olympus-view-<base>-<nonce>`. Enumerating views
// selects on the prefix, so a bare attach's throwaway view must carry it or the
// view would be invisible to `view ls` and to any sweep.
func TestViewNameCarriesTheReservedShape(t *testing.T) {
	name := viewName("build")
	rest, ok := strings.CutPrefix(name, "olympus-view-build-")
	if !ok || len(rest) != 8 {
		t.Errorf("viewName(build) = %q, want olympus-view-build-<8 hex>", name)
	}
}

// A bare attach on tmux attaches a VIEW, not the session: a fresh grouped
// session that keeps its own current window, so the base and every other
// client stay where they were (§8.9). Asserted on the prepared attachment
// rather than by running it, since running an attach needs a terminal — the
// argv names the view, the view exists and shows the pinned window, and the
// attachment's cleanup takes the view with it (§8.8).
func TestABareAttachOnTmuxOpensAViewPinnedToTheWindow(t *testing.T) {
	if testing.Short() {
		t.Skip("driving a real multiplexer; run `make test-full` for this")
	}
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is not installed")
	}

	// A private socket PATH, torn down with the case (§2.9).
	dir, err := os.MkdirTemp(os.TempDir(), "olyb")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	socket := filepath.Join(dir, "s.sock")
	t.Cleanup(func() { _ = exec.Command("tmux", "-S", socket, "kill-server").Run() })
	tmux := func(args ...string) string {
		t.Helper()
		out, err := exec.Command("tmux", append([]string{"-S", socket}, args...)...).Output()
		if err != nil {
			t.Fatalf("tmux %s: %v", strings.Join(args, " "), err)
		}
		return strings.TrimSpace(string(out))
	}

	ol, err := Open(WithBackend("tmux"), WithSocketPath(socket))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = ol.Close() })
	ctx := context.Background()
	if _, err := ol.Create(ctx, "base", In(t.TempDir())); err != nil {
		t.Fatalf("Create: %v", err)
	}
	// A second window, NOT selected: the base keeps showing window 0.
	tmux("new-window", "-d", "-t", "=base:", "-n", "second")
	// The base's current pane, the one thing a view cannot choose privately.
	basePane := tmux("display-message", "-p", "-t", "=base:", "#{pane_id}")

	spec := backend.AttachSpec{Role: backend.RoleViewer, Supersede: true, Bare: true}
	att, err := ol.OpenSessionName("base:second").prepareBareView(ctx, spec)
	if err != nil {
		t.Fatalf("preparing a bare attach: %v", err)
	}

	views, err := ol.Views(ctx, "base")
	if err != nil {
		t.Fatalf("Views: %v", err)
	}
	if len(views) != 1 {
		t.Fatalf("a bare attach left %d views on the base, want exactly one: %+v", len(views), views)
	}
	view := views[0].Name
	if !strings.HasPrefix(view, "olympus-view-base-") {
		t.Errorf("the attach's view is named %q, which is outside the reserved shape", view)
	}

	args := strings.Join(att.Cmd.Args, " ")
	if !strings.Contains(args, "attach-session -t ="+view) {
		t.Errorf("the attach argv does not target the view:\n  %s", args)
	}
	if !strings.Contains(args, " -r") {
		t.Errorf("a viewer bare attach is not read-only: %s", args)
	}

	if got := tmux("display-message", "-p", "-t", "="+view+":", "#{window_index} #{window_name}"); got != "1 second" {
		t.Errorf("the view shows window %q, want %q", got, "1 second")
	}
	if got := tmux("display-message", "-p", "-t", "=base:", "#{window_index} #{pane_id}"); got != "0 "+basePane {
		t.Errorf("the base was moved by a bare attach onto another window: now at %q, was at %q", got, "0 "+basePane)
	}

	if err := att.Close(); err != nil {
		t.Errorf("the attachment's cleanup failed: %v", err)
	}
	views, err = ol.Views(ctx, "base")
	if err != nil {
		t.Fatalf("Views after cleanup: %v", err)
	}
	if len(views) != 0 {
		t.Errorf("the view outlived its attach: %+v", views)
	}
	if state := ol.backend.Probe(ctx, "base"); state != backend.StatePresent {
		t.Errorf("reaping the view took the base with it: base is %q", state)
	}

	// A window the base does not have is not-found, and creates nothing.
	_, err = ol.OpenSessionName("base:9").prepareBareView(ctx, spec)
	if backend.CodeOf(err) != backend.CodeSessionNotFound {
		t.Errorf("a bare attach onto a missing window is %q, want %q", backend.CodeOf(err), backend.CodeSessionNotFound)
	}
	if views, _ := ol.Views(ctx, "base"); len(views) != 0 {
		t.Errorf("a refused bare attach left a view behind: %+v", views)
	}
}
