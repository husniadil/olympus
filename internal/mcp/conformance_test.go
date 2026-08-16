package mcp_test

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"os/exec"
	"slices"
	"strings"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/husniadil/olympus"
	olympusmcp "github.com/husniadil/olympus/internal/mcp"
)

// The six assertions of behavior §15.7.
//
// They are made against the RAW wire rather than through the SDK's client,
// because what is under test is what this server puts on the wire — including
// the two eras it serves. A client that negotiates for us would hide exactly
// the thing being checked.

const (
	modernVersion = "2026-07-28"
	legacyVersion = "2025-11-25"
	// The JSON-RPC code for an unsupported protocol version.
	codeUnsupportedProtocolVersion = -32022

	// A modern request carries its whole identity in _meta, because there is
	// no handshake to have carried it earlier. All three keys are required:
	// omitting the capabilities is rejected as an invalid request, not
	// defaulted.
	metaProtocolVersion    = "io.modelcontextprotocol/protocolVersion"
	metaClientCapabilities = "io.modelcontextprotocol/clientCapabilities"
	metaClientInfo         = "io.modelcontextprotocol/clientInfo"
)

// modernMeta is what every modern-era request declares about itself.
func modernMeta(version string) map[string]any {
	return map[string]any{
		metaProtocolVersion:    version,
		metaClientCapabilities: map[string]any{},
		metaClientInfo:         map[string]any{"name": "conformance", "version": "1.0"},
	}
}

type wire struct {
	t       *testing.T
	toServe io.WriteCloser
	replies *bufio.Reader
	nextID  int
}

// newWire starts the server on a pipe and returns a raw JSON-RPC client.
func newWire(t *testing.T) *wire {
	t.Helper()

	clientReader, serverWriter := io.Pipe()
	serverReader, clientWriter := io.Pipe()

	server := olympusmcp.NewServer()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = server.Run(ctx, &sdk.IOTransport{Reader: serverReader, Writer: serverWriter})
	}()

	t.Cleanup(func() {
		cancel()
		_ = clientWriter.Close()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
		}
	})

	return &wire{t: t, toServe: clientWriter, replies: bufio.NewReader(clientReader), nextID: 1}
}

// call sends one request and returns the decoded response.
func (w *wire) call(method string, params any) map[string]any {
	w.t.Helper()

	id := w.nextID
	w.nextID++
	request := map[string]any{"jsonrpc": "2.0", "id": id, "method": method}
	if params != nil {
		request["params"] = params
	}
	encoded, err := json.Marshal(request)
	if err != nil {
		w.t.Fatalf("encoding %s: %v", method, err)
	}
	if _, err := w.toServe.Write(append(encoded, '\n')); err != nil {
		w.t.Fatalf("sending %s: %v", method, err)
	}

	for {
		line, err := w.replies.ReadBytes('\n')
		if err != nil {
			w.t.Fatalf("reading the reply to %s: %v", method, err)
		}
		var response map[string]any
		if err := json.Unmarshal(line, &response); err != nil {
			w.t.Fatalf("decoding the reply to %s: %v\nline was: %s", method, err, line)
		}
		// Notifications carry no id and are not this call's answer.
		if response["id"] == nil {
			continue
		}
		return response
	}
}

func resultOf(t *testing.T, response map[string]any) map[string]any {
	t.Helper()
	if e, ok := response["error"]; ok {
		t.Fatalf("the server returned an error: %v", e)
	}
	result, ok := response["result"].(map[string]any)
	if !ok {
		t.Fatalf("the response has no result object: %v", response)
	}
	return result
}

