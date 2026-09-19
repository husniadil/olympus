package tmux_test

import (
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/husniadil/olympus/backend"
)

// §10 A session target is exact. tmux reads a "." in `=a.b` as a pane
// separator, so a session whose name holds one has to be addressed with the
// window separator after it, or it cannot be probed or killed at all — and a
// kill that cannot find it reports success.
func TestASessionNameWithADotIsAddressable(t *testing.T) {
	b := newBackend(t)
	if !tmuxKeepsADotInASessionName(t) {
		t.Skip("this tmux rewrites a dot in a session name to an underscore, so there is no dotted name to address")
	}
	name := create(t, b, backend.CreateSpec{Name: "oly.dot"})
	ctx := context.Background()
	if got := b.Probe(ctx, name); got != backend.StatePresent {
		t.Fatalf("a live session named %s probes as %s", name, got)
	}
	if err := b.Kill(ctx, name); err != nil {
		t.Fatalf("killing %s: %v", name, err)
	}
	if got := b.Probe(ctx, name); got != backend.StateAbsent {
		t.Errorf("after a kill %s probes as %s: the kill reported success and left it running", name, got)
	}
}

// §5.1 A window the session lacks is SESSION_NOT_FOUND. tmux matches a window
// name by prefix, so without the exact form "sec" silently lands on "second".
func TestAWindowNameIsMatchedExactly(t *testing.T) {
	b := newBackend(t)
	name := create(t, b, backend.CreateSpec{Name: "oly-win"})
	ctx := context.Background()
	socket := socketOf(t, b)
	if out, err := exec.Command("tmux", "-S", socket, "rename-window", "-t", "="+name+":0", "second").CombinedOutput(); err != nil {
		t.Fatalf("naming the window: %v\n%s", err, out)
	}
	if _, err := b.Screen(ctx, name+":sec", backend.ScreenOpts{}); backend.CodeOf(err) != backend.CodeSessionNotFound {
		t.Errorf("capturing a window prefix is %q, want %q (err %v)", backend.CodeOf(err), backend.CodeSessionNotFound, err)
	}
	if err := b.(backend.Renamer).Rename(ctx, name+":sec", "x"); backend.CodeOf(err) != backend.CodeSessionNotFound {
		t.Errorf("renaming a window prefix is %q, want %q (err %v)", backend.CodeOf(err), backend.CodeSessionNotFound, err)
	}
	out, _ := exec.Command("tmux", "-S", socket, "list-windows", "-t", "="+name+":", "-F", "#W").CombinedOutput()
	if got := strings.TrimSpace(string(out)); got != "second" {
		t.Errorf("the window is now %q: an operation on a prefix reached it", got)
	}
	if _, err := b.Screen(ctx, name+":second", backend.ScreenOpts{}); err != nil {
		t.Errorf("capturing the window by its exact name: %v", err)
	}
	if _, err := b.Screen(ctx, name+":0", backend.ScreenOpts{}); err != nil {
		t.Errorf("capturing the window by its index: %v", err)
	}
}

// §12.3 A status belongs to the session named, exactly, and asking for the
// status of a session that does not exist is not-found rather than empty.
func TestAStatusAddressesItsSessionExactly(t *testing.T) {
	b := newBackend(t)
	name := create(t, b, backend.CreateSpec{Name: "oly-statx"})
	ctx := context.Background()
	if err := b.SetStatus(ctx, "oly-stat", "stray"); backend.CodeOf(err) != backend.CodeSessionNotFound {
		t.Errorf("setting a status on a prefix is %q, want %q (err %v)", backend.CodeOf(err), backend.CodeSessionNotFound, err)
	}
	if got, err := b.Status(ctx, name); err != nil || got != "" {
		t.Errorf("the longer-named session reads %q (err %v): a status meant for another landed on it", got, err)
	}
	if _, err := b.Status(ctx, "oly-missing"); backend.CodeOf(err) != backend.CodeSessionNotFound {
		t.Errorf("reading the status of a missing session is %q, want %q (err %v)", backend.CodeOf(err), backend.CodeSessionNotFound, err)
	}
}

