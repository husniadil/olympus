package olympus

import (
	"errors"
	"strings"
	"testing"

	"github.com/husniadil/olympus/backend"
)

// installed builds a preflight stand-in, so resolution is tested against every
// combination rather than against whatever this machine happens to have.
func installed(names ...backend.Name) installedFunc {
	set := map[backend.Name]bool{}
	for _, n := range names {
		set[n] = true
	}
	return func(n backend.Name) bool { return set[n] }
}

func TestTheDefaultBackendIsZmx(t *testing.T) {
	got, err := resolve("", "", installed(backend.Zmx, backend.Tmux))
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if got.Backend != backend.Zmx {
		t.Errorf("resolved %q, want %q", got.Backend, backend.Zmx)
	}
	if got.Reason != ReasonDefault {
		t.Errorf("reason %q, want %q", got.Reason, ReasonDefault)
	}
}

func TestResolutionOrderPrefersTheExplicitChoice(t *testing.T) {
	got, err := resolve("tmux", "zmx", installed(backend.Zmx, backend.Tmux))
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if got.Backend != backend.Tmux || got.Reason != ReasonFlag {
		t.Errorf("resolved %q by %q, want tmux by flag", got.Backend, got.Reason)
	}
}

func TestTheEnvironmentBeatsTheDefault(t *testing.T) {
	got, err := resolve("", "tmux", installed(backend.Zmx, backend.Tmux))
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if got.Backend != backend.Tmux || got.Reason != ReasonEnv {
		t.Errorf("resolved %q by %q, want tmux by env", got.Backend, got.Reason)
	}
}

// The fallback exists so a host with a working multiplexer is not refused for
// no gain — but it applies to the DEFAULT only.
func TestTheDefaultFallsBackWhenItIsNotInstalled(t *testing.T) {
	got, err := resolve("", "", installed(backend.Tmux))
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if got.Backend != backend.Tmux {
		t.Errorf("resolved %q, want %q", got.Backend, backend.Tmux)
	}
	// The reason must say fallback, not default. Sessions are backend-scoped,
	// so a user who later installs the default would find their sessions
	// apparently vanished with nothing explaining why.
	if got.Reason != ReasonFallback {
		t.Errorf("reason %q, want %q", got.Reason, ReasonFallback)
	}
}

// An explicit choice that cannot be honoured fails loudly. Silently running
// somewhere the caller did not ask for is worse than failing.
func TestAnExplicitChoiceNeverFallsBack(t *testing.T) {
	for _, source := range []struct {
		what          string
		explicit, env string
	}{
		{"a flag", "zmx", ""},
		{"the environment", "", "zmx"},
	} {
		_, err := resolve(source.explicit, source.env, installed(backend.Tmux))
		if !errors.Is(err, backend.ErrUnavailable) {
			t.Errorf("%s selecting an uninstalled backend gave %v, want unavailable", source.what, err)
		}
		if err != nil && !strings.Contains(err.Error(), "zmx") {
			t.Errorf("%s: the error %q does not name the backend that was asked for", source.what, err)
		}
	}
}

// One corrected argument fixes this, which is the definition of a usage error.
// Unexpected-class would tell a machine consumer that retrying will not help —
// the opposite of the truth.
func TestAnUnknownBackendNameIsAUsageError(t *testing.T) {
	for _, source := range []struct{ explicit, env string }{
		{"screen", ""},
		{"", "screen"},
	} {
		_, err := resolve(source.explicit, source.env, installed(backend.Zmx, backend.Tmux))
		if !errors.Is(err, backend.ErrUsage) {
			t.Errorf("an unknown backend gave %v, want a usage error", err)
		}
		// The message has to name the legal values, or the caller is guessing.
		for _, want := range []string{"screen", "zmx", "tmux"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("the error %q does not mention %q", err.Error(), want)
			}
		}
	}
}

