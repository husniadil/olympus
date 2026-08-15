// Package cli is the command-line door.
//
// It translates; it does not decide. A default, validation rule, or result
// field invented here is a second contract — defaults live in the ergonomic
// layer (behavior §17.3), and this package passes them through.
package cli

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/husniadil/olympus"
	"github.com/husniadil/olympus/backend"
)

// An Envelope is the structured output shape of api §2.
//
// One shape for every operation, success and failure alike. The alternative —
// bare per-operation payloads — cannot carry the two cross-cutting fields that
// must appear everywhere: the resolved backend and the degraded-operation
// warnings.
type Envelope struct {
	// OK is always present, and is the only field a consumer needs to branch
	// on.
	OK bool `json:"ok"`
	// Backend is the RESOLVED backend, never the requested one, and is present
	// on failure too — a failure is exactly when knowing which backend
	// answered matters most (behavior §0.4).
	Backend backend.Name `json:"backend,omitempty"`
	// Data is the per-operation payload, absent for operations with none.
	Data any `json:"data,omitempty"`
	// Warnings is omitted when empty, never null.
	Warnings []olympus.Warning `json:"warnings,omitempty"`
	// Error is present exactly when OK is false.
	Error *EnvelopeError `json:"error,omitempty"`
}

// An EnvelopeError carries a code from the §12 vocabulary.
type EnvelopeError struct {
	Code    backend.Code `json:"code"`
	Message string       `json:"message"`
}

func successEnvelope(name backend.Name, data any, warnings []olympus.Warning) Envelope {
	return Envelope{OK: true, Backend: name, Data: data, Warnings: warnings}
}

func failureEnvelope(name backend.Name, err error) Envelope {
	return Envelope{
		OK:      false,
		Backend: name,
		Error:   &EnvelopeError{Code: backend.CodeOf(err), Message: err.Error()},
	}
}

// writeJSON emits an envelope on the data channel.
//
// Streams are separate and nothing crosses: stdout carries the payload, stderr
// carries narration. A consumer piping stdout into a parser must never have to
// filter it (api §2.3).
func writeJSON(w io.Writer, envelope Envelope) error {
	encoder := json.NewEncoder(w)
	encoder.SetEscapeHTML(false)
	return encoder.Encode(envelope)
}

// writeWarnings emits degraded-operation disclosure on the narration channel.
//
// Announced once per operation, never once per row: a warning per listed pane
// is noise that trains users to ignore the mechanism (behavior §0.8).
func writeWarnings(w io.Writer, warnings []olympus.Warning) {
	for _, warning := range warnings {
		fmt.Fprintf(w, "warning: %s\n", warning.Message)
	}
}
