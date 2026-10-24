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
	return spellPrefix(rawConfiguredPrefix(sessionDir))
}

// rawConfiguredPrefix is the same prefix in herdr's own spelling
// (`ctrl+space`), which is what the bare configuration writes back so a
// bare client answers to the operator's prefix (§8.9).
func rawConfiguredPrefix(sessionDir string) string {
	candidates := []string{}
	if sessionDir != "" {
		candidates = append(candidates, filepath.Join(sessionDir, "config.toml"))
	}
	// The operator's configuration directory, resolved the way herdr resolves
	// it. Not beside the ambient socket: HERDR_SOCKET_PATH may name any
	// socket, and inside an Olympus session it names Olympus's own.
	candidates = append(candidates, filepath.Join(operatorConfigDir(), "config.toml"))
	for _, path := range candidates {
		if v, ok := prefixInConfig(path); ok {
			return v
		}
	}
	return defaultPrefix
}

// operatorConfigDir is herdr's configuration directory for this environment,
// resolved as herdr resolves it (src/config/io.rs config_dir): XDG_CONFIG_HOME
// when it is set at all, an empty value included, then HOME the same way, then
// the temp directory. An empty value is a directory to herdr, the relative
// one it joins onto, so it is one here too.
func operatorConfigDir() string {
	if v, ok := os.LookupEnv("XDG_CONFIG_HOME"); ok {
		return filepath.Join(v, "herdr")
	}
	if home, ok := os.LookupEnv("HOME"); ok {
		return filepath.Join(home, ".config", "herdr")
	}
	return filepath.Join(os.TempDir(), "herdr")
}

// Prefix reports the prefix of the server this handle addresses: the one a
// session client of this handle reads (behavior §13.3, §8.10).
func (h *Herdr) Prefix(context.Context) (string, error) {
	return spellPrefix(h.rawPrefix()), nil
}

// rawPrefix is the prefix a session client of this handle reads, in herdr's
// spelling. A client onto a server this handle started is pointed at this
// backend's own configuration (clientEnv), so it reads the prefix that names,
// else herdr's default, and never the operator's. Any other is the
// config.toml beside the socket where there is one (a named session's
// directory), else the operator's.
func (h *Herdr) rawPrefix() string {
	if h.startedTheServer() && !h.socketOnly {
		if v, ok := prefixInConfig(h.managedConfigPath()); ok {
			return v
		}
		return defaultPrefix
	}
	return rawConfiguredPrefix(filepath.Dir(h.socketPath))
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
		// A quoted value ends at its closing quote, so a "#" inside it is the
		// key rather than a comment.
		if value != "" && (value[0] == '"' || value[0] == '\'') {
			if end := strings.IndexByte(value[1:], value[0]); end >= 0 {
				return value[1 : end+1], true
			}
		}
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
