package herdr

import (
	"os"
	"path/filepath"
	"testing"
)

// §13.3 The prefix is spelled the way tmux spells it, whichever backend
// answered, so a caller has one form to turn into bytes.
func TestPrefixSpellingIsTmuxs(t *testing.T) {
	for in, want := range map[string]string{
		"ctrl+b":       "C-b",
		"ctrl+space":   "C-Space",
		"alt+a":        "M-a",
		"f19":          "F19",
		"ctrl+shift+p": "C-S-p",
		"`":            "`",
	} {
		if got := spellPrefix(in); got != want {
			t.Errorf("spellPrefix(%q) = %q, want %q", in, got, want)
		}
	}
}

// The prefix is scanned out of config.toml's [keys] table; a file silent on
// it, or absent, falls through to the next candidate and finally the default.
func TestConfiguredPrefixReadsTheKeysTable(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	if v, ok := prefixInConfig(write("a.toml", "[ui]\nprefix = \"no\"\n[keys]\n# prefix = \"x\"\nprefix = \"ctrl+space\" # trailing\n")); !ok || v != "ctrl+space" {
		t.Errorf("read %q %v, want ctrl+space", v, ok)
	}
	if _, ok := prefixInConfig(write("b.toml", "[keys]\nsplit = \"x\"\n")); ok {
		t.Error("a [keys] table without prefix reported one")
	}
	if _, ok := prefixInConfig(filepath.Join(dir, "missing.toml")); ok {
		t.Error("a missing file reported a prefix")
	}
	// A session directory with its own config wins over the ambient one.
	sess := filepath.Join(dir, "sess")
	_ = os.MkdirAll(sess, 0o700)
	write(filepath.Join("sess", "config.toml"), "[keys]\nprefix = \"alt+x\"\n")
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(dir, "nowhere"))
	if got := configuredPrefix(sess); got != "M-x" {
		t.Errorf("session config prefix = %q, want M-x", got)
	}
	if got := configuredPrefix(filepath.Join(dir, "other")); got != "C-b" {
		t.Errorf("with no config the prefix is %q, want the default C-b", got)
	}
}
