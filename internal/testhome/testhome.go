// Package testhome gives a test binary a HOME of its own (behavior §2.9).
//
// A pane's login shell reads the profile under HOME, and the operator's can put
// another build of a backend on PATH ahead of the one under test: on herdr
// 0.8.2 a pane's `olympus self` then asked the operator's newer herdr and read
// nothing (measured). It can also leave test commands in the operator's shell
// history. Go's caches are pinned first, since they default to HOME.
package testhome

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Run runs the package's tests under a private HOME, with the configuration
// and state homes under it, and returns the exit code for os.Exit.
func Run(m *testing.M) int {
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
