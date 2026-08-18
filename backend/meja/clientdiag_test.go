package meja

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/husniadil/olympus/backend"
)

// §2.10: through meja 0.0.25 driving a session goes through an attached client,
// and when the transient one never arrives the operation fails with meja's own
// words — "command requires an attached client". (From 0.0.26 ordinary input
// takes no client, so this path is reached only on the floor and in copy mode;
// the unit tests below hold either way, since they build the error directly.)
//
// Those words describe the SYMPTOM and name no cause, which is exactly the
// position this repository was left in when every meja case failed at once,
// twice, and then would not reproduce. The evidence that would have settled it
// existed at the moment of failure and was thrown away: the client's own
// output went to io.Discard, and nothing recorded whether the process was
// still alive.
//
// So the give-up error carries what was observable when it gave up. These
// tests pin what it must carry, because an error that merely repeats the
// symptom is what made the burst undiagnosable.
func TestGivingUpNamesWhatTheClientWasDoing(t *testing.T) {
	base := errors.New("command requires an attached client")

	got := giveUp(base, "build", clientEvidence{
		Exited: true,
		Wait:   "exit status 1",
		Output: "meja: no current client",
	}).Error()

	for _, want := range []string{
		"command requires an attached client", // meja's own words, kept
		"build",                               // which session
		"exited",                              // the state that explains it
		"exit status 1",
		"no current client", // what the client itself said
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the give-up error does not mention %q:\n%s", want, got)
		}
	}
}

// A client that is STILL RUNNING when the budget expires is the more
// interesting case and the one the burst looked like: nothing crashed, the
// client simply never became usable. The error must distinguish the two, or it
// cannot tell "it died" from "it hung" — which are opposite bugs.
func TestGivingUpDistinguishesAHungClientFromADeadOne(t *testing.T) {
	base := errors.New("command requires an attached client")

	got := giveUp(base, "build", clientEvidence{
		Exited: false,
		Output: "\x1b[?1049h",
	}).Error()

	if strings.Contains(got, "exited") {
		t.Errorf("a live client was reported as exited:\n%s", got)
	}
	if !strings.Contains(got, "still running") {
		t.Errorf("the give-up error does not say the client was still running:\n%s", got)
	}
}

// The evidence is quoted, not pasted. A client's output is terminal escape
// bytes, and pasting them raw into an error means the error REDRAWS the
// operator's screen instead of describing a failure.
func TestTheClientsOutputIsQuotedRatherThanReplayed(t *testing.T) {
	got := giveUp(errors.New("x"), "build", clientEvidence{Output: "\x1b[2J"}).Error()

	if strings.Contains(got, "\x1b") {
		t.Errorf("a raw escape byte reached the error text, which would redraw the reader's terminal:\n%q", got)
	}
	if !strings.Contains(got, `\x1b[2J`) {
		t.Errorf("the client's output is not present in escaped form:\n%s", got)
	}
}

// Evidence that was not gathered must not read as evidence of absence: an
// empty output field means "it printed nothing OR nothing was captured", and
// claiming the client printed nothing would be a claim the code cannot make.
func TestAbsentEvidenceIsNotReportedAsAnObservation(t *testing.T) {
	got := giveUp(errors.New("x"), "build", clientEvidence{}).Error()

	if strings.Contains(got, "printed nothing") || strings.Contains(got, `""`) {
		t.Errorf("missing evidence was reported as an observation:\n%s", got)
	}
}

// meja has a SECOND client failure, and it is not the one above.
//
// "command requires an attached client" means the command never ran: meja
// refused it. "target client disconnected" means a client WAS there and went
// away underneath the command, so whether it ran is unknown. Opposite ends of
// one window, and the second arrived from CI while §5.6 was following — a case
// that attaches its own client, so Olympus never attached a transient one and
// the give-up diagnostic could not fire at all.
//
// Reported distinctly, because conflating them would claim the command did not
// run when nobody knows that.
func TestAVanishedClientIsNotReportedAsAMissingOne(t *testing.T) {
	got := vanished(errors.New("target client disconnected: exit status 1"), "build").Error()

	for _, want := range []string{
		"target client disconnected", // meja's own words, kept
		"build",
		"whether it ran is unknown", // the part that makes it actionable
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the vanished-client error does not mention %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "requires an attached client") {
		t.Errorf("a vanished client was described as a missing one:\n%s", got)
	}
}

// The two are told apart by their messages, and neither predicate may claim
// the other's. A predicate that matched both would send a half-delivered
// command back through the retry loop.
func TestTheTwoClientFailuresAreToldApart(t *testing.T) {
	missing := errors.New("command requires an attached client")
	gone := errors.New("target client disconnected: exit status 1")

	if !needsClient(missing) || clientGone(missing) {
		t.Error("a missing client was not classified as missing")
	}
	if !clientGone(gone) || needsClient(gone) {
		t.Error("a vanished client was not classified as vanished")
	}
	if needsClient(nil) || clientGone(nil) {
		t.Error("a nil error was classified as a client failure")
	}
}

// The give-up path must actually run on a real failure, not only assemble
// nicely in isolation. Squeezing the budget to nothing is the honest way to
// force it: the client genuinely has not arrived yet, which is the same
// condition as the burst, only reached on purpose.
func TestARealGiveUpCarriesTheEvidence(t *testing.T) {
	if testing.Short() {
		t.Skip("drives a real terminal")
	}
	if err := exec.Command("meja", "version").Run(); err != nil {
		t.Skip("meja is not installed or not runnable")
	}
	dir, err0 := os.MkdirTemp(os.TempDir(), "olym")
	if err0 != nil {
		t.Fatalf("MkdirTemp: %v", err0)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	socket := filepath.Join(dir, "m.sock")
	t.Cleanup(func() { _ = exec.Command("meja", "-S", socket, "kill-server").Run() })
	b := New(WithSocketPath(socket))
	ctx := context.Background()

	if _, err := b.Create(ctx, backend.CreateSpec{Name: "diag", Dir: t.TempDir()}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	restore := clientWait
	clientWait = time.Nanosecond
	defer func() { clientWait = restore }()

	err := b.Type(ctx, "diag", "x")
	if err == nil {
		// Not "the client was fast". From meja 0.0.26 ordinary input never
		// asks for a client (§2.10), so withClient returns on its first
		// attempt and the give-up path is UNREACHABLE here at any budget.
		// Said precisely, because a skip that misreports its reason reads as
		// a case that was tried and found fine — this one was not tried at
		// all. The path is covered on the 0.0.25 floor leg in CI.
		version, _ := b.Version(ctx)
		t.Skipf("this meja does not route ordinary input through a client, so the give-up path cannot be reached (%s)", version)
	}
	got := err.Error()
	for _, want := range []string{"diag", "transient client"} {
		if !strings.Contains(got, want) {
			t.Errorf("a real give-up does not mention %q:\n%s", want, got)
		}
	}
	t.Logf("give-up error as an operator would read it:\n%s", got)
}