// A missing binary must not surface as a raw exec error: that tells a
// first-time user nothing about what Olympus needs.
func TestAMissingBinaryIsExplainedWithAnInstallHint(t *testing.T) {
	_, err := resolve("tmux", "", installed())
	if err == nil {
		t.Fatal("selecting an uninstalled backend succeeded")
	}
	for _, want := range []string{"tmux", "PATH", "install", "doctor"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the error does not mention %q:\n%s", want, err.Error())
		}
	}
}

// One complete message, not a failure per attempted backend — and it has to say
// what Olympus IS, because a first-time user hitting this does not yet know it
// drives a multiplexer rather than embedding one.
func TestNoBackendInstalledIsOneCompleteMessage(t *testing.T) {
	_, err := resolve("", "", installed())
	if !errors.Is(err, backend.ErrUnavailable) {
		t.Fatalf("error is %v, want unavailable", err)
	}
	for _, want := range []string{"zmx", "tmux", "doctor", "drives an existing multiplexer"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the error does not mention %q:\n%s", want, err.Error())
		}
	}
}

// Whitespace around a name is a typo, not a different backend.
func TestSurroundingWhitespaceIsIgnored(t *testing.T) {
	got, err := resolve("  tmux ", "", installed(backend.Tmux))
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if got.Backend != backend.Tmux {
		t.Errorf("resolved %q, want %q", got.Backend, backend.Tmux)
	}
}

// §0.3: a third backend is selectable by name, and joins the fallback chain
// LAST.
//
// Last is the whole point. The standing order is zmx by default and tmux when
// it is absent; a new backend that displaced either would silently move a
// caller's sessions to a backend they never chose, and sessions never migrate
// between backends. Meja answers only when it is the last one standing.
func TestMejaIsSelectableAndFallsBackLast(t *testing.T) {
	all := installed(backend.Zmx, backend.Tmux, backend.Meja)
	onlyMeja := installed(backend.Meja)

	chosen, err := resolve("meja", "", all)
	if err != nil {
		t.Fatalf("selecting meja explicitly: %v", err)
	}
	if chosen.Backend != backend.Meja || chosen.Reason != ReasonFlag {
		t.Errorf("explicit meja resolved to %+v", chosen)
	}

	// With everything installed, meja must not displace the standing default.
	chosen, err = resolve("", "", all)
	if err != nil {
		t.Fatalf("resolving with everything installed: %v", err)
	}
	if chosen.Backend == backend.Meja {
		t.Errorf("meja displaced the default backend: %+v", chosen)
	}

	// Last one standing: refusing here would be hostile on a host with a
	// working multiplexer installed.
	chosen, err = resolve("", "", onlyMeja)
	if err != nil {
		t.Fatalf("resolving with only meja installed: %v", err)
	}
	if chosen.Backend != backend.Meja || chosen.Reason != ReasonFallback {
		t.Errorf("with only meja installed, resolution is %+v", chosen)
	}
}

// §0.3: with nothing chosen, the order is zmx, then tmux, then meja — and each
// step down is taken only because the one above it is absent.
//
// Tested as a walk down the whole chain rather than at its ends. The ends
// already passed before meja existed; what a third backend puts at risk is the
// MIDDLE, where two candidates are available at once and only one is correct.
// An ordering nobody asserts is one a later change can reverse in silence.
func TestFallbackWalksTheChainInOrder(t *testing.T) {
	for _, c := range []struct {
		name      string
		available installedFunc
		want      backend.Name
		reason    Reason
	}{
		{"everything installed", installed(backend.Zmx, backend.Tmux, backend.Meja), backend.Zmx, ReasonDefault},
		{"no zmx", installed(backend.Tmux, backend.Meja), backend.Tmux, ReasonFallback},
		{"no zmx and no tmux", installed(backend.Meja), backend.Meja, ReasonFallback},
	} {
		t.Run(c.name, func(t *testing.T) {
			got, err := resolve("", "", c.available)
			if err != nil {
				t.Fatalf("resolve: %v", err)
			}
			if got.Backend != c.want {
				t.Errorf("resolved %q, want %q", got.Backend, c.want)
			}
			// The reason is half the answer. A fallback that reports itself as
			// the default hides a substitution the caller has to know about:
			// sessions are backend-scoped, so one made under a fallback is
			// invisible once the preferred backend comes back (§0.4).
			if got.Reason != c.reason {
				t.Errorf("reason %q, want %q", got.Reason, c.reason)
			}
		})
	}
}

