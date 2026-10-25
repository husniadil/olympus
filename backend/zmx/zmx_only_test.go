package zmx_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/husniadil/olympus/backend"
	"github.com/husniadil/olympus/backend/zmx"
)

func newBackend(t *testing.T) backend.Backend {
	t.Helper()
	requireZmx(t)
	return newIsolated(t)
}

func startShell(t *testing.T, b backend.Backend, name string) string {
	t.Helper()
	if _, err := b.Create(context.Background(), backend.CreateSpec{Name: name, Cols: 80, Rows: 24}); err != nil {
		t.Fatalf("creating %s: %v", name, err)
	}
	t.Cleanup(func() { _ = b.Kill(context.Background(), name) })
	return name
}

func waitFor(t *testing.T, b backend.Backend, target, want string) string {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
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
			t.Fatalf("waiting for %q on %s: never appeared. Scrollback was:\n%s", want, target, last)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

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

// §1.1: an inherited session variable does not degrade — it silently
// retargets.
//
// "zmx attach <name>" with ZMX_SESSION set does not create or attach <name>: it
// acts on whatever the ambient variable held. This is not hypothetical; it is
// what happens to any caller running from inside a zmx session, which is a
// perfectly ordinary place to run from.
func TestAnAmbientSessionVariableDoesNotRetargetTheSpawn(t *testing.T) {
	b := newBackend(t)
	t.Setenv("ZMX_SESSION", "oly-ambient-decoy")
	t.Setenv("ZMX_SESSION_PREFIX", "decoy-")

	name := startShell(t, b, "oly-strip")

	sessions, err := b.Sessions(context.Background())
	if err != nil {
		t.Fatalf("listing: %v", err)
	}
	var names []string
	for _, s := range sessions {
		names = append(names, s.Name)
		if s.Name == "oly-ambient-decoy" {
			t.Fatalf("the spawn was retargeted at the ambient session name")
		}
	}
	if len(sessions) != 1 || sessions[0].Name != name {
		t.Errorf("created sessions %v, want exactly %q", names, name)
	}
}

// §2.5: a name whose socket path would not fit is rejected UP FRONT, with a
// usage-class error naming the path, its length and the budget.
//
// Without the check the failure is misleading rather than silent: zmx errors
// loudly, but the spawn path deliberately ignores the client's exit code, falls
// through to the registration poll, and times out into an error that never
// mentions the real cause.
func TestAnOverlongNameIsRejectedBeforeAnyInvocation(t *testing.T) {
	b := newBackend(t)
	ctx := context.Background()

	long := strings.Repeat("n", 120)
	started := time.Now()
	_, err := b.Create(ctx, backend.CreateSpec{Name: long, Cols: 80, Rows: 24})

	if !errors.Is(err, backend.ErrUsage) {
		t.Fatalf("creating an overlong name is %q, want a usage error: %v", backend.CodeOf(err), err)
	}
	// Rejected before any invocation means immediately, not after the
	// registration poll's budget has elapsed.
	if elapsed := time.Since(started); elapsed > 2*time.Second {
		t.Errorf("the rejection took %s, so it fell through to the registration poll instead of failing up front", elapsed)
	}
	for _, want := range []string{"103", long} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the error %q does not mention %q", err.Error(), want)
		}
	}
	if !strings.Contains(err.Error(), "byte") {
		t.Errorf("the error %q does not give the length or the budget in bytes", err.Error())
	}
}

// §5.2: colors are opt-in and NOT a no-op. Default output has every escape byte
// stripped; the flag preserves them byte-for-byte.
func TestColorsArePreservedOnlyWhenAskedFor(t *testing.T) {
	b := newBackend(t)
	name := startShell(t, b, "oly-color")
	warm(t, b, name)

	ctx := context.Background()
	if err := b.Type(ctx, name, `printf '\033[31mred-%d\033[0m\n' 5`); err != nil {
		t.Fatalf("typing: %v", err)
	}
	if err := b.Submit(ctx, name); err != nil {
		t.Fatalf("submitting: %v", err)
	}
	waitFor(t, b, name, "red-5")

	plain, err := b.Screen(ctx, name, backend.ScreenOpts{})
	if err != nil {
		t.Fatalf("capturing: %v", err)
	}
	colored, err := b.Screen(ctx, name, backend.ScreenOpts{Colors: true})
	if err != nil {
		t.Fatalf("capturing with colors: %v", err)
	}

	if strings.Contains(plain.Text, "\x1b[") {
		t.Errorf("the default capture kept escape sequences, but they must be stripped")
	}
	if !strings.Contains(colored.Text, "\x1b[") {
		t.Errorf("the opt-in capture dropped escape sequences, so colors are a no-op")
	}
}

