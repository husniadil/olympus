package olympus

import "time"

// The default values of behavior §17.3.
//
// ONE place decides these. A door that invents its own has created a second
// contract, and the two will drift — so the CLI, the MCP server and the library
// all read them from here rather than each having an opinion.
//
// Two are per-attempt rather than total, and the distinction is not cosmetic:
// the verified-send budget is spent TWICE (§7.4), and the graceful-kill timeout
// bounds only the poll phase, so total wall time there is presses*gap + timeout
// (§2.8).
const (
	// DefaultCols and DefaultRows size a session that does not ask.
	DefaultCols = 80
	DefaultRows = 24

	// DefaultRunTimeout bounds a synchronous run.
	DefaultRunTimeout = 60 * time.Second
	// DefaultRunPoll is how often a run checks for its completion marker.
	DefaultRunPoll = 250 * time.Millisecond

	// DefaultWaitTimeout and DefaultWaitPoll bound waiting for a pattern.
	DefaultWaitTimeout = 30 * time.Second
	DefaultWaitPoll    = 250 * time.Millisecond

	// DefaultVerifyBudget is ONE attempt's window; a verified send spends it
	// twice before failing.
	DefaultVerifyBudget = 5 * time.Second
	DefaultVerifyPoll   = 100 * time.Millisecond

	// DefaultLockWait is how long a writer waits for a contended session
	// before reporting a conflict.
	DefaultLockWait = 10 * time.Second

	// LockWaitEnv overrides DefaultLockWait, read at call time.
	LockWaitEnv = "OLYMPUS_LOCK_WAIT"
)