// §0.1: an addressing option the resolved backend cannot use is a USAGE error,
// never a silent no-op.
//
// Silence here is dangerous rather than merely untidy. Every one of these
// options exists to ISOLATE — to put a server somewhere the caller controls —
// so dropping one lands them on the shared default while they believe they are
// alone on a private one. Measured before this rule existed: `--backend meja
// --socket <private>` resolved to meja's default profile and exited 0.
func TestAnOptionTheBackendCannotUseIsUsage(t *testing.T) {
	for _, c := range []struct {
		name    string
		backend string
		opt     Option
		names   []string
	}{
		{"a zmx directory on tmux", "tmux", WithZmxDir("/tmp/x"), []string{"zmx-dir", "tmux"}},
		{"a socket name on zmx", "zmx", WithSocket("x"), []string{"socket", "zmx"}},
		{"a socket path on zmx", "zmx", WithSocketPath("/tmp/x.sock"), []string{"socket-path", "zmx"}},
		{"a socket name on meja", "meja", WithSocket("x"), []string{"socket", "meja"}},
		{"a zmx directory on meja", "meja", WithZmxDir("/tmp/x"), []string{"zmx-dir", "meja"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, err := open(config{
				explicit: c.backend,
				installs: func(backend.Name) bool { return true },
			}, c.opt)
			if CodeOf(err) != backend.CodeUsage {
				t.Fatalf("error is %v (%v), want USAGE", err, CodeOf(err))
			}
			// The message has to name BOTH the option and the backend, or the
			// caller is told something is wrong without being told what to
			// change.
			for _, want := range c.names {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("the message does not name %q: %s", want, err)
				}
			}
		})
	}
}

// The same options are accepted where they DO apply, which is what stops the
// rule above from being a blanket refusal.
func TestAnOptionTheBackendCanUseIsAccepted(t *testing.T) {
	for _, c := range []struct {
		name    string
		backend string
		opt     Option
	}{
		{"a socket name on tmux", "tmux", WithSocket("x")},
		{"a socket path on tmux", "tmux", WithSocketPath("/tmp/x.sock")},
		{"a socket path on meja", "meja", WithSocketPath("/tmp/x.sock")},
		{"a zmx directory on zmx", "zmx", WithZmxDir("/tmp/x")},
	} {
		t.Run(c.name, func(t *testing.T) {
			ol, err := open(config{
				explicit: c.backend,
				installs: func(backend.Name) bool { return true },
			}, c.opt)
			if err != nil {
				t.Fatalf("open: %v", err)
			}
			_ = ol.Close()
		})
	}
}

// §11: the write lock keys on the backend's scope, so two doors pointed at the
// same zmx daemon must compute the same one.
//
// They did not. Open recorded only what a caller passed, while the diagnostic
// fell back to ZMX_DIR — so a CLI run with --zmx-dir and an MCP server with
// ZMX_DIR exported addressed one daemon under two different keys and serialized
// against nothing.
func TestZmxScopeFallsBackToTheEnvironmentSoLockKeysAgree(t *testing.T) {
	t.Setenv("ZMX_DIR", "/tmp/oly-shared-daemon")

	passed, err := open(config{explicit: "zmx", installs: func(backend.Name) bool { return true }},
		WithZmxDir("/tmp/oly-shared-daemon"))
	if err != nil {
		t.Fatalf("open with the directory passed: %v", err)
	}
	inherited, err := open(config{explicit: "zmx", installs: func(backend.Name) bool { return true }})
	if err != nil {
		t.Fatalf("open with the directory inherited: %v", err)
	}
	if passed.lockKey("s") != inherited.lockKey("s") {
		t.Errorf("the same daemon is locked under two keys:\n  passed:    %+v\n  inherited: %+v",
			passed.lockKey("s"), inherited.lockKey("s"))
	}
}
