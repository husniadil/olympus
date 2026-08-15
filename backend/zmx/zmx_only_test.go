package zmx_test

import (
	"context"
	"errors"
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

	without, err := b.Screen(ctx, name, backend.ScreenOpts{})
	if err != nil {
		t.Fatalf("capturing: %v", err)
	}
	with, err := b.Screen(ctx, name, backend.ScreenOpts{HistoryLines: 500})
	if err != nil {
		t.Fatalf("capturing with history: %v", err)
	}
	if without.Text != with.Text {
		t.Errorf("requesting history changed the output, but on this backend it must be a no-op")
	}
	// Both must already carry the scrolled-off lines, which is why the request
	// has nothing left to do.
	if !strings.Contains(without.Text, "h-1\n") {
		t.Errorf("the default capture does not reach back to the first line, so it is not full scrollback")
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
		"create a view": func() error { _, err := b.CreateView(ctx, name, "olympus-view-x-1"); return err }(),
		"scroll a view": b.ScrollView(ctx, name, 1),
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
	if _, err := b.Create(context.Background(), backend.CreateSpec{Name: "oly-size", Cols: 200, Rows: 60}); err != nil {
		t.Fatalf("creating with a size: %v", err)
	}
	t.Cleanup(func() { _ = b.Kill(context.Background(), "oly-size") })
}

var _ = zmx.New
