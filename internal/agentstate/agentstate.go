// Package agentstate reads a coding agent's state off its screen.
//
// The rules are herdr's agent-detection manifests, vendored under
// manifests/ (Apache 2.0, © herdr authors — see manifests/LICENSE-herdr and
// the NOTICE at the repository root), and the engine is a port of the
// evaluator they were written for. One manifest per agent, keyed by the
// canonical name the agent listing reports; an agent without one has no
// screen-readable state here.
package agentstate

import (
	"embed"
	"fmt"
	"io/fs"
	"sort"
	"strings"
	"sync"
)

// State is what a screen shows an agent doing.
type State string

// The states a manifest can name. Blocked is the agent waiting on a person:
// a permission prompt, a question, a trust dialog.
const (
	Working State = "working"
	Idle    State = "idle"
	Blocked State = "blocked"
	Unknown State = "unknown"
)

func parseState(s string) (State, error) {
	switch State(s) {
	case Working, Idle, Blocked, Unknown:
		return State(s), nil
	}
	return "", fmt.Errorf("unknown state %q", s)
}

// Vendored from herdr commit 9d4f05e5edc783ed4d9b27b1c629d45797298493
// (2026-09-02), src/detect/manifests/. Refresh by copying the directory over
// and re-running the tests: a pattern the port cannot compile, or a region
// or field it does not know, fails at load.
//
//go:embed manifests/*.toml
var manifestFS embed.FS

var (
	loadOnce  sync.Once
	loaded    map[string]*Manifest
	loadError error
)

// load parses every vendored manifest once, indexed by id and by alias.
func load() (map[string]*Manifest, error) {
	loadOnce.Do(func() {
		loaded, loadError = loadAll(manifestFS)
	})
	return loaded, loadError
}

func loadAll(fsys fs.FS) (map[string]*Manifest, error) {
	entries, err := fs.ReadDir(fsys, "manifests")
	if err != nil {
		return nil, err
	}
	out := map[string]*Manifest{}
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".toml") {
			continue
		}
		src, err := fs.ReadFile(fsys, "manifests/"+entry.Name())
		if err != nil {
			return nil, err
		}
		m, err := ParseManifest(string(src))
		if err != nil {
			return nil, fmt.Errorf("%s: %w", entry.Name(), err)
		}
		for _, name := range append([]string{m.ID}, m.Aliases...) {
			if other, dup := out[name]; dup && other != m {
				return nil, fmt.Errorf("%s: %q is also named by manifest %s", entry.Name(), name, other.ID)
			}
			out[name] = m
		}
	}
	return out, nil
}

// Lookup returns the manifest for an agent, by canonical name or alias.
// A load error in the vendored set is a programming error and panics: the
// manifests are compiled into the binary and a test loads every one.
func Lookup(agent string) (*Manifest, bool) {
	manifests, err := load()
	if err != nil {
		panic("agentstate: " + err.Error())
	}
	m, ok := manifests[agent]
	return m, ok
}

// Agents lists the canonical names that have a manifest, sorted.
func Agents() []string {
	manifests, err := load()
	if err != nil {
		panic("agentstate: " + err.Error())
	}
	var ids []string
	seen := map[string]bool{}
	for _, m := range manifests {
		if !seen[m.ID] {
			seen[m.ID] = true
			ids = append(ids, m.ID)
		}
	}
	sort.Strings(ids)
	return ids
}

// Detect reads an agent's state off its screen. Unknown for an agent with
// no manifest, and for a screen no rule recognises: a state the screen does
// not show is never guessed.
func Detect(agent string, in Input) State {
	m, ok := Lookup(agent)
	if !ok {
		return Unknown
	}
	in.Screen = strings.ReplaceAll(in.Screen, "\r\n", "\n")
	return m.Evaluate(in).State
}
