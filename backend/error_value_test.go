package backend_test

import (
	"errors"
	"fmt"
	"io/fs"
	"testing"

	"github.com/husniadil/olympus/backend"
)

// api §3: the Go door returns typed errors matched with errors.Is against
// exported sentinels, with the code also readable from the error value.
var sentinels = map[backend.Code]error{
	backend.CodeUsage:              backend.ErrUsage,
	backend.CodeSessionNotFound:    backend.ErrNotFound,
	backend.CodeBackendUnavailable: backend.ErrUnavailable,
	backend.CodeTimeout:            backend.ErrTimeout,
	backend.CodeConflict:           backend.ErrConflict,
	backend.CodeUnsupported:        backend.ErrUnsupported,
}

func TestErrorMatchesOnlyItsOwnSentinel(t *testing.T) {
	for code, own := range sentinels {
		err := backend.Errorf(code, "something went wrong")
		if !errors.Is(err, own) {
			t.Errorf("errors.Is(%q error, its own sentinel) = false, want true", code)
		}
		for otherCode, other := range sentinels {
			if otherCode == code {
				continue
			}
			if errors.Is(err, other) {
				t.Errorf("errors.Is(%q error, %q sentinel) = true, want false", code, otherCode)
			}
		}
	}
}

func TestCodeIsReadableFromTheErrorValue(t *testing.T) {
	err := backend.Errorf(backend.CodeConflict, "lock held")
	if got := backend.CodeOf(err); got != backend.CodeConflict {
		t.Errorf("CodeOf = %q, want %q", got, backend.CodeConflict)
	}
}

// The code must survive an intermediate wrap, or every layer between the
// backend and the door has to re-tag failures by hand.
func TestCodeSurvivesWrapping(t *testing.T) {
	err := fmt.Errorf("resolving target: %w", backend.Errorf(backend.CodeSessionNotFound, "no session %q", "build"))
	if got := backend.CodeOf(err); got != backend.CodeSessionNotFound {
		t.Errorf("CodeOf through a wrap = %q, want %q", got, backend.CodeSessionNotFound)
	}
	if !errors.Is(err, backend.ErrNotFound) {
		t.Error("errors.Is through a wrap = false, want true")
	}
}

// §12: anything not carrying one of the codes is UNEXPECTED. A foreign error
// reaching a door must still classify, so the door never has a code-less error.
func TestForeignErrorClassifiesAsUnexpected(t *testing.T) {
	if got := backend.CodeOf(errors.New("some library failure")); got != backend.CodeUnexpected {
		t.Errorf("CodeOf(foreign) = %q, want %q", got, backend.CodeUnexpected)
	}
}

// No error is not an error: an empty code lets a caller distinguish "succeeded"
// from "failed for an unknown reason" without a nil check at every site.
func TestNilHasNoCode(t *testing.T) {
	if got := backend.CodeOf(nil); got != "" {
		t.Errorf("CodeOf(nil) = %q, want the empty code", got)
	}
}

// A cause must stay reachable, so a backend can attach the syscall or exec
// failure that explains the classification without the door losing it.
func TestCauseStaysUnwrappable(t *testing.T) {
	err := backend.Wrapf(backend.CodeBackendUnavailable, fs.ErrNotExist, "starting %s", "tmux")
	if !errors.Is(err, fs.ErrNotExist) {
		t.Error("errors.Is(err, cause) = false, want true")
	}
	if !errors.Is(err, backend.ErrUnavailable) {
		t.Error("errors.Is(err, sentinel) = false, want true")
	}
	if got := backend.CodeOf(err); got != backend.CodeBackendUnavailable {
		t.Errorf("CodeOf = %q, want %q", got, backend.CodeBackendUnavailable)
	}
}

func TestMessageReadsAsASentence(t *testing.T) {
	err := backend.Wrapf(backend.CodeBackendUnavailable, errors.New("exec: not found"), "starting %s", "tmux")
	if got, want := err.Error(), "starting tmux: exec: not found"; got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}
	plain := backend.Errorf(backend.CodeUsage, "unknown backend %q", "screen")
	if got, want := plain.Error(), `unknown backend "screen"`; got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}
}

// Wrapping nothing is a programmer slip, not a silent success: it must still
// produce a usable error rather than a nil the caller treats as OK.
func TestWrapfWithNoCauseIsStillAnError(t *testing.T) {
	err := backend.Wrapf(backend.CodeTimeout, nil, "waiting for %s", "prompt")
	if err == nil {
		t.Fatal("Wrapf with a nil cause returned nil")
	}
	if got, want := err.Error(), "waiting for prompt"; got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}
}

// The whole point of the vocabulary is that a door can turn any error into an
// exit status without knowing which layer produced it.
func TestAnyErrorReachesAnExitStatus(t *testing.T) {
	cases := map[error]int{
		backend.Errorf(backend.CodeUsage, "bad flag"): 2,
		errors.New("some library failure"):            1,
	}
	for err, want := range cases {
		if got := backend.ExitCode(backend.CodeOf(err)); got != want {
			t.Errorf("ExitCode(CodeOf(%v)) = %d, want %d", err, got, want)
		}
	}
}
