package olympus

import (
	"context"
	"errors"
	"testing"
	"time"
)

// White-box on purpose: the seam under test is one unexported step of Diagnose,
// and reaching it from outside would mean running a backend binary that hangs —
// which is the very thing that cannot be arranged portably.

type blockingProber struct{ started chan struct{} }

func (b *blockingProber) Version(ctx context.Context) (string, error) {
	close(b.started)
	<-ctx.Done()
	return "", ctx.Err()
}

type instantProber struct{ version string }

func (i instantProber) Version(context.Context) (string, error) { return i.version, nil }

// §0.6: the diagnostic is what every backend-unavailable error points at, and
// what turns "it does not work on my machine" into one command's output.
//
// So it must ALWAYS answer. A version probe is a subprocess, and a subprocess
// can hang — this one was found hanging for minutes because the binary on PATH
// was a version-manager shim that went to the network when HOME changed
// underneath it. Diagnose inherited the caller's context, which is normally
// Background, so `olympus doctor` hung with no output and no way to tell which
// backend was responsible: the worst possible behaviour from the command whose
// entire job is to explain a broken environment.
func TestAHangingVersionProbeDoesNotHangTheDiagnostic(t *testing.T) {
	prober := &blockingProber{started: make(chan struct{})}

	done := make(chan error, 1)
	go func() {
		_, err := probeVersion(context.Background(), prober)
		done <- err
	}()

	<-prober.started
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("a probe that never returns reported success")
		}
	case <-time.After(versionProbeBudget + 5*time.Second):
		t.Fatal("the probe was not bounded, so the diagnostic can hang forever")
	}
}

// The bound must not become a delay every caller pays. A backend that answers
// immediately is reported immediately.
func TestAProbeThatAnswersIsNotDelayed(t *testing.T) {
	start := time.Now()
	version, err := probeVersion(context.Background(), instantProber{version: "3.7b"})
	if err != nil {
		t.Fatalf("probeVersion: %v", err)
	}
	if version != "3.7b" {
		t.Errorf("probeVersion reported %q, want %q", version, "3.7b")
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("a probe that answered at once took %s", elapsed)
	}
}

// A caller who cancels is obeyed sooner than the budget. The budget is a
// backstop for a hung binary, not a floor on how long a cancel takes.
func TestACancelledCallerIsNotMadeToWaitForTheBudget(t *testing.T) {
	prober := &blockingProber{started: make(chan struct{})}
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error, 1)
	go func() {
		_, err := probeVersion(ctx, prober)
		done <- err
	}()

	<-prober.started
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("a cancelled probe returned %v, want a cancellation", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("cancelling the caller's context did not stop the probe")
	}
}
