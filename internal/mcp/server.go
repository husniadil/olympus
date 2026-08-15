// Package mcp is the MCP door: a stdio server on the official Go SDK.
//
// Protocol framing is never hand-rolled. The SDK is one of the three budgeted
// dependencies precisely so this door tracks the specification by upgrading a
// pin rather than by editing wire code (behavior §15.1).
//
// The door is dual-era by construction. A modern client negotiates 2026-07-28
// through per-request metadata; a legacy client's initialize handshake is
// answered at 2025-11-25. That cap is correct rather than a limitation: the
// modern revision deprecates initialize itself, so an initialize request IS the
// client selecting legacy semantics, and a dual-era server picks its era from
// how the client opens (behavior §15.3).
package mcp

import (
	"context"
	"fmt"
	"os"
	"strings"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/husniadil/olympus"
	"github.com/husniadil/olympus/backend"
)

// instructions are what a stateless client learns about this server.
//
// With no handshake, discover's instructions are the ONLY description a modern
// client receives, so leaving them empty removes the only thing telling a
// client what this server is for (behavior §15.6).
const instructions = `Olympus drives real terminal sessions on top of a terminal multiplexer.

Sessions are backend-scoped: a session created on one backend is invisible from
the other, and the backend that answered is reported by the doctor tool.

Placing text in a session and submitting it are separate operations. type_text
never submits; send_text confirms the text landed and then submits it.

run_command waits for a command and reports its own exit code, which is a
result rather than a failure. start_run returns an id to poll instead; nothing
is written down, so poll_run answers from the session's own scrollback and an
id that was never issued is indistinguishable from a command still running.`

// Serve runs the MCP server on stdio until the client disconnects.
//
// stdio only: there is no HTTP server and no daemon. Diagnostics go to stderr,
// which the stdio transport leaves alone — and never through MCP log
// notifications, which are deprecated (behavior §15.5).
func Serve(ctx context.Context) error {
	server := NewServer()
	return server.Run(ctx, &sdk.StdioTransport{})
}

// NewServer builds the server with every tool registered.
func NewServer() *sdk.Server {
	server := sdk.NewServer(&sdk.Implementation{
		Name:    "olympus",
		Title:   "Olympus",
		Version: olympus.Version,
	}, &sdk.ServerOptions{
		Instructions: instructions,
		// Explicitly empty, not left nil. The SDK advertises {"logging":{}} by
		// default for historical reasons, and logging is deprecated as of
		// 2026-07-28 (SEP-2577). Deprecated features remain functional during
		// a long window, which is exactly what makes them a trap: they work
		// today and are dead ends, so a client must not be told this server
		// offers one (behavior §15.5).
		Capabilities: &sdk.ServerCapabilities{},
	})
	register(server)
	return server
}

// open builds a handle for ONE request.
//
// Nothing is cached across calls: no backend handle kept per session, no state
// keyed by connection, nothing assuming a prior call happened. That is the
// modern era's request model and Olympus's own statelessness agreeing, and the
// agreement is load-bearing — session-scoped state to make a tool feel more
// convenient would break the transport model and the run contract at once
// (behavior §15.4).
//
// Configuration comes from the process environment, since a stateless request
// carries none (api §4).
func open() (*olympus.Olympus, error) {
	var opts []olympus.Option
	if v := strings.TrimSpace(os.Getenv("OLYMPUS_SOCKET")); v != "" {
		opts = append(opts, olympus.WithSocket(v))
	}
	if v := strings.TrimSpace(os.Getenv("ZMX_DIR")); v != "" {
		opts = append(opts, olympus.WithZmxDir(v))
	}
	return olympus.Open(opts...)
}

// A Result is what every tool returns alongside its payload.
type Result[T any] struct {
	// Backend is the RESOLVED backend, present on every result because
	// sessions are backend-scoped and a client must be able to tell which one
	// answered (behavior §0.4).
	Backend backend.Name `json:"backend"`
	Data    T            `json:"data"`
	// Warnings carries degraded-operation disclosure. Structured doors have no
	// stderr to narrate on, so it rides on the result (behavior §0.8).
	Warnings []olympus.Warning `json:"warnings,omitempty"`
}

// handler is one tool's work, written against a per-request handle.
type handler[In, Out any] func(context.Context, *olympus.Olympus, In) (Out, []olympus.Warning, error)

// addTool registers a tool with typed parameters and results, so the SDK
// generates the JSON schemas and populates structured content rather than the
// door hand-marshalling them (behavior §15.6).
func addTool[In, Out any](s *sdk.Server, name, description string, fn handler[In, Out]) {
	sdk.AddTool(s, &sdk.Tool{Name: name, Description: description},
		func(ctx context.Context, _ *sdk.CallToolRequest, in In) (*sdk.CallToolResult, Result[Out], error) {
			ol, err := open()
			if err != nil {
				return toolError(err), Result[Out]{}, nil
			}
			defer ol.Close()

			out, warnings, err := fn(ctx, ol, in)
			if err != nil {
				return toolErrorFrom(ol, err), Result[Out]{}, nil
			}
			return nil, Result[Out]{Backend: ol.Backend(), Data: out, Warnings: warnings}, nil
		})
}

// toolError turns an operation failure into a TOOL error carrying the §12 code.
//
// Never a JSON-RPC protocol error, and never a Go error returned to the SDK,
// which would become one. Protocol errors are reserved for protocol problems;
// conflating the two makes a session that was perfectly fine look broken, and
// leaves the client unable to tell "your target does not exist" from "this
// server is malfunctioning" (behavior §15.6).
func toolError(err error) *sdk.CallToolResult {
	return &sdk.CallToolResult{
		IsError: true,
		Content: []sdk.Content{
			&sdk.TextContent{Text: fmt.Sprintf("%s: %s", backend.CodeOf(err), err.Error())},
		},
	}
}

func toolErrorFrom(ol *olympus.Olympus, err error) *sdk.CallToolResult {
	result := toolError(err)
	result.Content = append(result.Content,
		&sdk.TextContent{Text: fmt.Sprintf("backend: %s", ol.Backend())})
	return result
}
