package tmux

import (
	"strings"
	"testing"
)

// §1.3: the attach client builds its own environment, and the strip is the part
// with teeth.
//
// The rule had no test on either backend — the audit that looked for uncited
// sections found this as the one genuine gap rather than a missing citation. On
// tmux the leak is a nested-session hazard: an attach client that inherits TMUX
// and TMUX_PANE from the session it was launched inside is a client claiming to
// already be in a terminal it is not in.
//
// White-box on purpose. The environment is what the client is handed, and
// nothing observable from outside distinguishes "stripped it" from "the variable
// happened to be unset".
func TestAttachEnvironmentStripsTheAmbientSessionVariables(t *testing.T) {
	for _, leaked := range []string{"TMUX", "TMUX_PANE", "ZMX_SESSION", "ZMX_SESSION_PREFIX"} {
		t.Setenv(leaked, "ambient-value")
	}

	env := attachEnv()
	for _, leaked := range []string{"TMUX", "TMUX_PANE", "ZMX_SESSION", "ZMX_SESSION_PREFIX"} {
		if value, ok := lookup(env, leaked); ok {
			t.Errorf("%s survived into the attach environment as %q", leaked, value)
		}
	}
}

// §1.3: "an interactive attach MUST inherit the operator's real TERM — forcing
// xterm-256color would misrepresent the terminal the human is sitting at."
//
// This is the difference from the spawn environment, which DOES force one. A
// change that made attach reuse spawnEnv would look like a tidy-up and would
// quietly lie to every full-screen program about the terminal it draws on.
func TestAttachInheritsTheOperatorsRealTerm(t *testing.T) {
	t.Setenv("TERM", "screen-256color")

	if value, ok := lookup(attachEnv(), "TERM"); !ok || value != "screen-256color" {
		t.Errorf("attach TERM is %q (present=%v), want the operator's own", value, ok)
	}
	// tmux spells its spawn-side environment clientEnv, and that one DOES
	// force a TERM. The two paths being different is the rule, not an accident.
	if value, _ := lookup(clientEnv(), "TERM"); value == "screen-256color" {
		t.Error("the client environment inherited TERM, so the two paths are no longer distinct")
	}
}

// §1.3 defers the LANG default to §1.1: a client with no LANG gets one, and a
// client that has one keeps it.
func TestAttachDefaultsLangOnlyWhenItIsMissing(t *testing.T) {
	t.Setenv("LANG", "")
	if value, ok := lookup(attachEnv(), "LANG"); !ok || value != defaultLang {
		t.Errorf("attach LANG is %q (present=%v), want the default %q", value, ok, defaultLang)
	}

	t.Setenv("LANG", "fr_FR.UTF-8")
	if value, _ := lookup(attachEnv(), "LANG"); value != "fr_FR.UTF-8" {
		t.Errorf("attach LANG is %q, want the operator's own", value)
	}
}

// §1.1: meja's pane identity and herdr's nesting marker are stripped like the
// others. A session created from inside a meja pane would otherwise inherit
// MEJA_SESSION_TARGET and answer "I am in a meja pane" as well, so asking it
// where it is gets "nested" instead of its own address.
func TestSpawnEnvironmentStripsMejaIdentityAndHerdrNesting(t *testing.T) {
	leaked := []string{"MEJA_SESSION_TARGET", "MEJA_PANE_ID", "MEJA_SOCKET", "HERDR_ENV"}
	for _, name := range leaked {
		t.Setenv(name, "ambient-value")
	}
	for _, name := range leaked {
		if value, ok := lookup(clientEnv(), name); ok {
			t.Errorf("%s survived into the client environment as %q", name, value)
		}
		if value, ok := lookup(sessionEnv(), name); !ok || value != "" {
			t.Errorf("%s is not set to empty per session (%q, present=%v)", name, value, ok)
		}
	}
}

// lookup reads the LAST assignment, which is what exec applies when a name
// appears more than once.
func lookup(env []string, name string) (string, bool) {
	value, found := "", false
	for _, kv := range env {
		if rest, ok := strings.CutPrefix(kv, name+"="); ok {
			value, found = rest, true
		}
	}
	return value, found
}
