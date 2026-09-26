package tmux

import (
	"context"
	"errors"
	"os"
	"path/filepath"
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
	// tmux prints the kill-race message as a line of its own. A message that
	// merely quotes it, as one echoing a caller's argument can, is not absence.
	quoted := "unknown command: server exited unexpectedly"
	if err := classify(exit, quoted, []string{"has-session"}); isNoServer(err) || backend.CodeOf(err) != backend.CodeUnexpected {
		t.Errorf("%q is %q (no server %v), want %q", quoted, backend.CodeOf(err), isNoServer(err), backend.CodeUnexpected)
	}
}

// §12.3 A server that exits under new-session is not absence. Create was asked
// to make a session, and "not found" answers a question it was never asked.
// Driven through a stand-in tmux, since the window is too narrow to open on
// demand against a real one.
func TestCreateOnAServerThatExitsUnderItIsUnexpected(t *testing.T) {
	dir := t.TempDir()
	script := `#!/bin/sh
for arg in "$@"; do
	case "$arg" in
	list-sessions) echo "no server running on /tmp/o/s" >&2; exit 1 ;;
	new-session) echo "server exited unexpectedly" >&2; exit 1 ;;
	esac
done
exit 0
`
	if err := os.WriteFile(filepath.Join(dir, "tmux"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)

	_, err := New(WithSocketPath("/tmp/o/s")).Create(context.Background(), backend.CreateSpec{Name: "a"})
	if backend.CodeOf(err) != backend.CodeUnexpected {
		t.Errorf("Create on a server that exited under it reports %q, want %q: %v", backend.CodeOf(err), backend.CodeUnexpected, err)
	}
}
