package tmux_test

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/husniadil/olympus/backend"
	"github.com/husniadil/olympus/backend/tmux"
)

// The rules in this file are real and specified, but invisible through the
// Backend interface: they are about the argv Olympus builds and the state it
// leaves on the tmux server. The conformance suite cannot see any of it.

func newBackend(t *testing.T) backend.Backend {
	t.Helper()
	requireTmux(t)
	return newIsolated(t)
}

func create(t *testing.T, b backend.Backend, spec backend.CreateSpec) string {
	t.Helper()
	if spec.Name == "" {
		spec.Name = "oly-only"
	}
	if spec.Cols == 0 {
		spec.Cols, spec.Rows = 80, 24
	}
	if spec.Dir == "" {
		spec.Dir = t.TempDir()
	}
	if _, err := b.Create(context.Background(), spec); err != nil {
		t.Fatalf("creating %s: %v", spec.Name, err)
	}
	return spec.Name
}

func waitForScreen(t *testing.T, b backend.Backend, target, want string) string {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	var last string
	for {
		capture, err := b.Screen(context.Background(), target, backend.ScreenOpts{})
		if err == nil {
			last = capture.Text
			if strings.Contains(last, want) {
				return last
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("waiting for %q on %s: never appeared. Screen was:\n%s", want, target, last)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// warm blocks until the shell has provably executed a command. Expansion-based
// for the reason §16 gives: the typed line is echoed verbatim, so only the
// substituted output distinguishes execution from echo.
func warm(t *testing.T, b backend.Backend, target string) {
	t.Helper()
	ctx := context.Background()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if err := b.Type(ctx, target, `printf 'ready-%d\n' 7`); err != nil {
			t.Fatalf("warming: %v", err)
		}
		if err := b.Submit(ctx, target); err != nil {
			t.Fatalf("warming: %v", err)
		}
		end := time.Now().Add(2 * time.Second)
		for time.Now().Before(end) {
			capture, err := b.Screen(ctx, target, backend.ScreenOpts{})
			if err == nil && strings.Contains(capture.Text, "ready-7") {
				return
			}
			time.Sleep(50 * time.Millisecond)
		}
	}
	t.Fatalf("the shell never executed a command")
}

// §2.2: options MUST be chained into the same new-session invocation.
//
// This race fails intermittently when reverted, so a single green run proves
// nothing (§16). The reproduction is a command that exits before a second tmux
// invocation could possibly run: with the options chained, the corpse is pinned
// and the session survives to be inspected; issued separately, the second call
// finds no such window and remain-on-exit does nothing for exactly the
// fastest-failing commands a caller most wants a corpse for.
func TestChainedOptionsSurviveAnInstantlyExitingCommand(t *testing.T) {
	b := newBackend(t)
	ctx := context.Background()

	const rounds = 25
	for i := 0; i < rounds; i++ {
		name := create(t, b, backend.CreateSpec{
			Name:         "oly-fast-" + itoa(i),
			Command:      []string{"sh", "-c", "exit 7"},
			RemainOnExit: true,
		})

		if state := b.Probe(ctx, name); state != backend.StatePresent {
			t.Fatalf("round %d: the session vanished despite remain-on-exit, so the corpse was never pinned (state %q)", i, state)
		}

		// The corpse is the observable: remain-on-exit is write-only, and the
		// only way to see it took effect is the dead row it leaves (§2.7).
		deadline := time.Now().Add(5 * time.Second)
		var dead bool
		for !dead && time.Now().Before(deadline) {
			panes, err := b.Panes(ctx, name)
			if err != nil {
				t.Fatalf("round %d: listing panes: %v", i, err)
			}
			for _, p := range panes {
				dead = dead || p.Dead
			}
			if !dead {
				time.Sleep(20 * time.Millisecond)
			}
		}
		if !dead {
			t.Fatalf("round %d: the pane is not marked dead, so remain-on-exit did not land", i)
		}
		if err := b.Kill(ctx, name); err != nil {
			t.Fatalf("round %d: killing: %v", i, err)
		}
	}
}

// §4.8: tmux's ";" chaining separator eats an unescaped TRAILING semicolon in a
// text argv element.
//
// The observable is the PTY's echo of the typed line: the characters that
// actually reached the pane are painted on screen, so a dropped ";" is visible
// there whether or not the command's behavior would have changed.
func TestTrailingSemicolonSurvivesTheChainedSendKeysPath(t *testing.T) {
	b := newBackend(t)
	name := create(t, b, backend.CreateSpec{Name: "oly-semi"})
	warm(t, b, name)

	if err := b.SendAtomic(context.Background(), name, `printf 'semi-%d\n' 1;`); err != nil {
		t.Fatalf("sending atomically: %v", err)
	}

	screen := waitForScreen(t, b, name, "semi-1")
	if !strings.Contains(screen, "1;") {
		t.Errorf("the trailing semicolon was eaten by tmux's command separator. Screen was:\n%s", screen)
	}
}

// §4.2: paste-buffer -d deletes the buffer only when the paste SUCCEEDS.
//
// Reproduced by pasting at a target that does not exist: load-buffer takes no
// target and succeeds, then paste-buffer fails, and without the explicit
// best-effort cleanup on that path the buffer leaks forever.
func TestAFailedPasteLeavesNoBufferBehind(t *testing.T) {
	b := newBackend(t)
	// An anchor keeps the server up so the buffer list is still askable after
	// the failure (§16).
	anchor := create(t, b, backend.CreateSpec{Name: "oly-anchor"})
	_ = anchor

	err := b.Type(context.Background(), "oly-does-not-exist", "some text")
	if err == nil {
		t.Fatal("typing into an absent session succeeded")
	}

	buffers := listBuffers(t, b)
	if buffers != "" {
		t.Errorf("a buffer leaked after the paste failed:\n%s", buffers)
	}
}

// §4.1: the buffer name MUST be unique per call. Two concurrent injections
// sharing a name race — one call's load-buffer clobbers the other's text before
// paste-buffer consumes it — so this drives them concurrently and repeatedly
// rather than asserting the name's shape.
func TestConcurrentInjectionsDoNotClobberEachOther(t *testing.T) {
	b := newBackend(t)
	ctx := context.Background()

	const sessions = 4
	names := make([]string, sessions)
	for i := range names {
		names[i] = create(t, b, backend.CreateSpec{Name: "oly-conc-" + itoa(i)})
		warm(t, b, names[i])
	}

	var wg sync.WaitGroup
	for i, name := range names {
		wg.Add(1)
		go func(i int, name string) {
			defer wg.Done()
			text := `printf 'conc` + itoa(i) + `-%d\n' ` + itoa(i)
			if err := b.Type(ctx, name, text); err != nil {
				t.Errorf("typing into %s: %v", name, err)
				return
			}
			if err := b.Submit(ctx, name); err != nil {
				t.Errorf("submitting to %s: %v", name, err)
			}
		}(i, name)
	}
	wg.Wait()

	for i, name := range names {
		want := "conc" + itoa(i) + "-" + itoa(i)
		screen := waitForScreen(t, b, name, want)
		// Another session's payload arriving here is precisely the clobber.
		for j := range names {
			if j == i {
				continue
			}
			if strings.Contains(screen, "conc"+itoa(j)+"-"+itoa(j)) {
				t.Errorf("session %s received session %d's text, so the buffers collided", name, j)
			}
		}
	}
}

// §1.2: the tmux server's global environment is a second leak, so new-session
// must also pass sanitized values per-session with -e.
//
// Only the ZMX_* strip and the LANG default are asserted. tmux re-sets TMUX,
// TMUX_PANE and forces TERM inside its own panes regardless of what is passed,
// so asserting those would produce a test that passes for the wrong reason —
// the note §1.2 marks backend-local.
func TestSpawnedSessionsGetASanitizedEnvironment(t *testing.T) {
	b := newBackend(t)
	name := create(t, b, backend.CreateSpec{Name: "oly-env"})
	warm(t, b, name)

	ctx := context.Background()
	// The marker is substituted, so it cannot appear in the echoed format
	// string. Waiting on a fragment that both lines share would match the echo
	// and prove only that the line was typed (§16).
	if err := b.Type(ctx, name, `printf 'env%d zmx=[%s] lang=[%s]\n' 9 "$ZMX_SESSION" "$LANG"`); err != nil {
		t.Fatalf("typing: %v", err)
	}
	if err := b.Submit(ctx, name); err != nil {
		t.Fatalf("submitting: %v", err)
	}

	screen := waitForScreen(t, b, name, "env9 ")
	line := lastMatching(screen, "env9 ")
	if !strings.Contains(line, "zmx=[]") {
		t.Errorf("ZMX_SESSION leaked into the session: %q", line)
	}
	if strings.Contains(line, "lang=[]") {
		t.Errorf("LANG was not defaulted: %q", line)
	}
}

// §5.1: -J MUST be dropped whenever history is requested.
//
// -J rejoins a line tmux auto-wrapped at the pane's width. That is correct on
// the live viewport, and wrong across scrollback, where it merges two separate
// history lines that never appeared as one on screen. The observable is a line
// longer than the pane: rejoined in the viewport capture, still split in the
// history one.
// §5.1 A capture target may name a window: `<session>:<window>` reads that
// window's active pane, which is what a reader of a view pinned to it (§8.9)
// needs, and which the session's own active pane may not be showing. A window
// the session lacks is not-found, and the plain session form is unchanged.
func TestACaptureTargetMayNameAWindow(t *testing.T) {
	b := newBackend(t)
	name := create(t, b, backend.CreateSpec{Name: "oly-win"})
	warm(t, b, name)
	ctx := context.Background()

	socket := socketOf(t, b)
	tmux := func(args ...string) {
		t.Helper()
		if out, err := exec.Command("tmux", append([]string{"-S", socket}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("tmux %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}
	tmux("new-window", "-d", "-t", "="+name+":", "-n", "second")
	tmux("send-keys", "-t", "="+name+":second.", "printf 'in-window-%d\\n' 2", "Enter")
	waitForScreen(t, b, name+":second", "in-window-2")

	byName, err := b.Screen(ctx, name+":second", backend.ScreenOpts{})
	if err != nil {
		t.Fatalf("capturing by window name: %v", err)
	}
	byIndex, err := b.Screen(ctx, name+":1", backend.ScreenOpts{})
	if err != nil {
		t.Fatalf("capturing by window index: %v", err)
	}
	session, err := b.Screen(ctx, name, backend.ScreenOpts{})
	if err != nil {
		t.Fatalf("capturing the session: %v", err)
	}
	for label, text := range map[string]string{"by name": byName.Text, "by index": byIndex.Text} {
		if !strings.Contains(text, "in-window-2") {
			t.Errorf("the capture %s does not show the second window:\n%s", label, text)
		}
	}
	if strings.Contains(session.Text, "in-window-2") {
		t.Errorf("the session capture shows the unselected window, so the window shape leaked into the plain form:\n%s", session.Text)
	}
	if _, err := b.Screen(ctx, name+":nine", backend.ScreenOpts{}); backend.CodeOf(err) != backend.CodeSessionNotFound {
		t.Errorf("a window the session lacks is %q, want %q (err %v)", backend.CodeOf(err), backend.CodeSessionNotFound, err)
	}
	if _, err := b.ScreenMeta(ctx, name+":second"); err != nil {
		t.Errorf("metadata for a window target: %v", err)
	}
}

func TestHistoryCaptureDoesNotRejoinWrappedLines(t *testing.T) {
	b := newBackend(t)
	name := create(t, b, backend.CreateSpec{Name: "oly-wrap"})
	warm(t, b, name)

	ctx := context.Background()
	// 200 characters into an 80-column pane: tmux wraps it across three rows.
	if err := b.Type(ctx, name, `printf 'W%.0s' $(seq 1 200); printf '\nwrapped-%d\n' 1`); err != nil {
		t.Fatalf("typing: %v", err)
	}
	if err := b.Submit(ctx, name); err != nil {
		t.Fatalf("submitting: %v", err)
	}
	waitForScreen(t, b, name, "wrapped-1")

	viewport, err := b.Screen(ctx, name, backend.ScreenOpts{})
	if err != nil {
		t.Fatalf("capturing the viewport: %v", err)
	}
	history, err := b.Screen(ctx, name, backend.ScreenOpts{HistoryLines: 500})
	if err != nil {
		t.Fatalf("capturing with history: %v", err)
	}

	if longestRun(viewport.Text) < 200 {
		t.Errorf("the viewport capture did not rejoin the wrapped line, so -J was not applied: longest run was %d", longestRun(viewport.Text))
	}
	if got := longestRun(history.Text); got >= 200 {
		t.Errorf("the history capture rejoined a wrapped line, so -J was not dropped: longest run was %d", got)
	}
}

// §1.4: the attach client needs -u, or the CLIENT sanitizes every non-ASCII
// byte to "_" before it reaches the consumer. The pane is fine; the stream is
// not. Asserted on the argv, since running an attach needs a terminal.
func TestAttachArgvCarriesTheFlagsTheStreamDependsOn(t *testing.T) {
	b := newBackend(t)
	name := create(t, b, backend.CreateSpec{Name: "oly-attach"})

	att, err := b.Attach(context.Background(), name, backend.AttachSpec{Role: backend.RoleController})
	if err != nil {
		t.Fatalf("preparing an attach: %v", err)
	}
	args := strings.Join(att.Cmd.Args, " ")
	if !strings.Contains(args, " -u") {
		t.Errorf("the attach argv has no -u, so the client will sanitize non-ASCII bytes to underscores: %s", args)
	}

	viewer, err := b.Attach(context.Background(), name, backend.AttachSpec{Role: backend.RoleViewer})
	if err != nil {
		t.Fatalf("preparing a viewer attach: %v", err)
	}
	// §8.7: a viewer drops input, and must drop resize with it.
	if !strings.Contains(strings.Join(viewer.Cmd.Args, " "), " -r") {
		t.Errorf("a viewer attach is not read-only: %s", strings.Join(viewer.Cmd.Args, " "))
	}
}

// §8.1: attaching onto nothing is not-found, decided before the client runs.
func TestAttachingToAnAbsentSessionIsNotFound(t *testing.T) {
	b := newBackend(t)
	create(t, b, backend.CreateSpec{Name: "oly-anchor2"})

	_, err := b.Attach(context.Background(), "oly-nope", backend.AttachSpec{})
	if backend.CodeOf(err) != backend.CodeSessionNotFound {
		t.Errorf("attaching to an absent session is %q, want %q", backend.CodeOf(err), backend.CodeSessionNotFound)
	}
}

// §9.1: views group by the base's immutable session ID, never by its name.
//
// tmux resolves a -t target against GROUP names before session names, so a
// group named after its base makes the base's own name ambiguous and a later
// operation on the base can land on the group instead.
func TestViewsGroupByIDSoTheBaseStaysAddressableByName(t *testing.T) {
	b := newBackend(t)
	ctx := context.Background()
	base := create(t, b, backend.CreateSpec{Name: "oly-base"})
	warm(t, b, base)

	if _, err := b.CreateView(ctx, base, backend.ViewSpec{Name: "olympus-view-oly-base-a1b2", Mouse: true}); err != nil {
		t.Fatalf("creating a view: %v", err)
	}

	// The base must still answer to its own name for every verb, and the view
	// must be reported as a view of it rather than as a peer.
	if state := b.Probe(ctx, base); state != backend.StatePresent {
		t.Errorf("the base probes %q after a view was created, want present", state)
	}
	if err := b.Type(ctx, base, `printf 'base-%d\n' 4`); err != nil {
		t.Fatalf("typing into the base: %v", err)
	}
	if err := b.Submit(ctx, base); err != nil {
		t.Fatalf("submitting to the base: %v", err)
	}
	waitForScreen(t, b, base, "base-4")

	views, err := b.Views(ctx, base)
	if err != nil {
		t.Fatalf("listing views: %v", err)
	}
	if len(views) != 1 {
		t.Fatalf("listing views of the base returned %d, want 1", len(views))
	}
	if views[0].Base != base {
		t.Errorf("the view names base %q, want %q", views[0].Base, base)
	}
}

func listBuffers(t *testing.T, b backend.Backend) string {
	t.Helper()
	socket := socketOf(t, b)
	out, err := exec.Command("tmux", "-S", socket, "list-buffers").Output()
	if err != nil {
		// No buffers at all makes tmux exit non-zero on some versions.
		return ""
	}
	return strings.TrimSpace(string(out))
}

// socketOf recovers the private socket for raw verification calls. Those calls
// are held to the same isolation rule as the backend itself (§2.9).
func socketOf(t *testing.T, b backend.Backend) string {
	t.Helper()
	type socketed interface{ Scope() string }
	s, ok := b.(socketed)
	if !ok {
		t.Fatal("the tmux backend does not expose its socket, so a raw verification call cannot be isolated")
	}
	return s.Scope()
}

func lastMatching(screen, prefix string) string {
	var found string
	for _, line := range strings.Split(screen, "\n") {
		if strings.Contains(line, prefix) {
			found = line
		}
	}
	return found
}

// longestRun returns the length of the longest run of "W" characters on any one
// line, which is how a wrapped-and-rejoined line is distinguished from a
// wrapped-and-left-split one.
func longestRun(text string) int {
	best := 0
	for _, line := range strings.Split(text, "\n") {
		run := 0
		for _, r := range line {
			if r == 'W' {
				run++
				if run > best {
					best = run
				}
			} else {
				run = 0
			}
		}
	}
	return best
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var digits []byte
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	return string(digits)
}

var _ = tmux.DefaultSocket

// §2.1: a session whose command finishes before the create call returns is not
// an infrastructure failure.
//
// Without a corpse flag, a session takes itself down when its command exits, so
// a fast command is routinely gone before the confirming listing runs.
// Reporting that as UNEXPECTED would make an ordinary short command look like
// Olympus broke — while still leaving the caller no idea what happened. The row
// is synthesized instead: created, and gone.
func TestAnInstantlyFinishedSessionIsCreatedAndGoneNotAnError(t *testing.T) {
	b := newBackend(t)
	create(t, b, backend.CreateSpec{Name: "oly-anchor3"})

	row, err := b.Create(context.Background(), backend.CreateSpec{
		Name:    "oly-instant",
		Dir:     t.TempDir(),
		Cols:    80,
		Rows:    24,
		Command: []string{"sh", "-c", "exit 0"},
	})
	if err != nil {
		t.Fatalf("creating a session whose command exits immediately: %v", err)
	}
	if row.Name != "oly-instant" {
		t.Errorf("the row names %q, want %q", row.Name, "oly-instant")
	}
	if row.Outcome != backend.OutcomeCreated {
		t.Errorf("outcome %q, want %q", row.Outcome, backend.OutcomeCreated)
	}

	// Which liveness comes back is a genuine race and both arms are correct:
	// the command may or may not have exited before the confirming listing
	// ran. Pinning one would make this test flaky for a reason that has
	// nothing to do with the rule. What must never happen is an error, and
	// what must always happen is a classified row rather than a blank one.
	switch row.Liveness {
	case backend.LivenessPresent, backend.LivenessGone:
	default:
		t.Errorf("liveness %q, want present or gone — the row must be classified either way", row.Liveness)
	}
}

// The same command WITH a corpse requested keeps the session, which is the
// whole point of the flag and the contrast that makes the case above correct.
func TestTheSameCommandWithACorpseRequestedStaysListed(t *testing.T) {
	b := newBackend(t)
	row, err := b.Create(context.Background(), backend.CreateSpec{
		Name:         "oly-corpse-kept",
		Dir:          t.TempDir(),
		Cols:         80,
		Rows:         24,
		Command:      []string{"sh", "-c", "exit 0"},
		RemainOnExit: true,
	})
	if err != nil {
		t.Fatalf("creating: %v", err)
	}
	t.Cleanup(func() { _ = b.Kill(context.Background(), "oly-corpse-kept") })
	if row.Liveness != backend.LivenessPresent {
		t.Errorf("liveness %q, want %q — a requested corpse must keep the session listed", row.Liveness, backend.LivenessPresent)
	}
}

// §9.3: view creation is not a side-effect-free read. It defines a
// server-global option and defines a server-global key table, and both have to
// be right or the view is unusable in ways nothing reports.
func TestCreatingAViewSetsUpItsReadOnlyPosture(t *testing.T) {
	b := newBackend(t)
	ctx := context.Background()
	base := create(t, b, backend.CreateSpec{Name: "oly-vbase"})

	view, err := b.CreateView(ctx, base, backend.ViewSpec{Name: "olympus-view-oly-vbase-aa11", Mouse: true})
	if err != nil {
		t.Fatalf("creating a view: %v", err)
	}

	socket := socketOf(t, b)
	// Read back through the WINDOW target. A bare session target is resolved
	// against group names first, and a view is in a group — so "=name" answers
	// "no such session" for a session that plainly exists (§9.1, §10).
	option := func(name string) string {
		t.Helper()
		out, err := exec.Command("tmux", "-S", socket, "show-options", "-t", "="+view.Name+":", name).Output()
		if err != nil {
			return ""
		}
		return strings.TrimSpace(string(out))
	}

	// A view is for reading: no status bar, and no prefix so the multiplexer's
	// own key passes through to the pane instead of being captured.
	if got := option("status"); !strings.Contains(got, "off") {
		t.Errorf("the view's status bar is %q, want off", got)
	}
	if got := option("prefix"); !strings.Contains(got, "None") {
		t.Errorf("the view's prefix is %q, want None", got)
	}
	if got := option("key-table"); !strings.Contains(got, "olympus-passthrough") {
		t.Errorf("the view's key table is %q, want the reserved pass-through table", got)
	}

	// The empty pass-through table strips tmux's own mouse bindings, so the
	// wheel would do nothing at all without these being re-added.
	bindings, err := exec.Command("tmux", "-S", socket, "list-keys", "-T", "olympus-passthrough").Output()
	if err != nil {
		t.Fatalf("listing the pass-through table: %v", err)
	}
	for _, want := range []string{"WheelUpPane", "WheelDownPane"} {
		if !strings.Contains(string(bindings), want) {
			t.Errorf("the pass-through table has no %s binding, so scrolling a view does nothing:\n%s", want, bindings)
		}
	}
	// A click selects the pane and is forwarded (§9.3): both halves must be in
	// the one binding, since a bare `;` in the bind-key argv would run the
	// second half at bind time instead.
	if !strings.Contains(string(bindings), "MouseDown1Pane select-pane -t = \\; send-keys -M") {
		t.Errorf("the pass-through table has no click-to-select binding, so a touch cannot move between panes:\n%s", bindings)
	}
}

// tmux's `new-session -t` SUCCEEDS against a base that does not exist: it
// simply starts a brand-new group under that name. Without probing first, a
// typo'd base silently produces an orphan session and reports success.
func TestCreatingAViewOnAnAbsentBaseIsNotFound(t *testing.T) {
	b := newBackend(t)
	ctx := context.Background()
	create(t, b, backend.CreateSpec{Name: "oly-vanchor"})

	_, err := b.CreateView(ctx, "oly-no-such-base", backend.ViewSpec{Name: "olympus-view-orphan-1"})
	if backend.CodeOf(err) != backend.CodeSessionNotFound {
		t.Fatalf("creating a view on an absent base is %q, want %q", backend.CodeOf(err), backend.CodeSessionNotFound)
	}

	// And it must not have left the orphan behind.
	sessions, err := b.Sessions(ctx)
	if err != nil {
		t.Fatalf("listing: %v", err)
	}
	for _, s := range sessions {
		if s.Name == "olympus-view-orphan-1" || s.Name == "oly-no-such-base" {
			t.Errorf("an orphan session %q was created for an absent base", s.Name)
		}
	}
}

// A failure partway through the setup must not leave a half-configured view: a
// session with no status bar, no prefix and no key table is worse than none.
func TestAViewIsScrollableAfterCreation(t *testing.T) {
	b := newBackend(t)
	ctx := context.Background()
	base := create(t, b, backend.CreateSpec{Name: "oly-vscroll"})
	warm(t, b, base)

	view, err := b.CreateView(ctx, base, backend.ViewSpec{Name: "olympus-view-oly-vscroll-b2", Mouse: true})
	if err != nil {
		t.Fatalf("creating a view: %v", err)
	}
	if err := b.ScrollView(ctx, view.Name, 5); err != nil {
		t.Errorf("scrolling the view: %v", err)
	}
	// Back toward the live tail.
	if err := b.ScrollView(ctx, view.Name, -5); err != nil {
		t.Errorf("scrolling the view back: %v", err)
	}
	// Zero is a no-op success rather than an error.
	if err := b.ScrollView(ctx, view.Name, 0); err != nil {
		t.Errorf("scrolling by zero: %v", err)
	}
}

// §9.6: focusing a view by cell selects the pane under that cell on the
// view's current window and reports its id; a border cell selects nothing and
// reports an empty id without error. §9.4: the active pane is the shared
// window's, so the base follows.
func TestFocusingAViewByCellSelectsThePaneUnderIt(t *testing.T) {
	b := newBackend(t)
	ctx := context.Background()
	base := create(t, b, backend.CreateSpec{Name: "oly-vfocus"})
	socket := socketOf(t, b)

	// Two panes side by side on the base's window; the split leaves the NEW
	// pane active, so select the left one back to make the move observable.
	if out, err := exec.Command("tmux", "-S", socket, "split-window", "-h", "-t", "="+base+":").CombinedOutput(); err != nil {
		t.Fatalf("splitting the base: %v: %s", err, out)
	}
	rects, err := exec.Command("tmux", "-S", socket, "list-panes", "-t", "="+base+":", "-F",
		"#{pane_id} #{pane_left} #{pane_right} #{pane_active}").Output()
	if err != nil {
		t.Fatalf("listing panes: %v", err)
	}
	var left, right string
	var borderCol, rightCol int
	for _, line := range strings.Split(strings.TrimSpace(string(rects)), "\n") {
		var id string
		var l, r, active int
		if _, err := fmt.Sscanf(line, "%s %d %d %d", &id, &l, &r, &active); err != nil {
			t.Fatalf("parsing %q: %v", line, err)
		}
		if l == 0 {
			left, borderCol = id, r+1
		} else {
			right, rightCol = id, l+1
		}
	}
	if left == "" || right == "" {
		t.Fatalf("expected two panes side by side, got:\n%s", rects)
	}
	if out, err := exec.Command("tmux", "-S", socket, "select-pane", "-t", left).CombinedOutput(); err != nil {
		t.Fatalf("selecting the left pane: %v: %s", err, out)
	}

	view, err := b.CreateView(ctx, base, backend.ViewSpec{Name: "olympus-view-oly-vfocus-c3"})
	if err != nil {
		t.Fatalf("creating a view: %v", err)
	}
	activePane := func() string {
		out, err := exec.Command("tmux", "-S", socket, "display-message", "-p", "-t", "="+base+":", "#{pane_id}").Output()
		if err != nil {
			t.Fatalf("reading the active pane: %v", err)
		}
		return strings.TrimSpace(string(out))
	}
	if got := activePane(); got != left {
		t.Fatalf("before focusing, the active pane is %s, want %s", got, left)
	}

	// A cell inside the right pane moves the active pane there, on the base
	// too (§9.4), and the id comes back.
	got, err := b.FocusView(ctx, view.Name, rightCol, 2)
	if err != nil {
		t.Fatalf("focusing (%d, 2): %v", rightCol, err)
	}
	if got != right {
		t.Errorf("focusing (%d, 2) reported %q, want %s", rightCol, got, right)
	}
	if active := activePane(); active != right {
		t.Errorf("after focusing, the active pane is %s, want %s", active, right)
	}

	// The border belongs to no pane: nothing moves, nothing errors.
	got, err = b.FocusView(ctx, view.Name, borderCol, 2)
	if err != nil {
		t.Fatalf("focusing the border (%d, 2): %v", borderCol, err)
	}
	if got != "" {
		t.Errorf("focusing the border reported %q, want an empty pane", got)
	}
	if active := activePane(); active != right {
		t.Errorf("focusing the border moved the active pane to %s", active)
	}

	// Out of range is the same non-answer.
	if got, err := b.FocusView(ctx, view.Name, 500, 500); err != nil || got != "" {
		t.Errorf("focusing (500, 500) = %q, %v; want empty and no error", got, err)
	}
	// A negative cell is a caller mistake rather than a miss.
	if _, err := b.FocusView(ctx, view.Name, -1, 0); backend.CodeOf(err) != backend.CodeUsage {
		t.Errorf("focusing (-1, 0) is %q, want %q", backend.CodeOf(err), backend.CodeUsage)
	}
	// An absent view is not-found, as for any other target.
	if _, err := b.FocusView(ctx, "olympus-view-nonesuch-00", 0, 0); backend.CodeOf(err) != backend.CodeSessionNotFound {
		t.Errorf("focusing an absent view is %q, want %q", backend.CodeOf(err), backend.CodeSessionNotFound)
	}
}

// §9.5: the base of a group is its oldest member, decided by creation time.
//
// NOT by list order. tmux lists sessions sorted by NAME, and the reserved view
// prefix sorts before most session names — so an implementation that took the
// first row as the base reported every group with the base and the view
// swapped, which is what this reproduces.
func TestAViewIsNeverMistakenForItsBase(t *testing.T) {
	b := newBackend(t)
	ctx := context.Background()
	// A base whose name sorts AFTER its view's, which is the ordinary case for
	// the reserved prefix and the one that used to break.
	base := create(t, b, backend.CreateSpec{Name: "zzz-base"})

	// The reserved prefix sorts BEFORE this base's name, which is the ordering
	// that used to make the base and the view swap places.
	view, err := b.CreateView(ctx, base, backend.ViewSpec{Name: "olympus-view-zzz-base-a1"})
	if err != nil {
		t.Fatalf("creating a view: %v", err)
	}

	views, err := b.Views(ctx, "")
	if err != nil {
		t.Fatalf("listing views: %v", err)
	}
	if len(views) != 1 {
		t.Fatalf("listing views returned %d, want 1: %+v", len(views), views)
	}
	if views[0].Name != view.Name {
		t.Errorf("the view is named %q, want %q — the base was reported as the view", views[0].Name, view.Name)
	}
	if views[0].Base != base {
		t.Errorf("the view's base is %q, want %q — the two are swapped", views[0].Base, base)
	}

	// Filtering by base must find it too, which it cannot if they are swapped.
	filtered, err := b.Views(ctx, base)
	if err != nil {
		t.Fatalf("listing views of the base: %v", err)
	}
	if len(filtered) != 1 {
		t.Errorf("listing views of %s returned %d, want 1", base, len(filtered))
	}
}

// §9.5/§17.1: enumeration selects on the reserved prefix, so a session an
// operator grouped themselves is NOT reported as a view.
//
// The consequence of getting this wrong is not cosmetic: a consumer sweeping
// orphaned views would kill a session it never created.
func TestAForeignGroupedSessionIsNotReportedAsAView(t *testing.T) {
	b := newBackend(t)
	ctx := context.Background()
	base := create(t, b, backend.CreateSpec{Name: "oly-fbase"})

	// A grouped session created outside Olympus, exactly as an operator would.
	socket := socketOf(t, b)
	id, err := exec.Command("tmux", "-S", socket, "display-message", "-p", "-t", "=oly-fbase:", "#{session_id}").Output()
	if err != nil {
		t.Fatalf("reading the base's id: %v", err)
	}
	if err := exec.Command("tmux", "-S", socket, "new-session", "-d",
		"-t", strings.TrimSpace(string(id)), "-s", "operators-own-window").Run(); err != nil {
		t.Fatalf("creating a foreign grouped session: %v", err)
	}

	views, err := b.Views(ctx, "")
	if err != nil {
		t.Fatalf("listing views: %v", err)
	}
	for _, v := range views {
		if v.Name == "operators-own-window" {
			t.Errorf("a session the operator grouped themselves was reported as a view; a sweep would kill it")
		}
	}

	// And an Olympus view of the same base still is one.
	if _, err := b.CreateView(ctx, base, backend.ViewSpec{Name: "olympus-view-oly-fbase-c3"}); err != nil {
		t.Fatalf("creating a view: %v", err)
	}
	views, err = b.Views(ctx, base)
	if err != nil {
		t.Fatalf("listing views: %v", err)
	}
	if len(views) != 1 || views[0].Name != "olympus-view-oly-fbase-c3" {
		t.Errorf("listing views of %s returned %+v, want only Olympus's own", base, views)
	}
}

// §17.2: a socket NAME and a socket PATH address different servers, and neither
// can see the other's sessions.
//
// The distinction is the whole point of offering both: a name lands in the
// directory tmux shares with every server the user runs, while a path puts the
// socket where the caller chooses — a project directory, a mounted volume, a
// directory with tighter permissions.
func TestASocketPathAddressesADifferentServerFromASocketName(t *testing.T) {
	requireTmux(t)
	ctx := context.Background()

	dir, err := os.MkdirTemp(os.TempDir(), "olyp")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })

	path := filepath.Join(dir, "by-path.sock")
	name := fmt.Sprintf("olyname-%d-%d", os.Getpid(), socketCounter.Add(1))

	byPath := tmux.New(tmux.WithSocketPath(path))
	byName := tmux.New(tmux.WithSocket(name))
	t.Cleanup(func() {
		_ = exec.Command("tmux", "-S", path, "kill-server").Run()
		_ = exec.Command("tmux", "-L", name, "kill-server").Run()
		dir := os.Getenv("TMUX_TMPDIR")
		if dir == "" {
			dir = "/tmp"
		}
		_ = os.Remove(filepath.Join(dir, fmt.Sprintf("tmux-%d", os.Getuid()), name))
	})

	if _, err := byPath.Create(ctx, backend.CreateSpec{Name: "on-path", Dir: t.TempDir(), Cols: 80, Rows: 24}); err != nil {
		t.Fatalf("creating on the path-addressed server: %v", err)
	}
	if _, err := byName.Create(ctx, backend.CreateSpec{Name: "on-name", Dir: t.TempDir(), Cols: 80, Rows: 24}); err != nil {
		t.Fatalf("creating on the name-addressed server: %v", err)
	}

	// The socket really is where it was asked to be, rather than wherever tmux
	// would have put a name.
	if _, err := os.Stat(path); err != nil {
		t.Errorf("no socket at the requested path: %v", err)
	}

	// Neither server sees the other's sessions: they are separate servers, and
	// a session is scoped to one.
	for _, c := range []struct {
		what     string
		backend  backend.Backend
		wants    string
		wantsNot string
	}{
		{"path-addressed", byPath, "on-path", "on-name"},
		{"name-addressed", byName, "on-name", "on-path"},
	} {
		sessions, err := c.backend.Sessions(ctx)
		if err != nil {
			t.Fatalf("%s: listing: %v", c.what, err)
		}
		var names []string
		for _, s := range sessions {
			names = append(names, s.Name)
		}
		if len(names) != 1 || names[0] != c.wants {
			t.Errorf("%s server lists %v, want only %q", c.what, names, c.wants)
		}
	}

	// And the scope they report differs, which is what keeps their lock keys
	// and diagnostics apart.
	if byPath.Scope() == byName.Scope() {
		t.Errorf("both addressings report the same scope %q, so they would share a lock", byPath.Scope())
	}
}

// A socket of our own is not a configuration of our own: a server on a private
// socket still loads the operator's tmux.conf, because tmux fixes configuration
// per SERVER at boot and the socket only decides which server that is.
//
// Measured, and not theoretical: an operator's `default-command` makes our
// sessions run a shell we never chose, and the run protocol's exit marker is
// written by that shell. Under csh, `echo "OLY_D_id_$?_"` becomes
// `OLY_D_id_1` — csh reads `$?_` as "is the variable _ set", so the real exit
// status is replaced by a 1 and the closing delimiter disappears. A caller is
// then told a command that failed with 3 succeeded, or is told nothing at all.
//
// So the options the protocol depends on are pinned by Olympus, and pinned
// BEFORE the pane spawns — afterwards is too late, the shell is already running.
func TestTheOperatorsConfigCannotChooseOurShell(t *testing.T) {
	requireTmux(t)

	b := backendUnderHostileConfig(t, `set -g default-command "/bin/csh"`)
	name := create(t, b, backend.CreateSpec{Name: "shell", Dir: t.TempDir(), Cols: 80, Rows: 24})

	panes, err := b.Panes(context.Background(), name)
	if err != nil {
		t.Fatalf("Panes: %v", err)
	}
	if len(panes) != 1 {
		t.Fatalf("want one pane, got %d", len(panes))
	}
	if panes[0].CurrentCommand == "csh" {
		t.Errorf("the operator's default-command chose our shell: the run protocol's exit marker is written by it, and csh writes the wrong one")
	}
}

// backendUnderHostileConfig gives a backend whose server will boot against a
// tmux.conf the test controls, by pointing HOME at a directory it owns. The
// operator's real configuration is never read and never written.
func backendUnderHostileConfig(t *testing.T, conf string) backend.Backend {
	t.Helper()

	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, ".tmux.conf"), []byte(conf+"\n"), 0o600); err != nil {
		t.Fatalf("writing the config the server will boot against: %v", err)
	}
	t.Setenv("HOME", home)
	// tmux 3.7 prefers the XDG location, so leaving this set would let the
	// operator's real file win and the test would prove nothing.
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))

	// A short directory, not t.TempDir(): a unix socket path is capped near 104
	// bytes and the test's own name is part of t.TempDir()'s path.
	dir, err := os.MkdirTemp(os.TempDir(), "olyc")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	socket := filepath.Join(dir, "s.sock")
	t.Cleanup(func() { _ = exec.Command("tmux", "-S", socket, "kill-server").Run() })
	return tmux.New(tmux.WithSocketPath(socket))
}

// §17.5: a capture asking for N lines must mean the same thing on every
// machine. tmux's own default history is 2000 and a typical operator config
// raises it, so an unpinned Olympus reads a different depth everywhere and
// reports the difference as no difference at all — the caller is handed a short
// history that looks exactly like a short session.
func TestTheOperatorsConfigCannotSetOurScrollbackDepth(t *testing.T) {
	requireTmux(t)

	b := backendUnderHostileConfig(t, "set -g history-limit 5")
	name := create(t, b, backend.CreateSpec{Name: "depth", Dir: t.TempDir(), Cols: 80, Rows: 24})

	got := serverOption(t, b, "history-limit")
	if got != itoa(tmux.HistoryLimit) {
		t.Errorf("history-limit is %q, want the pinned %d — the operator's config decided how much of %s we can read",
			got, tmux.HistoryLimit, name)
	}
}

// The other half of the same rule: Olympus pins what its own correctness rests
// on and NOTHING cosmetic. A human who attaches keeps their prefix, their
// theme, their bindings — a session driven by a program is still a terminal
// somebody may end up sitting in.
func TestTheOperatorsOwnSettingsSurvive(t *testing.T) {
	requireTmux(t)

	b := backendUnderHostileConfig(t, `set -g @operators-own "kept"`)
	create(t, b, backend.CreateSpec{Name: "cosmetic", Dir: t.TempDir(), Cols: 80, Rows: 24})

	if got := serverOption(t, b, "@operators-own"); got != "kept" {
		t.Errorf("the operator's own setting is %q, want %q — Olympus took away more than it needs", got, "kept")
	}
}

func serverOption(t *testing.T, b backend.Backend, name string) string {
	t.Helper()
	out, err := exec.Command("tmux", "-S", socketOf(t, b), "show-options", "-gqv", name).Output()
	if err != nil {
		t.Fatalf("show-options %s: %v", name, err)
	}
	return strings.TrimSpace(string(out))
}

// §17.2: a socket NAME and a socket PATH are different servers. Attach builds
// its own argv rather than going through run(), so it is the one place that can
// disagree with every other verb about which server it is talking to — and a
// disagreement here hands the operator's terminal to a session on the wrong
// server, or fails to find one that plainly exists.
func TestAttachAddressesTheSameServerAsEveryOtherVerb(t *testing.T) {
	requireTmux(t)

	dir, err := os.MkdirTemp(os.TempDir(), "olya")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	socket := filepath.Join(dir, "s.sock")
	t.Cleanup(func() { _ = exec.Command("tmux", "-S", socket, "kill-server").Run() })

	b := tmux.New(tmux.WithSocketPath(socket))
	name := create(t, b, backend.CreateSpec{Name: "reachable", Dir: t.TempDir(), Cols: 80, Rows: 24})

	att, err := b.Attach(context.Background(), name, backend.AttachSpec{Role: backend.RoleController})
	if err != nil {
		t.Fatalf("preparing an attach: %v", err)
	}
	args := strings.Join(att.Cmd.Args, " ")
	if !strings.Contains(args, "-S "+socket) {
		t.Errorf("attach argv does not address the path-addressed server it was configured with:\n  %s", args)
	}
	if strings.Contains(args, "-L ") {
		t.Errorf("attach argv falls back to a socket NAME, which is a different server entirely:\n  %s", args)
	}
}

// §9.3: creating a view MUST NOT reconfigure the server.
//
// terminal-features is a SERVER option — it has no per-session form — so
// appending to it changes how tmux renders to every client of that server,
// including the operator's own sessions when they point Olympus at a server
// they already run. Olympus pins what it discloses (§17.5) and nothing else;
// silently editing an option nobody asked about is the opposite of that.
//
// The feature is needed only by Olympus's OWN client: a real terminal answers
// tmux's runtime probe, while a headless PTY client never does. So it belongs
// on that client, where tmux's -T flag puts it, and not on the server.
func TestCreatingAViewLeavesTheServersOptionsAlone(t *testing.T) {
	requireTmux(t)

	b := newBackend(t)
	base := create(t, b, backend.CreateSpec{Name: "oly-hl-base", Dir: t.TempDir(), Cols: 80, Rows: 24})
	before := serverOption(t, b, "terminal-features")

	if _, err := b.CreateView(context.Background(), base, backend.ViewSpec{Name: "oly-hl-view"}); err != nil {
		t.Fatalf("CreateView: %v", err)
	}

	if after := serverOption(t, b, "terminal-features"); after != before {
		t.Errorf("creating a view rewrote a server option every other client of this server also reads:\n  before: %s\n  after:  %s", before, after)
	}
}

// The other half: dropping the server-wide edit must not drop the capability.
// A headless client never answers tmux's feature probe, so without a declared
// feature every OSC 8 hyperlink is stripped on its way out — silently, with no
// error anywhere.
func TestAttachDeclaresHyperlinksForItsOwnClient(t *testing.T) {
	requireTmux(t)

	b := newBackend(t)
	name := create(t, b, backend.CreateSpec{Name: "oly-hl-attach", Dir: t.TempDir(), Cols: 80, Rows: 24})

	att, err := b.Attach(context.Background(), name, backend.AttachSpec{Role: backend.RoleController})
	if err != nil {
		t.Fatalf("preparing an attach: %v", err)
	}
	args := strings.Join(att.Cmd.Args, " ")
	if !strings.Contains(args, "-T hyperlinks") {
		t.Errorf("the attach argv declares no hyperlinks feature, so tmux will strip every OSC 8 sequence on its way to this client:\n  %s", args)
	}
}

// §17.5: Olympus configures only servers it STARTS.
//
// Pinning an option with `set-option -g` reaches every session on that server,
// including sessions Olympus was never asked about. On a server the operator
// already runs, a caller who asked us to drive one session would have the
// scrollback of all their others silently changed underneath them — an effect
// far outside the target they named (§0.4). Disclosure explains an action; it
// does not change who bears it.
func TestAPreExistingServerIsNotReconfigured(t *testing.T) {
	requireTmux(t)

	b, socket := backendOnAHostileServer(t, "set -g history-limit 7")

	// Somebody else's server, already running with their settings, before
	// Olympus touches it at all.
	if err := exec.Command("tmux", "-S", socket, "new-session", "-d", "-s", "theirs").Run(); err != nil {
		t.Fatalf("starting the operator's server: %v", err)
	}

	create(t, b, backend.CreateSpec{Name: "ours", Dir: t.TempDir(), Cols: 80, Rows: 24})

	if got := serverOption(t, b, "history-limit"); got != "7" {
		t.Errorf("history-limit is %q, want the operator's 7 — Olympus rewrote a server it did not start, changing every session on it", got)
	}
}

// The other side of the same rule: on a server Olympus starts, there is no one
// else to disturb, and the pins are what make the run protocol and capture
// depth mean the same thing on every machine.
func TestAServerOlympusStartsIsStillPinned(t *testing.T) {
	requireTmux(t)

	b, _ := backendOnAHostileServer(t, "set -g history-limit 7")
	create(t, b, backend.CreateSpec{Name: "ours", Dir: t.TempDir(), Cols: 80, Rows: 24})

	if got := serverOption(t, b, "history-limit"); got != itoa(tmux.HistoryLimit) {
		t.Errorf("history-limit is %q, want the pinned %d on a server Olympus started", got, tmux.HistoryLimit)
	}
}

// backendOnAHostileServer is backendUnderHostileConfig, plus the socket, for
// tests that need to start the server themselves first.
func backendOnAHostileServer(t *testing.T, conf string) (backend.Backend, string) {
	t.Helper()
	b := backendUnderHostileConfig(t, conf)
	return b, socketOf(t, b)
}

// A status is a label a process INSIDE a session leaves for whoever is driving
// it from outside — "I am waiting on you" — which a screen scrape cannot tell
// you reliably, because a program at a prompt and a program mid-work can render
// identically.
//
// Olympus stores it and never reads meaning into it. Enumerating states would
// mean naming the concerns of whatever is driving the terminal rather than the
// terminal, which this repo does not do.
func TestASessionCarriesAStatusItWasGiven(t *testing.T) {
	requireTmux(t)

	b := newBackend(t)
	name := create(t, b, backend.CreateSpec{Name: "oly-status", Dir: t.TempDir(), Cols: 80, Rows: 24})
	ctx := context.Background()

	// Unset is empty, not an error: a session that has never reported anything
	// is a real state, and one a caller has to be able to tell from a failure.
	got, err := b.Status(ctx, name)
	if err != nil {
		t.Fatalf("reading an unset status: %v", err)
	}
	if got != "" {
		t.Errorf("a session that never reported a status reads as %q", got)
	}

	if err := b.SetStatus(ctx, name, "waiting on review"); err != nil {
		t.Fatalf("SetStatus: %v", err)
	}
	got, err = b.Status(ctx, name)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	// Opaque: whitespace and prose survive, because Olympus is not parsing it.
	if got != "waiting on review" {
		t.Errorf("status is %q, want %q", got, "waiting on review")
	}
}

// §8: the attach argv must be one tmux ACCEPTS, which only running it can show.
//
// Asserting that the argv string contains " -u" is what let a real defect ship:
// -u is a global tmux flag and attach-session takes [-dErx], so `attach-session
// … -u` is rejected outright with "command attach-session: unknown flag -u".
// The string assertion passed the whole time, because the flag was present —
// just in a position tmux refuses.
//
// Run without a terminal, tmux gets past flag parsing and fails at "open
// terminal failed", which is the outcome this asserts for: the argv was
// understood, and only the missing TTY stopped it. A flag error would mean the
// command is malformed for every caller, TTY or not.
func TestTheAttachArgvIsOneTmuxAccepts(t *testing.T) {
	requireTmux(t)

	b := newBackend(t)
	name := create(t, b, backend.CreateSpec{Name: "oly-argv", Dir: t.TempDir(), Cols: 80, Rows: 24})

	for _, role := range []backend.Role{backend.RoleController, backend.RoleViewer} {
		att, err := b.Attach(context.Background(), name, backend.AttachSpec{Role: role})
		if err != nil {
			t.Fatalf("preparing a %s attach: %v", role, err)
		}
		out, _ := att.Cmd.CombinedOutput()
		text := string(out)
		if strings.Contains(text, "unknown flag") || strings.Contains(text, "usage:") {
			t.Errorf("tmux rejects the %s attach argv: %s\n  argv: %s",
				role, strings.TrimSpace(text), strings.Join(att.Cmd.Args, " "))
		}
	}
}

// §0.5: the floor is tmux 3.3, so every version from there up must parse.
//
// It did not. tmux SANITIZES non-printable characters in format output, and
// which ones survive differs by version: measured, 3.7b passes a 0x1f
// separator through as the byte, while 3.5a renders it as the four-character
// text `\037` (and turns a tab into `_`). Splitting only on the byte finds no
// separators there, so every row is one field long, every row is skipped, and
// a listing comes back EMPTY on a server that plainly has sessions.
//
// That is the worst shape a failure can take here: `start` reports success,
// tmux itself lists the session, and Olympus reports nothing — so presence
// checks say absent, ensure creates a duplicate, and the error a caller
// finally sees is about something else entirely.
func TestListingsParseBothSpellingsOfTheSeparator(t *testing.T) {
	raw := "build\x1f$0\x1f0\x1f0\x1f/repo"
	escaped := `build\037$0\0370\0370\037/repo`

	for _, c := range []struct {
		name string
		line string
	}{
		{"the byte itself, as tmux 3.7 emits it", raw},
		{"the octal escape, as tmux 3.5 emits it", escaped},
	} {
		t.Run(c.name, func(t *testing.T) {
			fields := tmux.SplitFields(c.line)
			if len(fields) != 5 {
				t.Fatalf("split into %d fields, want 5: %q", len(fields), fields)
			}
			if fields[0] != "build" || fields[1] != "$0" || fields[4] != "/repo" {
				t.Errorf("fields came apart wrong: %q", fields)
			}
		})
	}
}

// §9.4: a grouped session keeps its own current WINDOW, so a view can be
// pinned to one of the base's windows without moving the base or any sibling
// view. The pane is the one thing it cannot choose privately, so a pinned view
// selects no pane at all.
func TestAViewPinnedToAWindowLeavesTheBaseWhereItWas(t *testing.T) {
	b := newBackend(t)
	ctx := context.Background()
	base := create(t, b, backend.CreateSpec{Name: "oly-wbase"})
	socket := socketOf(t, b)
	tmux := func(args ...string) string {
		t.Helper()
		out, err := exec.Command("tmux", append([]string{"-S", socket}, args...)...).Output()
		if err != nil {
			t.Fatalf("tmux %s: %v", strings.Join(args, " "), err)
		}
		return strings.TrimSpace(string(out))
	}
	// Not selected: the base keeps showing window 0.
	tmux("new-window", "-d", "-t", "="+base+":", "-n", "second")

	for _, window := range []string{"1", "second"} {
		view, err := b.CreateView(ctx, base, backend.ViewSpec{Name: "olympus-view-oly-wbase-" + window, Window: window})
		if err != nil {
			t.Fatalf("creating a view pinned to window %q: %v", window, err)
		}
		if got := tmux("display-message", "-p", "-t", "="+view.Name+":", "#{window_index} #{window_name}"); got != "1 second" {
			t.Errorf("a view pinned to %q shows %q, want %q", window, got, "1 second")
		}
	}
	if got := tmux("display-message", "-p", "-t", "="+base+":", "#{window_index}"); got != "0" {
		t.Errorf("pinning a view moved the base to window %s", got)
	}
}

// A window the base does not have is not-found — and, since the check runs
// before the view exists, nothing is left behind. An exact match only: tmux's
// own target matching would let "sec" land on "second", which turns a typo into
// a window.
func TestAViewPinnedToAMissingWindowIsNotFoundAndCreatesNothing(t *testing.T) {
	b := newBackend(t)
	ctx := context.Background()
	base := create(t, b, backend.CreateSpec{Name: "oly-wnone"})
	if err := exec.Command("tmux", "-S", socketOf(t, b), "new-window", "-d", "-t", "="+base+":", "-n", "second").Run(); err != nil {
		t.Fatalf("adding a window: %v", err)
	}

	for _, window := range []string{"9", "nope", "sec"} {
		_, err := b.CreateView(ctx, base, backend.ViewSpec{Name: "olympus-view-oly-wnone-x", Window: window})
		if backend.CodeOf(err) != backend.CodeSessionNotFound {
			t.Errorf("a view pinned to window %q is %q, want %q", window, backend.CodeOf(err), backend.CodeSessionNotFound)
		}
	}
	views, err := b.Views(ctx, base)
	if err != nil {
		t.Fatalf("listing views: %v", err)
	}
	if len(views) != 0 {
		t.Errorf("a refused view was left behind: %+v", views)
	}
}

// §8.10 Focus on tmux: clients attached to one plain session share its
// current window and pane, so `<session>:<window>` selects the window for
// all of them, a pane id selects its window and then the pane, and a bare
// session name is accepted with nothing to do.
func TestFocusSelectsAWindowOrAPaneForEveryClient(t *testing.T) {
	b := newBackend(t)
	name := create(t, b, backend.CreateSpec{Name: "oly-focus"})
	ctx := context.Background()
	socket := socketOf(t, b)
	tmux := func(args ...string) string {
		t.Helper()
		out, err := exec.Command("tmux", append([]string{"-S", socket}, args...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("tmux %s: %v\n%s", strings.Join(args, " "), err, out)
		}
		return strings.TrimSpace(string(out))
	}
	firstPane := tmux("display-message", "-p", "-t", "="+name+":", "#{pane_id}")
	tmux("split-window", "-d", "-t", "="+name+":.")
	// The split leaves the first pane active; the second window is created
	// and left unselected, so the base sits at window 0, pane one.
	tmux("new-window", "-d", "-t", "="+name+":", "-n", "second")
	f, ok := b.(backend.Focuser)
	if !ok {
		t.Fatal("the tmux backend does not implement Focuser")
	}

	if err := f.Focus(ctx, name+":second"); err != nil {
		t.Fatalf("focusing a window: %v", err)
	}
	if got := tmux("display-message", "-p", "-t", "="+name+":", "#{window_name}"); got != "second" {
		t.Errorf("after focusing the window the session shows %q, want second", got)
	}
	otherPane := tmux("list-panes", "-t", "="+name+":0", "-F", "#{pane_id}", "-f", "#{==:#{pane_id},"+firstPane+"}")
	if otherPane != firstPane {
		t.Fatalf("pane bookkeeping: %q", otherPane)
	}
	second := strings.Fields(tmux("list-panes", "-t", "="+name+":0", "-F", "#{pane_id}"))
	var target string
	for _, p := range second {
		if p != firstPane {
			target = p
		}
	}
	if err := f.Focus(ctx, target); err != nil {
		t.Fatalf("focusing a pane: %v", err)
	}
	if got := tmux("display-message", "-p", "-t", "="+name+":", "#{window_index} #{pane_id}"); got != "0 "+target {
		t.Errorf("after focusing pane %s the session shows %q, want %q", target, got, "0 "+target)
	}
	if err := f.Focus(ctx, name); err != nil {
		t.Errorf("a bare session name has nothing to steer and must be accepted: %v", err)
	}
	if err := f.Focus(ctx, name+":nine"); backend.CodeOf(err) != backend.CodeSessionNotFound {
		t.Errorf("focusing a window the session lacks is %q, want %q (err %v)", backend.CodeOf(err), backend.CodeSessionNotFound, err)
	}
}

// §2.11 Rename on tmux: a session through rename-session, a window through
// rename-window, a pane's title through select-pane -T; a colon in a session
// name is refused rather than rewritten.
func TestRenameRenamesASessionAWindowAndAPaneTitle(t *testing.T) {
	b := newBackend(t)
	name := create(t, b, backend.CreateSpec{Name: "oly-ren"})
	ctx := context.Background()
	socket := socketOf(t, b)
	tmux := func(args ...string) string {
		t.Helper()
		out, err := exec.Command("tmux", append([]string{"-S", socket}, args...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("tmux %s: %v\n%s", strings.Join(args, " "), err, out)
		}
		return strings.TrimSpace(string(out))
	}
	r, ok := b.(backend.Renamer)
	if !ok {
		t.Fatal("the tmux backend does not implement Renamer")
	}
	if err := r.Rename(ctx, name+":0", "editor"); err != nil {
		t.Fatalf("renaming a window: %v", err)
	}
	if got := tmux("display-message", "-p", "-t", "="+name+":0", "#{window_name}"); got != "editor" {
		t.Errorf("window name is %q, want editor", got)
	}
	pane := tmux("display-message", "-p", "-t", "="+name+":", "#{pane_id}")
	if err := r.Rename(ctx, pane, "titled"); err != nil {
		t.Fatalf("titling a pane: %v", err)
	}
	if got := tmux("display-message", "-p", "-t", pane, "#{pane_title}"); got != "titled" {
		t.Errorf("pane title is %q, want titled", got)
	}
	// The two names come back on the pane row, so a caller can show what it
	// renamed.
	rows, err := b.Panes(ctx, name)
	if err != nil || len(rows) == 0 {
		t.Fatalf("Panes: %v (%d rows)", err, len(rows))
	}
	if rows[0].WindowName != "editor" || rows[0].Title != "titled" {
		t.Errorf("the pane row reports window %q title %q, want editor/titled", rows[0].WindowName, rows[0].Title)
	}
	if err := r.Rename(ctx, name, "a:b"); backend.CodeOf(err) != backend.CodeUsage {
		t.Errorf("a colon in a session name is %q, want %q (err %v)", backend.CodeOf(err), backend.CodeUsage, err)
	}
	if err := r.Rename(ctx, name, "oly-renamed"); err != nil {
		t.Fatalf("renaming the session: %v", err)
	}
	if b.Probe(ctx, "oly-renamed") != backend.StatePresent || b.Probe(ctx, name) != backend.StateAbsent {
		t.Errorf("after the rename the session answers to old=%s new=%s", b.Probe(ctx, name), b.Probe(ctx, "oly-renamed"))
	}
	if err := r.Rename(ctx, "oly-renamed:nine", "x"); backend.CodeOf(err) != backend.CodeSessionNotFound {
		t.Errorf("renaming a missing window is %q, want %q (err %v)", backend.CodeOf(err), backend.CodeSessionNotFound, err)
	}
}