// 1. server/discover advertises the modern revision.
func TestDiscoverAdvertisesTheModernRevision(t *testing.T) {
	w := newWire(t)
	// discover is only SERVED to a request that itself declares the modern
	// protocol. It is registered unconditionally, but a pre-modern request
	// gets method-not-found — which is what lets a client probe an older
	// server and learn it is legacy.
	result := resultOf(t, w.call("server/discover", map[string]any{"_meta": modernMeta(modernVersion)}))

	versions, _ := result["supportedVersions"].([]any)
	var got []string
	for _, v := range versions {
		got = append(got, v.(string))
	}
	if !slices.Contains(got, modernVersion) {
		t.Errorf("supportedVersions is %v, which does not include %s", got, modernVersion)
	}

	// With no handshake, discover's instructions are the ONLY description a
	// modern client receives, so an empty one removes the only thing telling a
	// client what this server is for (§15.6).
	instructions, _ := result["instructions"].(string)
	if strings.TrimSpace(instructions) == "" {
		t.Error("the server advertises no instructions, so a stateless client learns nothing about it")
	}
}

// 2. A modern-era request — per-request metadata, no handshake — completes a
// real tool call end to end.
func TestAModernRequestCompletesAToolCallWithoutAHandshake(t *testing.T) {
	w := newWire(t)

	response := w.call("tools/call", map[string]any{
		"name":      "version",
		"arguments": map[string]any{},
		"_meta":     modernMeta(modernVersion),
	})
	result := resultOf(t, response)

	structured, ok := result["structuredContent"].(map[string]any)
	if !ok {
		t.Fatalf("the tool returned no structured content: %v", result)
	}
	data, ok := structured["data"].(map[string]any)
	if !ok {
		t.Fatalf("the result carries no data: %v", structured)
	}
	if data["version"] != olympus.Version {
		t.Errorf("version tool reported %v, want %q — the tool and the server identity must agree", data["version"], olympus.Version)
	}
}

// 3. A legacy initialize still negotiates, and serves the same tools.
//
// Not optional politeness: most deployed clients are still legacy, and a change
// that quietly broke them would pass a modern-only suite.
func TestALegacyHandshakeStillNegotiatesAndServesTheSameTools(t *testing.T) {
	w := newWire(t)

	result := resultOf(t, w.call("initialize", map[string]any{
		"protocolVersion": legacyVersion,
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "legacy-client", "version": "1.0"},
	}))

	// The legacy path is capped at the last revision before initialize was
	// deprecated. That cap is correct: an initialize request IS the client
	// selecting legacy semantics (§15.3).
	if result["protocolVersion"] != legacyVersion {
		t.Errorf("a legacy handshake negotiated %v, want %s", result["protocolVersion"], legacyVersion)
	}

	listed := resultOf(t, w.call("tools/list", map[string]any{}))
	if got := toolNames(t, listed); !slices.Equal(got, sortedToolNames()) {
		t.Errorf("the legacy era serves a different tool set:\n got %v\nwant %v", got, sortedToolNames())
	}
}

// 4. An unknown requested version is answered with the specified code, carrying
// the supported list so the client can retry with a mutually supported one.
func TestAnUnknownProtocolVersionIsRejectedWithTheSupportedList(t *testing.T) {
	w := newWire(t)

	// A version WITHIN the modern era but unknown. The distinction matters: a
	// version string below 2026-07-28 is not a malformed modern request at
	// all, it is a legacy-era request, and it is handled by the legacy gate
	// rather than by this rejection.
	const unknownModern = "2099-01-01"

	response := w.call("tools/call", map[string]any{
		"name":      "version",
		"arguments": map[string]any{},
		"_meta":     modernMeta(unknownModern),
	})

	failure, ok := response["error"].(map[string]any)
	if !ok {
		t.Fatalf("an unknown protocol version was accepted: %v", response)
	}
	code, _ := failure["code"].(float64)
	if int(code) != codeUnsupportedProtocolVersion {
		t.Errorf("error code %v, want %d", failure["code"], codeUnsupportedProtocolVersion)
	}

	// The data has to name both sides, or the client cannot pick a version to
	// retry with.
	encoded, err := json.Marshal(failure["data"])
	if err != nil {
		t.Fatalf("encoding the error data: %v", err)
	}
	for _, want := range []string{modernVersion, unknownModern} {
		if !strings.Contains(string(encoded), want) {
			t.Errorf("the error data %s does not mention %q", encoded, want)
		}
	}
}

