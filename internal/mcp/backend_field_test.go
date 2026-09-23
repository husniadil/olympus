package mcp_test

import (
	"encoding/json"
	"os/exec"
	"strings"
	"testing"
)

func structuredOf(t *testing.T, w *wire, name string) (map[string]any, map[string]any) {
	t.Helper()
	result := resultOf(t, w.call("tools/call", map[string]any{
		"name": name, "arguments": map[string]any{}, "_meta": modernMeta(modernVersion)}))
	structured, _ := result["structuredContent"].(map[string]any)
	return result, structured
}

// version, list_kinds and self resolve no backend, so naming one would say the
// answer came from a backend it never asked. The CLI omits it for the same
// verbs; the two doors agree (api §2).
func TestToolsThatResolveNoBackendNameNone(t *testing.T) {
	w := newWire(t)
	for _, name := range []string{"version", "list_kinds", "self"} {
		_, structured := structuredOf(t, w, name)
		if _, present := structured["backend"]; present {
			t.Errorf("%s names a backend it never resolved: %v", name, structured["backend"])
		}
	}
}

// A handle that fails after its backend resolved still names that backend
// (api §2), as the CLI envelope does.
func TestAnOpenFailureAfterResolutionNamesTheBackend(t *testing.T) {
	if err := exec.Command("zmx", "version").Run(); err != nil {
		t.Skipf("zmx does not run here: %v", err)
	}
	t.Setenv("OLYMPUS_BACKEND", "zmx")
	t.Setenv("OLYMPUS_SOCKET", "not-for-zmx")
	w := newWire(t)
	result, structured := structuredOf(t, w, "list_sessions")
	if isError, _ := result["isError"].(bool); !isError {
		t.Fatalf("a tmux socket name on zmx was accepted: %v", result)
	}
	encoded, _ := json.Marshal(result["content"])
	if !strings.Contains(string(encoded), "USAGE") || !strings.Contains(string(encoded), "backend: zmx") {
		t.Errorf("the failure does not name its code and backend: %s", encoded)
	}
	if structured["backend"] != "zmx" {
		t.Errorf("structured backend is %v, want zmx", structured["backend"])
	}
}
