package herdr

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/husniadil/olympus/backend"
)

// envValue returns the value of the LAST occurrence of key in env, matching how
// exec resolves a duplicated variable (later wins).
func envValue(env []string, key string) (string, bool) {
	val, ok := "", false
	for _, kv := range env {
		if strings.HasPrefix(kv, key+"=") {
			val, ok = kv[len(key)+1:], true
		}
	}
	return val, ok
}

// A session-client attach runs `herdr session attach <name>` and does NOT touch
// a server: it neither resolves a pane nor reads Olympus's socket, so the target
// is the herdr session name carried through unchanged. Asserted against a socket
// no server could answer on.
func TestSessionClientAttachBuildsSessionAttachWithoutTouchingAServer(t *testing.T) {
	t.Parallel()
	b := New(WithSocketPath(filepath.Join(shortDir(t), "h.sock")))

	att, err := b.Attach(context.Background(), "my-session", backend.AttachSpec{
		Role:          backend.RoleController,
		Supersede:     true,
		SessionClient: true,
	})
	if err != nil {
		t.Fatalf("session-client Attach: %v", err)
	}
	if got := att.Cmd.Args; len(got) != 4 || got[1] != "session" || got[2] != "attach" || got[3] != "my-session" {
		t.Fatalf("cmd args = %v, want [herdr session attach my-session]", got)
	}
	if att.Cleanup != nil {
		t.Error("a non-bare session attach left a cleanup to run, but writes no temp file")
	}
	// The ambient HERDR_* identity is stripped so the client does not detect it
	// is launched from inside a pane, and Olympus's own configuration directory
	// is NOT imposed (the operator's real sessions live in theirs).
	for _, key := range []string{"HERDR_SOCKET_PATH", "HERDR_PANE_ID", "HERDR_TAB_ID", "HERDR_WORKSPACE_ID", "HERDR_SESSION", "HERDR_CLIENT_SOCKET_PATH"} {
		if _, ok := envValue(att.Cmd.Env, key); ok {
			t.Errorf("session attach env carries %s; it must be stripped", key)
		}
	}
	if v, ok := envValue(att.Cmd.Env, "XDG_CONFIG_HOME"); ok && strings.Contains(v, b.StateHome()) {
		t.Errorf("session attach imposes Olympus's own configuration directory: %s", v)
	}
	if _, ok := envValue(att.Cmd.Env, "HERDR_CONFIG_PATH"); ok {
		t.Error("a non-bare session attach set HERDR_CONFIG_PATH; only --bare should")
	}
}

// A bare session-client attach writes the stripped config to a temp file, points
// HERDR_CONFIG_PATH at it, and reaps it on Close.
func TestBareSessionClientAttachWritesStrippedConfig(t *testing.T) {
	t.Parallel()
	b := New(WithSocketPath(filepath.Join(shortDir(t), "h.sock")))

	att, err := b.Attach(context.Background(), "my-session", backend.AttachSpec{
		Role:          backend.RoleController,
		Supersede:     true,
		SessionClient: true,
		Bare:          true,
	})
	if err != nil {
		t.Fatalf("bare session-client Attach: %v", err)
	}
	path, ok := envValue(att.Cmd.Env, "HERDR_CONFIG_PATH")
	if !ok {
		t.Fatal("a bare session attach did not set HERDR_CONFIG_PATH")
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading the stripped config at %s: %v", path, err)
	}
	if string(content) != bareSessionConfig {
		t.Errorf("the written config does not match bareSessionConfig")
	}
	if att.Cleanup == nil {
		t.Fatal("a bare session attach left no cleanup to reap the temp config")
	}
	if err := att.Close(); err != nil {
		t.Errorf("closing the bare attach: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("the temp config survived Close: stat err = %v", err)
	}
}

// herdr's session client has no co-attach control, so an explicit opt-out of
// supersession is reported rather than silently dropped.
func TestSessionClientAttachReportsUnhonoredKeepOthers(t *testing.T) {
	t.Parallel()
	b := New(WithSocketPath(filepath.Join(shortDir(t), "h.sock")))

	att, err := b.Attach(context.Background(), "my-session", backend.AttachSpec{
		Role:          backend.RoleController,
		Supersede:     false,
		SessionClient: true,
	})
	if err != nil {
		t.Fatalf("session-client Attach: %v", err)
	}
	if len(att.Notices) == 0 {
		t.Fatal("--keep-others on a session attach produced no notice")
	}
	joined := strings.Join(att.Notices, " ")
	if !strings.Contains(joined, "keep-others") {
		t.Errorf("notice does not mention the unhonored option: %q", joined)
	}
}

// The stripped config is what herdr accepts: `herdr config check` reports it ok.
// Guarded on the binary being installed, so a host without herdr still passes.
func TestBareSessionConfigValidatesAgainstHerdr(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("herdr"); err != nil {
		t.Skip("herdr not installed")
	}
	dir := shortDir(t)
	path := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(path, []byte(bareSessionConfig), 0o600); err != nil {
		t.Fatalf("writing config: %v", err)
	}
	cmd := exec.Command("herdr", "config", "check")
	cmd.Env = append(os.Environ(), "HERDR_CONFIG_PATH="+path)
	out, _ := cmd.CombinedOutput()
	if !strings.Contains(string(out), "config: ok") {
		t.Errorf("herdr config check did not accept the stripped config:\n%s", out)
	}
}
