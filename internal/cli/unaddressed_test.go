package cli_test

import (
	"testing"

	"github.com/husniadil/olympus/backend"
)

// self, kinds and version address no backend, so an addressing option given
// to them would change nothing. It is refused rather than ignored (api §4).
func TestVerbsThatAddressNoBackendRefuseAddressing(t *testing.T) {
	for _, args := range [][]string{
		{"self", "--socket", "x"},
		{"self", "--backend", "tmux"},
		{"kinds", "--server", "x"},
		{"version", "--backend", "bogus"},
		{"version", "--no-lock"},
	} {
		got := run(t, append(args, "--json")...)
		if got.code != 2 {
			t.Errorf("%v exited %d, want 2\n%s%s", args, got.code, got.stdout, got.stderr)
			continue
		}
		if e := got.envelope(t); e.Error == nil || e.Error.Code != backend.CodeUsage {
			t.Errorf("%v: envelope %s, want USAGE", args, got.stdout)
		}
	}
}