// §2.2 A colon in a created session's name is refused before tmux runs. tmux
// 3.7c keeps it, and then no target can address the session: the chained
// set-option fails and so does the cleanup, leaking a live session behind a
// not-found error.
func TestACreatedSessionNameWithAColonIsUsageAndLeavesNothing(t *testing.T) {
	b := newBackend(t)
	ctx := context.Background()
	create(t, b, backend.CreateSpec{Name: "oly-keep"})
	_, err := b.Create(ctx, backend.CreateSpec{Name: "c:d", Cols: 80, Rows: 24, Dir: t.TempDir()})
	if backend.CodeOf(err) != backend.CodeUsage {
		t.Errorf("creating c:d is %q, want %q (err %v)", backend.CodeOf(err), backend.CodeUsage, err)
	}
	sessions, err := b.Sessions(ctx)
	if err != nil {
		t.Fatalf("Sessions: %v", err)
	}
	for _, s := range sessions {
		if s.Name != "oly-keep" {
			t.Errorf("a refused create left session %q behind", s.Name)
		}
	}
}

// §4.8 tmux reads every argument in a command line the same way: an unescaped
// trailing ";" is dropped, and a trailing `\;` loses its backslash. So the
// escape is unconditional, and it covers every argument a caller supplies, not
// only the chained send-keys text.
func TestATrailingSemicolonSurvivesEveryArgument(t *testing.T) {
	b := newBackend(t)
	ctx := context.Background()
	name := create(t, b, backend.CreateSpec{Name: "oly-semi2"})
	warm(t, b, name)

	if err := b.SendAtomic(ctx, name, `printf 'semi-%s\n' 'x\;'`+` \;`); err != nil {
		t.Fatalf("sending atomically: %v", err)
	}
	screen := waitForScreen(t, b, name, "semi-x")
	if !strings.Contains(screen, `' \;`) {
		t.Errorf("a trailing backslash-semicolon lost its backslash. Screen was:\n%s", screen)
	}

	if err := b.SetStatus(ctx, name, "busy;"); err != nil {
		t.Fatalf("SetStatus: %v", err)
	}
	if got, _ := b.Status(ctx, name); got != "busy;" {
		t.Errorf("a status ending in ; reads back as %q", got)
	}

	if err := b.(backend.Renamer).Rename(ctx, name, "oly-semi;"); err != nil {
		t.Fatalf("Rename: %v", err)
	}
	if got := b.Probe(ctx, "oly-semi;"); got != backend.StatePresent {
		t.Errorf("a session renamed to oly-semi; probes as %s", got)
	}

	spawned := create(t, b, backend.CreateSpec{Name: "oly-semi3", Command: []string{"sh", "-c", "echo spawned-$0; sleep 30", "arg;"}})
	waitForScreen(t, b, spawned, "spawned-arg;")
}

// §5.3 The alt-screen flag describes the pane the capture read: the window's
// active pane. list-panes on a pane target lists every pane in the window, so
// its first row is the first pane, not the active one.
func TestTheAltScreenFlagIsTheCapturedPanes(t *testing.T) {
	b := newBackend(t)
	ctx := context.Background()
	name := create(t, b, backend.CreateSpec{Name: "oly-split"})
	socket := socketOf(t, b)
	program := `printf '\033[?1049hALT-ON'; sleep 30`
	if out, err := exec.Command("tmux", "-S", socket, "split-window", "-t", "="+name+":", "sh", "-c", program).CombinedOutput(); err != nil {
		t.Fatalf("splitting: %v\n%s", err, out)
	}
	waitForScreen(t, b, name, "ALT-ON")
	capture, err := b.Screen(ctx, name, backend.ScreenOpts{})
	if err != nil {
		t.Fatalf("Screen: %v", err)
	}
	if !capture.Meta.AltScreen {
		t.Errorf("the active pane is on the alternate screen, but the capture says it is not")
	}
}

// Follow taps a pane with pipe-pane, and a pane has one pipe: a second tap
// silently replaces the first, whose reader then waits forever, and closing
// either turns off the other. A second follow is refused instead.
func TestASecondFollowOfOnePaneIsAConflict(t *testing.T) {
	b := newBackend(t)
	ctx := context.Background()
	name := create(t, b, backend.CreateSpec{Name: "oly-follow2"})
	first, err := b.Follow(ctx, name)
	if err != nil {
		t.Fatalf("Follow: %v", err)
	}
	defer first.Close()
	if second, err := b.Follow(ctx, name); backend.CodeOf(err) != backend.CodeConflict {
		if second != nil {
			_ = second.Close()
		}
		t.Errorf("a second follow is %q, want %q (err %v)", backend.CodeOf(err), backend.CodeConflict, err)
	}
}