// 5. No advertised capability includes a deprecated feature.
//
// Roots, sampling and logging are deprecated as of the modern revision. They
// remain functional during a long deprecation window, which is exactly what
// makes them a trap: they work today and are dead ends.
func TestNoDeprecatedCapabilityIsAdvertised(t *testing.T) {
	w := newWire(t)
	result := resultOf(t, w.call("server/discover", map[string]any{"_meta": modernMeta(modernVersion)}))

	capabilities, _ := result["capabilities"].(map[string]any)
	for _, deprecated := range []string{"logging", "sampling", "roots"} {
		if _, present := capabilities[deprecated]; present {
			t.Errorf("the server advertises the deprecated %q capability", deprecated)
		}
	}
}

// 6. The registered tool list is pinned, so a tool cannot silently appear or
// vanish. Tool names are semver-bound: a client written against them breaks
// invisibly if one is renamed.
func TestTheToolSurfaceIsPinned(t *testing.T) {
	w := newWire(t)
	listed := resultOf(t, w.call("tools/list", map[string]any{"_meta": modernMeta(modernVersion)}))

	got := toolNames(t, listed)
	want := sortedToolNames()
	if !slices.Equal(got, want) {
		t.Errorf("the registered tools have changed:\n got %v\nwant %v", got, want)
	}
}

// Every tool must describe itself, or a client choosing between them is
// guessing from the name alone.
func TestEveryToolIsDescribed(t *testing.T) {
	w := newWire(t)
	listed := resultOf(t, w.call("tools/list", map[string]any{"_meta": modernMeta(modernVersion)}))

	tools, _ := listed["tools"].([]any)
	for _, entry := range tools {
		tool, _ := entry.(map[string]any)
		name, _ := tool["name"].(string)
		if strings.TrimSpace(tool["description"].(string)) == "" {
			t.Errorf("tool %q has no description", name)
		}
		if _, ok := tool["inputSchema"]; !ok {
			t.Errorf("tool %q has no input schema, so its parameters are undiscoverable", name)
		}
	}
}

// An operation failure is a TOOL error carrying the §12 code, never a JSON-RPC
// protocol error. Conflating them makes a session that was fine look broken,
// and leaves the client unable to tell a missing target from a broken server.
func TestAnOperationFailureIsAToolErrorNotAProtocolError(t *testing.T) {
	requireBackend(t)
	w := newWire(t)

	response := w.call("tools/call", map[string]any{
		"name":      "capture",
		"arguments": map[string]any{"targets": []string{"oly-never-existed"}},
		"_meta":     modernMeta(modernVersion),
	})

	if _, isProtocolError := response["error"]; isProtocolError {
		t.Fatalf("a missing session came back as a protocol error: %v", response["error"])
	}
	result := resultOf(t, response)
	if isError, _ := result["isError"].(bool); !isError {
		t.Fatalf("a missing session did not come back as a tool error: %v", result)
	}

	encoded, _ := json.Marshal(result["content"])
	// The §12 code has to survive to the client, or it cannot branch on the
	// failure any better than by reading prose.
	if !strings.Contains(string(encoded), "SESSION_NOT_FOUND") {
		t.Errorf("the tool error does not carry its code: %s", encoded)
	}
}

