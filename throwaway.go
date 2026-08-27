package olympus

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"

	"github.com/husniadil/olympus/backend"
	"github.com/husniadil/olympus/internal/engine"
)

// throwawayPrefix is reserved (behavior §17.1).
const throwawayPrefix = "olympus-run-"

// throwawayName builds the reserved name for a run's own session.
func throwawayName() string {
	var nonce [4]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		nonce = [4]byte{}
	}
	return fmt.Sprintf("%s%d-%s", throwawayPrefix, os.Getpid(), hex.EncodeToString(nonce[:]))
}

// RunOnce runs a command in a session created for it and killed afterwards
// (behavior §6.10).
//
// The session is killed on success, failure and timeout alike — a throwaway
// that only gets cleaned up on the happy path is a leak with extra steps.
//
// A cleanup failure MUST NOT override the run's own result. It comes back as a
// warning instead: a session that failed to clean up is a leak to notice
// separately, not a reason to hide the answer the caller actually asked for.
//
// Two different things are configurable here and the signature names both
// rather than making a caller guess which one a single variadic shapes: run
// bounds the run itself — the timeout above all — and session shapes the
// session created to hold it. A throwaway that silently dropped the run's own
// timeout would run every command on the default budget while reporting the
// caller's.
func (o *Olympus) RunOnce(ctx context.Context, command string, run []RunOption, session ...SessionOption) (Result, []Warning, error) {
	// Validated before anything is created, so a bad command does not leave a
	// session behind to clean up (behavior §6.3).
	if err := engine.ValidateCommand(command); err != nil {
		return Result{}, nil, err
	}

	name := throwawayName()
	spec := backend.CreateSpec{Name: name, Cols: DefaultCols, Rows: DefaultRows}
	for _, opt := range session {
		opt(&spec)
	}
	// It gets the default shell: the sentinel protocol is shell syntax, so a
	// throwaway spawned onto some other argv could never run the command it
	// exists for (behavior §6.5).
	spec.Command = nil

	if _, err := o.backend.Create(ctx, spec); err != nil {
		return Result{}, nil, err
	}

	handle := &Session{ol: o, name: name}
	result, runErr := handle.Exec(ctx, command, run...)

	var warnings []Warning
	// context.WithoutCancel so a cancelled or timed-out run still cleans up:
	// the timeout is exactly when a throwaway is most likely to be left behind.
	if err := o.backend.Kill(context.WithoutCancel(ctx), name); err != nil {
		warnings = append(warnings, Warning{
			Code:    WarningDegraded,
			Message: "the throwaway session " + name + " could not be cleaned up: " + err.Error(),
		})
	}

	if runErr != nil {
		return Result{}, warnings, runErr
	}
	return result, warnings, nil
}

// engineWithLock runs fn holding a session's write lock, for callers in this
// package that are not going through a Session handle.
func engineWithLock(ctx context.Context, o *Olympus, name string, fn func() error) error {
	return engine.WithLock(ctx, o.locks, o.lockKey(name), o.lockWait, fn)
}