// Watch stops "when the session ends", so the stream ends with it, as it does
// on zmx and meja, rather than waiting at the end of its file forever.
func TestAFollowEndsWhenTheSessionDoes(t *testing.T) {
	b := newBackend(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	name := create(t, b, backend.CreateSpec{Name: "oly-follow-end"})
	stream, err := b.Follow(ctx, name)
	if err != nil {
		t.Fatalf("Follow: %v", err)
	}
	defer stream.Close()
	if err := b.Kill(ctx, name); err != nil {
		t.Fatalf("Kill: %v", err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := io.Copy(io.Discard, stream)
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("the stream ended with %v, want a clean end", err)
		}
	case <-time.After(10 * time.Second):
		t.Error("the stream is still open ten seconds after its session ended")
	}
}

// The tap's shell fragment names its file, so the path is quoted: a TMPDIR
// with a space in it would otherwise split it.
func TestAFollowSurvivesATempDirWithASpace(t *testing.T) {
	b := newBackend(t)
	ctx := context.Background()
	dir := filepath.Join(t.TempDir(), "with space")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TMPDIR", dir)
	name := create(t, b, backend.CreateSpec{Name: "oly-follow-sp"})
	warm(t, b, name)
	stream, err := b.Follow(ctx, name)
	if err != nil {
		t.Fatalf("Follow: %v", err)
	}
	defer stream.Close()
	if err := b.SendAtomic(ctx, name, "echo tapped-$((6*7))"); err != nil {
		t.Fatalf("SendAtomic: %v", err)
	}
	got := make(chan string, 1)
	go func() {
		var seen strings.Builder
		buf := make([]byte, 4096)
		for {
			n, err := stream.Read(buf)
			seen.Write(buf[:n])
			if strings.Contains(seen.String(), "tapped-42") || err != nil {
				got <- seen.String()
				return
			}
		}
	}()
	select {
	case s := <-got:
		if !strings.Contains(s, "tapped-42") {
			t.Errorf("the stream ended without the output: %q", s)
		}
	case <-time.After(10 * time.Second):
		t.Error("nothing reached the stream: the tap did not write where the reader reads")
	}
}

// §9.5 A view names its base by the base's current name. tmux's
// #{session_group} keeps the name the group was created with, so after the
// base is renamed the group name names a session that no longer exists.
func TestAViewFollowsItsBaseThroughARename(t *testing.T) {
	b := newBackend(t)
	ctx := context.Background()
	base := create(t, b, backend.CreateSpec{Name: "oly-zeta"})
	if _, err := b.CreateView(ctx, base, backend.ViewSpec{Name: "olympus-view-oly-zeta-1"}); err != nil {
		t.Fatalf("CreateView: %v", err)
	}
	if err := b.(backend.Renamer).Rename(ctx, base, "oly-zeta2"); err != nil {
		t.Fatalf("Rename: %v", err)
	}
	views, err := b.Views(ctx, "oly-zeta2")
	if err != nil {
		t.Fatalf("Views: %v", err)
	}
	if len(views) != 1 || views[0].Base != "oly-zeta2" {
		t.Errorf("views of the renamed base are %+v, want one naming oly-zeta2", views)
	}
}

// §2.9 The test process's HOME is private, so a pane's login shell reads no
// operator profile.
func TestAPaneRunsUnderThePrivateTestHome(t *testing.T) {
	b := newBackend(t)
	name := create(t, b, backend.CreateSpec{Name: "oly-home"})
	if err := b.SendAtomic(context.Background(), name, `echo "home-is-$HOME"`); err != nil {
		t.Fatalf("SendAtomic: %v", err)
	}
	screen := waitForScreen(t, b, name, "home-is-/")
	if !strings.Contains(screen, "home-is-"+os.Getenv("HOME")) || !strings.Contains(os.Getenv("HOME"), "olyhome") {
		t.Errorf("the pane's HOME is not the private test HOME %q:\n%s", os.Getenv("HOME"), screen)
	}
}

// tmuxKeepsADotInASessionName asks a private server what it names a session
// created as "a.b": tmux 3.4 stores "a_b", and 3.7c keeps the dot.
func tmuxKeepsADotInASessionName(t *testing.T) bool {
	t.Helper()
	// Short and outside t.TempDir, whose path carries the test's name and
	// overruns a socket path's byte budget.
	dir, err := os.MkdirTemp(os.TempDir(), "olyp")
	if err != nil {
		t.Fatalf("creating a probe socket directory: %v", err)
	}
	defer os.RemoveAll(dir)
	socket := filepath.Join(dir, "p.sock")
	run := func(args ...string) string {
		out, _ := exec.Command("tmux", append([]string{"-S", socket}, args...)...).Output()
		return strings.TrimSpace(string(out))
	}
	defer run("kill-server")
	run("new-session", "-d", "-s", "a.b")
	return run("list-sessions", "-F", "#{session_name}") == "a.b"
}
