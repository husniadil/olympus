package engine

import (
	"strconv"
	"strings"

	"github.com/husniadil/olympus/backend"
)

// ExitMarker scans a capture for a caller-supplied completion marker and
// reports the exit code it carries (behavior §14).
//
// The marker format is caller-supplied, always, and there is deliberately NO
// default. A fixed default invites collision with ordinary program output or
// stale scrollback, and weakens the caller-controlled uniqueness the whole
// design assumes.
//
// This answers a CONTENT question — what marker, if any, is on screen — which
// is distinct from a detached run's desired-state question of whether an
// injected line finished. The two read different evidence and neither
// substitutes for the other.
//
// A marker that is present but malformed reports not-found. That is a false
// negative in the safe direction: a consumer whose reaper treats a missing
// marker as "still running" waits rather than finalizing something that may
// still be alive.
func ExitMarker(capture, marker string) (int, bool, error) {
	if marker == "" {
		return 0, false, backend.Errorf(backend.CodeUsage,
			"an exit marker must be supplied; there is deliberately no default, since a fixed one would collide with ordinary output")
	}

	code, found := 0, false
	for _, line := range strings.Split(capture, "\n") {
		// Line-anchored. A mid-line occurrence is ordinary output that happens
		// to mention the marker — an echoed command, a log line quoting it —
		// and matching it would report a completion that never happened.
		if !strings.HasPrefix(line, marker) {
			continue
		}
		rest := strings.TrimPrefix(line, marker)

		// The LEADING whitespace-delimited token, never the whole remainder.
		// After a full-screen process exits, the wrapper's echo lands on a
		// rendered row still carrying leftover screen content to its right,
		// because the exiting program never cleared to end of line:
		//
		//     TASK_COMPLETED:0 Esc to cancel
		//
		// Requiring the entire remainder to parse would classify every such
		// legitimate exit as malformed, so the code would stay unset forever
		// and any reaper waiting on it would silently never fire.
		token := rest
		if at := strings.IndexAny(token, " \t"); at >= 0 {
			token = token[:at]
		}

		// The token itself stays strict: trailing junk inside it is malformed,
		// not something to salvage a prefix from.
		parsed, err := strconv.Atoi(token)
		if err != nil {
			continue
		}
		// The last line-anchored occurrence wins: scrollback holds every run
		// the session has done, and the newest one is the answer.
		code, found = parsed, true
	}
	return code, found, nil
}
