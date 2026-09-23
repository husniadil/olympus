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
	// A "#" inside the quotes is the key, not a comment.
	if v, ok := prefixInConfig(write("c.toml", "[keys]\nprefix = \"ctrl+#\" # hash\n")); !ok || v != "ctrl+#" {
		t.Errorf("read %q %v, want ctrl+#", v, ok)
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

// §13.3 The fallback is the operator's config.toml, found where herdr finds
// it — under the configuration home — and not beside whatever socket
// HERDR_SOCKET_PATH names, which inside an Olympus session is Olympus's own.
func TestThePrefixFallbackIsTheOperatorsConfigWhateverTheSocketOverride(t *testing.T) {
	dir := t.TempDir()
	operator := filepath.Join(dir, "config", "herdr")
	if err := os.MkdirAll(operator, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(operator, "config.toml"), []byte("[keys]\nprefix = \"ctrl+space\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(dir, "config"))
	t.Setenv("HERDR_SOCKET_PATH", filepath.Join(dir, "elsewhere", "herdr.sock"))
	if got := configuredPrefix(filepath.Join(dir, "session")); got != "C-Space" {
		t.Errorf("the fallback prefix is %q, want the operator's C-Space", got)
	}

	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("HOME", dir)
	home := filepath.Join(dir, ".config", "herdr")
	if err := os.MkdirAll(home, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte("[keys]\nprefix = \"alt+q\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := configuredPrefix(filepath.Join(dir, "session")); got != "M-q" {
		t.Errorf("with no configuration home the fallback prefix is %q, want M-q from $HOME/.config/herdr", got)
	}
}
