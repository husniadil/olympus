package cli_test

import (
	"os/exec"
	"testing"
)

// An addressing option the resolved backend cannot use fails after
// resolution, so the envelope names that backend (api §2).
func TestAUsageAfterResolutionNamesTheBackend(t *testing.T) {
	if err := exec.Command("zmx", "version").Run(); err != nil {
		t.Skipf("zmx does not run here: %v", err)
	}
	got := run(t, "ls", "--backend", "zmx", "--socket", "not-for-zmx", "--json")
	if got.code != 2 {
		t.Fatalf("exit %d, want 2\n%s%s", got.code, got.stdout, got.stderr)
	}
	if e := got.envelope(t); e.Backend != "zmx" {
		t.Errorf("envelope backend is %q, want zmx\n%s", e.Backend, got.stdout)
	}
}
