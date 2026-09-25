package tmux

import (
	"errors"
	"testing"

	"github.com/husniadil/olympus/backend"
)

// §3.5 Only a socket with nothing behind it is absence. A socket the client
// cannot open — no permission, a path too long — is a backend that cannot be
// reached, and reading it as absence makes a probe say absent and a kill
// report success while the session runs on.
func TestOnlyANoServerFailureIsReadAsAbsence(t *testing.T) {
	exit := errors.New("exit status 1")
	absent := []string{
		"no server running on /tmp/o/s",
		"error connecting to /tmp/o/s (No such file or directory)",
		"error connecting to /tmp/o/s (Connection refused)",
		"server exited unexpectedly",
	}
	for _, stderr := range absent {
		err := classify(exit, stderr, []string{"has-session"})
		if !isNoServer(err) || backend.CodeOf(err) != backend.CodeSessionNotFound {
			t.Errorf("%q is %q (no server %v), want absence", stderr, backend.CodeOf(err), isNoServer(err))
		}
	}
	unreachable := []string{
		"error connecting to /tmp/o/s (Permission denied)",
		"error connecting to /tmp/o/s (File name too long)",
	}
	for _, stderr := range unreachable {
		err := classify(exit, stderr, []string{"has-session"})
		if isNoServer(err) || backend.CodeOf(err) != backend.CodeBackendUnavailable {
			t.Errorf("%q is %q (no server %v), want %q", stderr, backend.CodeOf(err), isNoServer(err), backend.CodeBackendUnavailable)
		}
	}
}
