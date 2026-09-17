package olympus

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestMain gives the whole package a HOME of its own (§2.9). A pane's login
// shell reads the profile under HOME, and the operator's can put another build
// of a backend on PATH ahead of the one under test: on herdr 0.8.2 the pane's
// `olympus self` then asked the operator's newer herdr, and read nothing
// (measured). Go's caches are pinned first, since they default to HOME.
func TestMain(m *testing.M) {
	os.Exit(runWithOwnHome(m))
}

func runWithOwnHome(m *testing.M) int {
	for _, key := range []string{"GOCACHE", "GOMODCACHE", "GOPATH"} {
		if os.Getenv(key) != "" {
			continue
		}
		out, err := exec.Command("go", "env", key).Output()
		if err != nil {
			fmt.Fprintf(os.Stderr, "reading go env %s: %v\n", key, err)
			return 1
		}
		os.Setenv(key, strings.TrimSpace(string(out)))
	}
	home, err := os.MkdirTemp("", "olyhome")
	if err != nil {
		fmt.Fprintf(os.Stderr, "making a test HOME: %v\n", err)
		return 1
	}
	defer os.RemoveAll(home)
	os.Setenv("HOME", home)
	os.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	os.Setenv("XDG_STATE_HOME", filepath.Join(home, ".local", "state"))
	return m.Run()
}