// §5.2: history IS a documented no-op here, and both flag states MUST return
// byte-identical output.
//
// Regression-guarded rather than assumed: a backend that quietly started
// honouring the request would change what every existing caller gets back
// without any caller asking for it.
func TestRequestingHistoryIsAByteIdenticalNoOp(t *testing.T) {
	b := newBackend(t)
	name := startShell(t, b, "oly-hist")
	warm(t, b, name)

	ctx := context.Background()
	if err := b.Type(ctx, name, `i=1; while [ $i -le 60 ]; do printf 'h-%d\n' $i; i=$((i+1)); done`); err != nil {
		t.Fatalf("typing: %v", err)
	}
	if err := b.Submit(ctx, name); err != nil {
		t.Fatalf("submitting: %v", err)
	}
	waitFor(t, b, name, "h-60")

	// The two captures are taken as a PAIR and the pair is retried. Both read a
	// live session, and the session repaints between them — the prompt lands
	// after the last line of output — so a single disagreeing pair says nothing
	// about the flag. Only a whole budget of disagreement is the backend
	// honouring a request it must ignore (§16).
	deadline := time.Now().Add(15 * time.Second)
	for {
		without, err := b.Screen(ctx, name, backend.ScreenOpts{})
		if err != nil {
			t.Fatalf("capturing: %v", err)
		}
		with, err := b.Screen(ctx, name, backend.ScreenOpts{HistoryLines: 500})
		if err != nil {
			t.Fatalf("capturing with history: %v", err)
		}
		if without.Text == with.Text {
			// Both must already carry the scrolled-off lines, which is why the
			// request has nothing left to do.
			if !strings.Contains(without.Text, "h-1\n") {
				t.Errorf("the default capture does not reach back to the first line, so it is not full scrollback")
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("requesting history changed the output, but on this backend it must be a no-op. "+
				"Without:\n%s\nWith:\n%s", without.Text, with.Text)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// §12: unsupported is not unavailable, and it is not a real negative answer
// either. A consumer branches on capabilities rather than on these errors, so
// the two must agree.
func TestOperationsWithNoConceptHereAnswerUnsupported(t *testing.T) {
	b := newBackend(t)
	ctx := context.Background()
	name := startShell(t, b, "oly-unsup")

	cases := map[string]error{
		"create a view": func() error {
			_, err := b.CreateView(ctx, name, backend.ViewSpec{Name: "olympus-view-x-1"})
			return err
		}(),
		"scroll a view": b.ScrollView(ctx, name, 1),
		"focus a view":  func() error { _, err := b.FocusView(ctx, name, 0, 0); return err }(),
		"list views":    func() error { _, err := b.Views(ctx, name); return err }(),
		"read server environment": func() error {
			_, _, err := b.ServerEnv(ctx, "ANY")
			return err
		}(),
		"keep a corpse": func() error {
			_, err := b.Create(ctx, backend.CreateSpec{Name: "oly-corpse", Cols: 80, Rows: 24, RemainOnExit: true})
			return err
		}(),
	}

	for what, err := range cases {
		if !errors.Is(err, backend.ErrUnsupported) {
			t.Errorf("asking this backend to %s is %q, want %q", what, backend.CodeOf(err), backend.CodeUnsupported)
		}
	}

	// §2.7: the corpse flag must be rejected before any invocation, so the
	// rejection cannot depend on whether the session already existed.
	if state := b.Probe(ctx, "oly-corpse"); state != backend.StateAbsent {
		t.Errorf("the rejected create still left a session behind (state %q)", state)
	}
}

// §2.7's failure mode stated directly: a state-dependent contract. If the
// rejection lived inside the create path only, a name that already exists would
// take the reuse branch, never reach create, and silently accept and ignore the
// flag.
func TestTheCorpseFlagIsRejectedEvenForAnExistingName(t *testing.T) {
	b := newBackend(t)
	ctx := context.Background()
	name := startShell(t, b, "oly-existing")

	_, err := b.Create(ctx, backend.CreateSpec{Name: name, Cols: 80, Rows: 24, RemainOnExit: true})
	if !errors.Is(err, backend.ErrUnsupported) {
		t.Errorf("the corpse flag against an existing name is %q, want %q", backend.CodeOf(err), backend.CodeUnsupported)
	}
}

// §3.1: the listing MUST use the long form.
//
// The short form is not a smaller version of the same answer — it omits the err
// field, and without that field every row classifies as present, the gone
// signal disappears entirely, and nothing can ever be reaped. The observable is
// that rows carry the fields only the long form provides.
func TestListingCarriesTheFieldsOnlyTheLongFormProvides(t *testing.T) {
	b := newBackend(t)
	name := startShell(t, b, "oly-long")

	panes, err := b.Panes(context.Background(), name)
	if err != nil {
		t.Fatalf("listing panes: %v", err)
	}
	if len(panes) == 0 {
		t.Fatal("no panes")
	}
	// created and start_dir appear only in the long form. The short form would
	// leave both empty, and a silently zeroed timestamp is exactly the failure
	// §16 warns about.
	if panes[0].CreatedAt == 0 {
		t.Errorf("the row has no creation time, so the listing is not using the long form")
	}
	if panes[0].CurrentPath == "" {
		t.Errorf("the row has no start directory, so the listing is not using the long form")
	}
}

// §2.1: initial size is accepted for interface conformance and ignored, because
// there is no spawn-time sizing concept here and the PTY is sized entirely by
// whatever client attaches later. Accepting it must not fail; papering over it
// by pretending it applied would be worse.
func TestAnInitialSizeIsAcceptedAndIgnored(t *testing.T) {
	b := newBackend(t)
	ctx := context.Background()
	if _, err := b.Create(ctx, backend.CreateSpec{Name: "oly-size", Cols: 200, Rows: 60}); err != nil {
		t.Fatalf("creating with a size: %v", err)
	}
	t.Cleanup(func() { _ = b.Kill(ctx, "oly-size") })

	// "Accepted" was all this asserted, which made the second half of its own
	// name — and the reason the case exists — unverified: it passed whether the
	// size was ignored, honoured, or never looked at. Found by looking for tests
	// whose only assertion is an error check.
	//
	// The declaration first, since callers branch on it rather than measuring.
	if b.Capabilities().SpawnSizing {
		t.Error("this backend claims spawn sizing, which would make ignoring the size a defect")
	}

	// Then the session itself, because a capability is a claim and this is the
	// thing it claims about. 200 columns is far wider than any default, so a
	// session that reports it would mean the request had been honoured after all.
	warm(t, b, "oly-size")
	if err := b.Type(ctx, "oly-size", `printf 'cols=[%s]\n' "$(tput cols)"`); err != nil {
		t.Fatalf("asking the session its width: %v", err)
	}
	if err := b.Submit(ctx, "oly-size"); err != nil {
		t.Fatalf("submitting: %v", err)
	}
	screen := waitFor(t, b, "oly-size", "cols=[")
	if strings.Contains(screen, "cols=[200]") {
		t.Errorf("the session is 200 columns wide, so the initial size was NOT ignored:\n%s", screen)
	}
}

var _ = zmx.New

// A backend with nowhere to keep a session label MUST say so rather than
// swallow the write. Silently accepting a status and reading back nothing would
// make a caller wait forever for a state that can never be reported.
func TestZmxHasNowhereToKeepAStatus(t *testing.T) {
	requireZmx(t)

	z := newBackend(t)
	ctx := context.Background()

	if z.Capabilities().SessionStatus {
		t.Fatal("zmx claims it can carry a session status")
	}
	err := z.SetStatus(ctx, "anything", "working")
	if backend.CodeOf(err) != backend.CodeUnsupported {
		t.Errorf("SetStatus reports %v, want UNSUPPORTED", backend.CodeOf(err))
	}
	if _, err := z.Status(ctx, "anything"); backend.CodeOf(err) != backend.CodeUnsupported {
		t.Errorf("Status reports %v, want UNSUPPORTED", backend.CodeOf(err))
	}
}

// §3.4 A pane row carries the session's process id, read from the `pid=`
// field of the long listing: the one live fact the row has, and the root the
// agent listing walks (§3.7). Zero would mean the field was not parsed, and
// a zeroed pid silently turns every agent under a shell invisible.
func TestPaneRowsCarryTheSessionsProcessID(t *testing.T) {
	b := newBackend(t)
	name := startShell(t, b, "oly-pid")

	panes, err := b.Panes(context.Background(), name)
	if err != nil {
		t.Fatalf("listing panes: %v", err)
	}
	if len(panes) == 0 {
		t.Fatal("no panes")
	}
	if panes[0].PID <= 0 {
		t.Errorf("the row has pid %d, want the session's process id from the listing's pid= field", panes[0].PID)
	}
}

// §4.6 A paste reaches a program that asked for bracketed paste as one framed
// paste.
//
// zmx has no framing of its own, so Olympus frames the text where the
// session's state says the program asked for it. Typed, a paste-aware program
// read a long text as several pastes split where the terminal's reads fell:
// Claude Code kept only the last.
func TestAPasteReachesABracketedPasteProgramFramed(t *testing.T) {
	b := newBackend(t)
	ctx := context.Background()
	const name = "oly-paste-framed"
	if _, err := b.Create(ctx, backend.CreateSpec{Name: name, Cols: 80, Rows: 24,
		Command: []string{"sh", "-c", `printf '\033[?2004hmode-on\n'; exec cat -v`}}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	t.Cleanup(func() { _ = b.Kill(context.Background(), name) })
	waitFor(t, b, name, "mode-on")

	if err := b.Paste(ctx, name, "framed-7"); err != nil {
		t.Fatalf("Paste: %v", err)
	}
	if err := b.Submit(ctx, name); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	// cat -v draws ESC as ^[, so a framed paste reads back as its markers.
	waitFor(t, b, name, "^[[200~framed-7^[[201~")
}

// §4.6 A program that did not ask for bracketed paste gets the raw write. The
// markers would otherwise reach it as text.
func TestAPasteToAProgramThatDidNotAskIsRaw(t *testing.T) {
	b := newBackend(t)
	ctx := context.Background()
	const name = "oly-paste-raw"
	if _, err := b.Create(ctx, backend.CreateSpec{Name: name, Cols: 80, Rows: 24,
		Command: []string{"sh", "-c", `printf 'mode-off\n'; exec cat -v`}}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	t.Cleanup(func() { _ = b.Kill(context.Background(), name) })
	waitFor(t, b, name, "mode-off")

	if err := b.Paste(ctx, name, "raw-8"); err != nil {
		t.Fatalf("Paste: %v", err)
	}
	if err := b.Submit(ctx, name); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	screen := waitFor(t, b, name, "raw-8\nraw-8")
	if strings.Contains(screen, "200~") {
		t.Errorf("a program that never asked for bracketed paste was sent the markers:\n%s", screen)
	}
}

// §4.3: injected text stays off zmx's argv, where every local user reads it in
// the process list while the call runs. zmx send reads its text from stdin when
// it is given none as an argument.
//
// A zmx ahead of the real one on PATH records every argument it is given, and
// each injection carries a text no argument may hold.
func TestInjectedTextIsOnNoArgv(t *testing.T) {
	requireZmx(t)
	log := recordArgv(t, "zmx")
	b := newIsolated(t)
	name := startShell(t, b, "oly-argv")
	warm(t, b, name)
	ctx := context.Background()

	// Short, since zmx does not rejoin a wrapped line and the prompt is long.
	secret := fmt.Sprintf("s%06d", time.Now().UnixNano()%1e6)
	for _, step := range []struct {
		op string
		fn func() error
	}{
		{"type", func() error { return b.Type(ctx, name, ": "+secret+"-t") }},
		{"paste", func() error { return b.Paste(ctx, name, " "+secret+"-p") }},
		{"atomic submit", func() error { return b.SendAtomic(ctx, name, " "+secret+"-a") }},
	} {
		if err := step.fn(); err != nil {
			t.Fatalf("%s: %v", step.op, err)
		}
	}
	waitFor(t, b, name, secret+"-t "+secret+"-p "+secret+"-a")

	argv, err := os.ReadFile(log)
	if err != nil {
		t.Fatalf("reading the recorded argv: %v", err)
	}
	if strings.Contains(string(argv), secret) {
		t.Errorf("the injected text was on zmx's argv:\n%s", argv)
	}
	if !strings.Contains(string(argv), "send") {
		t.Fatalf("the recording zmx never sent anything, so this proves nothing. It recorded:\n%s", argv)
	}
}

// §4.3: zmx send drops one trailing newline from what it reads on stdin, so a
// text that ends in one must still arrive with it. The reader takes exactly the
// bytes typed and prints them: a newline dropped leaves it waiting for a byte
// that never comes.
func TestATypedTrailingNewlineArrives(t *testing.T) {
	b := newBackend(t)
	ctx := context.Background()
	const name = "oly-trailing-nl"
	if _, err := b.Create(ctx, backend.CreateSpec{Name: name, Cols: 80, Rows: 24,
		Command: []string{"sh", "-c", `printf 'reading-%d\n' 4; head -c 4 | od -An -tx1; exec cat`}}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	t.Cleanup(func() { _ = b.Kill(context.Background(), name) })
	waitFor(t, b, name, "reading-4")

	if err := b.Type(ctx, name, "ab\n"); err != nil {
		t.Fatalf("Type: %v", err)
	}
	if err := b.Press(ctx, name, backend.Key("c-j")); err != nil {
		t.Fatalf("Press: %v", err)
	}
	waitFor(t, b, name, "61 62 0a 0a")
}

// §4.3: a text longer than one zmx send delivers for certain goes as several,
// and arrives whole and in order. A piece that ends in a newline keeps it, since
// zmx drops one trailing newline from each read of stdin.
func TestALongTextArrivesWholeAcrossSends(t *testing.T) {
	b := newBackend(t)
	ctx := context.Background()
	text := []byte(strings.Repeat("0123456789abcdef", 1000))
	text[4087] = '\n' // the last byte of the first send
	text[len(text)-1] = '\n'
	const name = "oly-long-text"
	reader := fmt.Sprintf(`stty -icanon -echo; printf 'reading-%%d\n' 5; head -c %d | tr -dc '\n' | wc -c | tr -d ' ' | sed 's/^/newlines-/'`, len(text))
	if _, err := b.Create(ctx, backend.CreateSpec{Name: name, Cols: 80, Rows: 24,
		Command: []string{"sh", "-c", reader + "; exec cat"}}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	t.Cleanup(func() { _ = b.Kill(context.Background(), name) })
	waitFor(t, b, name, "reading-5")

	if err := b.Type(ctx, name, string(text)); err != nil {
		t.Fatalf("Type: %v", err)
	}
	// Both newlines arrived only if every byte did: the reader stops at the
	// count it was given, and the second newline is the text's last byte. The
	// order the pieces arrive in is the conformance suite's checksum case.
	waitFor(t, b, name, "newlines-2")
}

// §4.1: empty text is not injected at all. Only the target's presence is
// checked, so a missing session is still not-found.
func TestInjectingNothingIsAPresenceCheck(t *testing.T) {
	b := newBackend(t)
	ctx := context.Background()
	name := startShell(t, b, "oly-empty")
	if err := b.Type(ctx, name, ""); err != nil {
		t.Errorf("typing nothing: %v", err)
	}
	if err := b.Paste(ctx, name, ""); err != nil {
		t.Errorf("pasting nothing: %v", err)
	}
	if err := b.Type(ctx, "oly-empty-missing", ""); backend.CodeOf(err) != backend.CodeSessionNotFound {
		t.Errorf("typing nothing into an absent session is %v, want not-found", err)
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
	script := fmt.Sprintf("#!/bin/sh\nfor a in \"$@\"; do printf '%%s\\n' \"$a\" >> '%s'; done\nexec '%s' \"$@\"\n", log, real)
	if err := os.WriteFile(filepath.Join(dir, bin), []byte(script), 0o755); err != nil {
		t.Fatalf("writing the recording %s: %v", bin, err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return log
}
