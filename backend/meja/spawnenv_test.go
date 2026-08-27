package meja

import (
	"context"
	"strings"
	"testing"
)

// §1.1: every session Olympus creates is spawned with a sanitized environment,
// and this backend had none at all — every meja invocation simply inherited the
// process environment, so a session created from inside tmux or zmx carried
// that multiplexer's identity into the pane.
//
// White-box on purpose, like the tmux and zmx cases. The environment is what
// the invocation is handed, and nothing observable from outside distinguishes
// "stripped it" from "the variable happened to be unset".
func TestSpawnEnvironmentStripsTheAmbientSessionVariables(t *testing.T) {
	for _, leaked := range []string{"TMUX", "TMUX_PANE", "ZMX_SESSION", "ZMX_SESSION_PREFIX"} {
		t.Setenv(leaked, "ambient-value")
	}

	env := spawnEnv()
	for _, leaked := range []string{"TMUX", "TMUX_PANE", "ZMX_SESSION", "ZMX_SESSION_PREFIX"} {
		if value, ok := lookup(env, leaked); ok {
			t.Errorf("%s survived into the spawn environment as %q", leaked, value)
		}
	}
}

// §1.1: TERM is forced and LANG is defaulted only when it is missing.
func TestSpawnEnvironmentForcesTermAndDefaultsLang(t *testing.T) {
	t.Setenv("TERM", "screen-256color")
	t.Setenv("LANG", "")

	env := spawnEnv()
	if value, _ := lookup(env, "TERM"); value != spawnTerm {
		t.Errorf("spawn TERM is %q, want the forced %q", value, spawnTerm)
	}
	if value, ok := lookup(env, "LANG"); !ok || value != defaultLang {
		t.Errorf("spawn LANG is %q (present=%v), want the default %q", value, ok, defaultLang)
	}

	t.Setenv("LANG", "fr_FR.UTF-8")
	if value, _ := lookup(spawnEnv(), "LANG"); value != "fr_FR.UTF-8" {
		t.Errorf("spawn LANG is %q, want the operator's own", value)
	}
}

// §1.1 applies to the spawn PATH, not merely to a function that exists. Every
// session-creating invocation goes through command, so this is where the rule
// either reaches new-session or does not.
func TestTheSpawnInvocationCarriesTheSanitizedEnvironment(t *testing.T) {
	t.Setenv("TMUX", "ambient-value")
	t.Setenv("TERM", "screen-256color")

	cmd := New().command(context.Background(), "new-session", "-d", "-s", "build")
	if cmd.Env == nil {
		t.Fatal("the invocation inherits the process environment, so §1.1 sanitizes nothing")
	}
	if value, ok := lookup(cmd.Env, "TMUX"); ok {
		t.Errorf("TMUX reached the spawn invocation as %q", value)
	}
	if value, _ := lookup(cmd.Env, "TERM"); value != spawnTerm {
		t.Errorf("the spawn invocation's TERM is %q, want the forced %q", value, spawnTerm)
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
