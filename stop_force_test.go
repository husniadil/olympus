package olympus

import (
	"testing"
	"time"
)

// Force skips the graceful attempt whatever else is given and in whatever
// order: a door that appends Presses or InterruptTimeout after it must not
// quietly turn a forced stop back into a graceful one.
func TestForceWinsOverAnyOrderOfStopOptions(t *testing.T) {
	for name, opts := range map[string][]StopOption{
		"force first": {Force(), Presses(3), InterruptTimeout(5 * time.Second)},
		"force last":  {Presses(3), InterruptTimeout(5 * time.Second), Force()},
	} {
		policy := stopPolicy(opts)
		if policy.Presses != 0 || policy.Timeout != 0 {
			t.Errorf("%s: policy is %+v, want no presses and no timeout", name, policy)
		}
	}
}
