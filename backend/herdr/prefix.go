package herdr

import (
	"bufio"
	"context"
	"os"
	"path/filepath"
	"strings"
)

// defaultPrefix is what herdr binds when config.toml names none.
const defaultPrefix = "ctrl+b"

// configuredPrefix reads a server's prefix out of its configuration
// (behavior §13.3): a config.toml beside the session where one exists, else
// the operator's, which is what every named session resolves under (§8.10).
// The file is scanned, not parsed: one key under one table is all that is
// wanted, and a TOML dependency is outside the budget. Absent ⇒ the default.
func configuredPrefix(sessionDir string) string {
	candidates := []string{}
	if sessionDir != "" {
		candidates = append(candidates, filepath.Join(sessionDir, "config.toml"))
	}
	if ambient := AmbientSocketPath(); ambient != "" {
		candidates = append(candidates, filepath.Join(filepath.Dir(ambient), "config.toml"))
	}
	for _, path := range candidates {
		if v, ok := prefixInConfig(path); ok {
			return spellPrefix(v)
		}
	}
	return spellPrefix(defaultPrefix)
}

// Prefix reports the prefix of the server this handle addresses: the
// config.toml beside its socket where there is one (a named session's
// directory), else the operator's (behavior §13.3, §8.10).
func (h *Herdr) Prefix(context.Context) (string, error) {
	return configuredPrefix(filepath.Dir(h.socketPath)), nil
}

// prefixInConfig finds `prefix = "…"` under `[keys]`. It reports false for a
// file it cannot read or that does not set one, so the next candidate is
// tried; a file that exists but is silent on the key is also false.
func prefixInConfig(path string) (string, bool) {
	f, err := os.Open(path)
	if err != nil {
		return "", false
	}
	defer f.Close()
	inKeys := false
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if strings.HasPrefix(line, "[") {
			inKeys = line == "[keys]"
			continue
		}
		if !inKeys {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok || strings.TrimSpace(key) != "prefix" {
			continue
		}
		value = strings.TrimSpace(value)
		if i := strings.Index(value, "#"); i > 0 {
			value = strings.TrimSpace(value[:i])
		}
		return strings.Trim(value, `"'`), true
	}
	return "", false
}

// spellPrefix rewrites herdr's `ctrl+space` into tmux's `C-Space`, so one
// spelling reaches a caller whichever backend answered: modifiers become
// their tmux letters, a named key is capitalised, a single character is kept.
func spellPrefix(v string) string {
	parts := strings.Split(strings.ToLower(strings.TrimSpace(v)), "+")
	if len(parts) == 0 || v == "" {
		return ""
	}
	var mods strings.Builder
	for _, m := range parts[:len(parts)-1] {
		switch m {
		case "ctrl", "control":
			mods.WriteString("C-")
		case "alt", "opt", "option", "meta":
			mods.WriteString("M-")
		case "shift":
			mods.WriteString("S-")
		case "super", "cmd", "command":
			mods.WriteString("Super-")
		}
	}
	key := parts[len(parts)-1]
	switch {
	case len(key) == 1:
	case key == "space":
		key = "Space"
	case strings.HasPrefix(key, "f") && len(key) <= 3:
		key = strings.ToUpper(key)
	default:
		key = strings.ToUpper(key[:1]) + key[1:]
	}
	return mods.String() + key
}
