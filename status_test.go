package olympus_test

import (
	"context"
	"testing"
	"time"

	"github.com/husniadil/olympus"
)

// The whole story, end to end: a program inside a session reports what it is
// doing, and a caller outside blocks until it says the thing they care about.
//
// The reporter is given NO arguments — not the session's name, not the socket.
// It has to work that out from where it is, because a program that must be told
// its own address cannot report anything a launcher did not already know.
//
// This is the case that isolation makes hard: the session is on a private
// socket or directory, so a reporter that resolves its name but not its server
// writes a status onto a completely different backend and the waiter times out
// against a session that never hears anything.
func TestAProcessInsideASessionReportsItsOwnStatus(t *testing.T) {
	binary := buildOlympus(t)

	for _, l := range legs(t) {
		t.Run(l.name, func(t *testing.T) {
			ol := l.open(t)
			if !ol.Capabilities().SessionStatus {
				t.Skipf("%s cannot carry a session status", l.name)
			}
			s := session(t, ol)
			ctx := context.Background()

			reported := make(chan error, 1)
			go func() {
				_, err := s.WaitForStatus(ctx, "ready", olympus.WaitTimeout(20*time.Second))
				reported <- err
			}()

			// No target, no --backend, no --socket: everything comes from the
			// environment the session itself provides.
			if _, err := s.Exec(ctx, binary+" status --set ready"); err != nil {
				t.Fatalf("reporting from inside the session: %v", err)
			}

			if err := <-reported; err != nil {
				got, _ := s.Status(context.Background())
				t.Fatalf("the waiter never saw the status the session reported: %v (status now %q)", err, got)
			}
		})
	}
}
