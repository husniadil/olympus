package cli

import (
	"bytes"
	"testing"

	"github.com/husniadil/olympus"
)

// A pending poll that could not confirm the session's liveness says why, as a
// died one does; a bare "pending" hides the doubt from a person (§6.8).
func TestAHumanPollSaysWhyItIsPending(t *testing.T) {
	for _, c := range []struct {
		result olympus.PollResult
		want   string
	}{
		{olympus.PollResult{State: "pending"}, "pending\n"},
		{olympus.PollResult{State: "pending", Reason: "the listing failed"}, "pending: the listing failed\n"},
		{olympus.PollResult{State: "died", Reason: "the session is gone"}, "died: the session is gone\n"},
	} {
		var out bytes.Buffer
		renderPoll(&out, c.result)
		if out.String() != c.want {
			t.Errorf("%+v renders %q, want %q", c.result, out.String(), c.want)
		}
	}
}
