// Package engine holds the backend-agnostic logic that sits above the Backend
// interface: the sentinel run protocol, verified delivery, the per-session
// write lock, idempotent ensure, graceful kill, and exit-marker inspection.
//
// Nothing here talks to a multiplexer directly. Each engine is written against
// the interface or against injected operations, so the rules can be tested
// without a backend and behave identically on every backend.
package engine

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"strings"
	"sync/atomic"

	"github.com/husniadil/olympus/backend"
)

// Reserved sentinel shapes (behavior §17.1). They land in the session's own
// visible output, so they are as much a shared namespace as a session name is.
const (
	startPrefix = "OLY_S_"
	donePrefix  = "OLY_D_"
)

// maxExitDigits is the digit run a completion marker may carry. A longer run is
// not a marker at all rather than a marker to truncate.
const maxExitDigits = 3

var idCounter atomic.Uint64

// NewID returns an identifier unique across processes, goroutines and time
// (behavior §6.1).
//
// All three parts are load-bearing: the pid separates processes, the counter
// separates goroutines within one, and the random bytes separate a reused pid
// from a previous process's markers still sitting in the same scrollback.
func NewID() string {
	var random [4]byte
	if _, err := rand.Read(random[:]); err != nil {
		// A failure here would only weaken uniqueness across pid reuse; the
		// pid and counter still separate everything live.
		random = [4]byte{}
	}
	return fmt.Sprintf("%d%d%s", os.Getpid(), idCounter.Add(1), hex.EncodeToString(random[:]))
}

// Markers are one run's sentinel pair.
type Markers struct {
	id    string
	start string
	done  string
}

// NewMarkers builds the sentinel pair for a run id.
func NewMarkers(id string) Markers {
	return Markers{
		id:    id,
		start: startPrefix + id,
		done:  donePrefix + id + "_",
	}
}

// ID reports the run identifier, which is the whole of a detached run's state:
// nothing durable is written, so a caller resumes solely by re-presenting it
// (behavior §6.7).
func (m Markers) ID() string { return m.id }

// Line is the command line to inject.
//
// Both markers are echoed onto the pane before the shell runs anything, and
// quoting cannot prevent that — quoting controls shell parsing, not terminal
// rendering. What separates the echo from the real completion is expansion: the
// echoed line shows a literal, unexpanded $?, while the real marker is followed
// by actual digits (behavior §6.1).
func (m Markers) Line(command string) string {
	return fmt.Sprintf(`echo %s; %s; echo "%s$?_"`, m.start, command, m.done)
}

// A Result is a completed run.
type Result struct {
	Output   string
	ExitCode int
}

// Parse looks for this run's completion in a capture.
//
// Both markers are required. A window that caught the completion but scrolled
// past the start parses as not-found rather than as a truncated match, so a
// too-small window is deliberately indistinguishable from "still running" and
// the caller keeps polling until its own timeout (behavior §6.2).
func (m Markers) Parse(capture string) (Result, bool) {
	// Newlines are stripped exactly once into a search copy with a parallel
	// map back to raw offsets. A marker wrapped by the pane's width comes back
	// split by a newline that cannot be told from a real one, so matching has
	// to happen on text where that split is gone — while the OUTPUT still has
	// to be sliced out of the raw capture, newlines and all.
	stripped, offsets := stripNewlines(capture)

	doneAt, exitCode, ok := lastCompletion(stripped, m.done)
	if !ok {
		return Result{}, false
	}

	// The last start marker strictly BEFORE the completion. The command line's
	// echo of the start marker appears before the real one's own output, so
	// "last before done" selects the right one — and a later run's echo, which
	// sits after this completion, is correctly ignored.
	startAt := strings.LastIndex(stripped[:doneAt], m.start)
	if startAt < 0 {
		return Result{}, false
	}

	from := rawOffset(offsets, startAt+len(m.start), len(capture))
	to := rawOffset(offsets, doneAt, len(capture))
	if from > to {
		return Result{}, false
	}
	return Result{Output: trimOneNewline(capture[from:to]), ExitCode: exitCode}, true
}

// lastCompletion finds the last done marker followed by 1-3 digits and the
// closing delimiter, returning where the marker starts and what it carried.
//
// The closing delimiter is not decoration. Without it, a digit opening the NEXT
// captured line — a prompt like "12:34 $" — joins the digit run once newlines
// are stripped, and exit 0 is read as exit 12.
func lastCompletion(stripped, marker string) (int, int, bool) {
	search := stripped
	for {
		at := strings.LastIndex(search, marker)
		if at < 0 {
			return 0, 0, false
		}
		if code, ok := parseExit(stripped[at+len(marker):]); ok {
			return at, code, true
		}
		// Not a real completion — the echoed line's unexpanded $? lands here.
		// Keep looking further back.
		search = search[:at]
	}
}

func parseExit(rest string) (int, bool) {
	digits := 0
	for digits < len(rest) && digits < maxExitDigits && rest[digits] >= '0' && rest[digits] <= '9' {
		digits++
	}
	if digits == 0 || digits >= len(rest) || rest[digits] != '_' {
		return 0, false
	}
	code := 0
	for i := 0; i < digits; i++ {
		code = code*10 + int(rest[i]-'0')
	}
	return code, true
}

// stripNewlines returns the capture with newlines removed, plus a map from each
// surviving byte's index to its offset in the original.
func stripNewlines(raw string) (string, []int) {
	var b strings.Builder
	b.Grow(len(raw))
	offsets := make([]int, 0, len(raw))
	for i := 0; i < len(raw); i++ {
		if raw[i] == '\n' {
			continue
		}
		b.WriteByte(raw[i])
		offsets = append(offsets, i)
	}
	return b.String(), offsets
}

func rawOffset(offsets []int, strippedIndex, rawLen int) int {
	if strippedIndex >= len(offsets) {
		return rawLen
	}
	return offsets[strippedIndex]
}

// trimOneNewline removes exactly one leading and one trailing newline — the
// ones the protocol's own echoes contribute — without touching blank lines the
// command genuinely produced.
func trimOneNewline(s string) string {
	s = strings.TrimPrefix(s, "\n")
	s = strings.TrimSuffix(s, "\n")
	return s
}

// ValidateCommand rejects the two commands that would degrade silently
// (behavior §6.3).
//
// Neither degradation is a timeout, which is why an explicit check is needed. A
// newline makes the shell run the fragments as separate commands: both markers
// still echo, the run SUCCEEDS, and it reports the exit code of the last
// fragment. An empty command is shell-dependent — one shell hard-errors into a
// genuine timeout, another tolerates it and reports success with exit 0.
//
// Rejecting up front also means no partial pane interaction happens either way.
func ValidateCommand(command string) error {
	if strings.TrimSpace(command) == "" {
		return backend.Errorf(backend.CodeUsage, "the command is empty")
	}
	if strings.ContainsAny(command, "\n\r") {
		return backend.Errorf(backend.CodeUsage,
			"the command contains a line break, which would run as separate commands and report only the last one's exit code")
	}
	return nil
}
