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