func toolNames(t *testing.T, listed map[string]any) []string {
	t.Helper()
	tools, ok := listed["tools"].([]any)
	if !ok {
		t.Fatalf("the listing has no tools array: %v", listed)
	}
	var names []string
	for _, entry := range tools {
		tool, _ := entry.(map[string]any)
		name, _ := tool["name"].(string)
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}

func sortedToolNames() []string {
	names := slices.Clone(olympusmcp.ToolNames)
	slices.Sort(names)
	return names
}

// §15.6 and api §5: three tools answer about Olympus and this process, not about
// a multiplexer — and must therefore answer when no multiplexer is installed.
//
// That is precisely when they are needed. `doctor` is the command sent to
// explain an environment with nothing in it; refusing because there is nothing
// in it is the same self-defeating shape as a diagnostic that hangs. `version`
// exists so a consumer can floor-check without shelling out, which it cannot do
// if the answer depends on an unrelated binary. And `self` outside a session is
// documented to answer `inside: false` — an answer a caller can act on, where an
// error leaves them guessing whether it meant nowhere or could-not-tell.
//
// The whole tool surface used to open a backend handle before dispatch, so all
// three failed with BACKEND_UNAVAILABLE on a machine with no multiplexer.
func TestTheBackendIndependentToolsAnswerWithNothingInstalled(t *testing.T) {
	// An empty directory as the entire PATH: no zmx, no tmux, no meja, without
	// uninstalling anything.
	t.Setenv("PATH", t.TempDir())
	w := newWire(t)

	for _, name := range []string{"version", "doctor", "self"} {
		result := resultOf(t, w.call("tools/call", map[string]any{
			"name": name, "arguments": map[string]any{}, "_meta": modernMeta(modernVersion)}))
		if isError, _ := result["isError"].(bool); isError {
			encoded, _ := json.Marshal(result["content"])
			t.Errorf("%s failed with no multiplexer installed: %s", name, encoded)
		}
	}
}

// The rest of the surface must still refuse, and refuse in the vocabulary. A
// blanket exemption would turn "nothing is installed" into a pile of confusing
// per-tool failures instead of one code the caller can branch on.
func TestTheBackendDependentToolsStillRefuseWithNothingInstalled(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	w := newWire(t)

	result := resultOf(t, w.call("tools/call", map[string]any{
		"name": "list_sessions", "arguments": map[string]any{}, "_meta": modernMeta(modernVersion)}))
	if isError, _ := result["isError"].(bool); !isError {
		t.Fatal("listing sessions succeeded with no multiplexer installed")
	}
	encoded, _ := json.Marshal(result["content"])
	if !strings.Contains(string(encoded), "BACKEND_UNAVAILABLE") {
		t.Errorf("the refusal does not carry its code: %s", encoded)
	}
}

// The exemption must not cost the envelope a field. `backend` is shipped and
// semver-bound (api §2), so a healthy machine keeps naming the resolved backend
// on these three results; only a machine where nothing resolves leaves it empty.
func TestAFreestandingToolStillNamesTheResolvedBackend(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is not installed")
	}
	t.Setenv("OLYMPUS_BACKEND", "tmux")
	w := newWire(t)

	result := resultOf(t, w.call("tools/call", map[string]any{
		"name": "version", "arguments": map[string]any{}, "_meta": modernMeta(modernVersion)}))
	structured, ok := result["structuredContent"].(map[string]any)
	if !ok {
		t.Fatalf("the tool returned no structured content: %v", result)
	}
	if structured["backend"] != "tmux" {
		t.Errorf("the envelope names backend %v, want tmux", structured["backend"])
	}
}

// requireBackend skips when no multiplexer is installed.
//
// Cases that assert a SESSION-level outcome need something to have sessions.
// Without this they fail with BACKEND_UNAVAILABLE, which is the CORRECT answer
// on such a machine — so the failure reports a working product as broken, and
// does it on exactly the machines least able to tell the difference.
func requireBackend(t *testing.T) {
	t.Helper()
	for _, name := range []string{"tmux", "zmx", "meja"} {
		if _, err := exec.LookPath(name); err == nil {
			return
		}
	}
	t.Skip("no terminal multiplexer is installed")
}
