package backend

import (
	"errors"
	"fmt"
)

// The sentinels a caller matches with errors.Is (api §3). They carry no
// message of their own; they exist to be compared against.
//
// There is deliberately no sentinel for CodeUnexpected. It is the catch-all —
// every error that carries no other code answers to it — so matching against
// it would say nothing a failed match against the other six does not already
// say. Read CodeOf when the classification itself is the question.
var (
	ErrUsage       = errors.New("usage")
	ErrNotFound    = errors.New("not found")
	ErrUnavailable = errors.New("backend unavailable")
	ErrTimeout     = errors.New("timed out")
	ErrConflict    = errors.New("conflict")
	ErrUnsupported = errors.New("unsupported")
)

// sentinels maps a code to the value errors.Is matches it against.
var sentinels = map[Code]error{
	CodeUsage:              ErrUsage,
	CodeSessionNotFound:    ErrNotFound,
	CodeBackendUnavailable: ErrUnavailable,
	CodeTimeout:            ErrTimeout,
	CodeConflict:           ErrConflict,
	CodeUnsupported:        ErrUnsupported,
}

// An Error carries a classification alongside its message, so a door can
// translate any failure into the structured envelope and an exit status
// without knowing which layer produced it.
type Error struct {
	// Code classifies the failure. It is the semver-bound vocabulary of §12.
	Code Code
	// Msg describes this failure in the caller's terms.
	Msg string
	// Cause is the underlying error, if any. It stays unwrappable so a backend
	// can attach the exec or syscall failure that explains the classification.
	Cause error
}

// Errorf builds a classified error with a formatted message.
func Errorf(code Code, format string, args ...any) *Error {
	return &Error{Code: code, Msg: fmt.Sprintf(format, args...)}
}

// Wrapf builds a classified error around an underlying cause. A nil cause is
// not an error-free result: it yields a classified error with no cause, since
// a caller that reached Wrapf has already decided something failed.
func Wrapf(code Code, cause error, format string, args ...any) *Error {
	return &Error{Code: code, Msg: fmt.Sprintf(format, args...), Cause: cause}
}

func (e *Error) Error() string {
	switch {
	case e.Cause == nil:
		return e.Msg
	case e.Msg == "":
		return e.Cause.Error()
	default:
		return e.Msg + ": " + e.Cause.Error()
	}
}

// Unwrap exposes the cause to errors.Is and errors.As.
func (e *Error) Unwrap() error { return e.Cause }

// Is reports whether this error answers to a sentinel. Only the sentinel for
// its own code matches; the cause chain is handled by Unwrap.
func (e *Error) Is(target error) bool {
	sentinel, ok := sentinels[e.Code]
	return ok && target == sentinel
}

// CodeOf classifies any error, looking through wrapping. A nil error has the
// empty code — "nothing failed" is distinct from "failed for an unknown
// reason". Anything else that carries no classification is CodeUnexpected, per
// §12: a door therefore never holds an error it cannot put in the envelope.
func CodeOf(err error) Code {
	if err == nil {
		return ""
	}
	var classified *Error
	if errors.As(err, &classified) {
		return classified.Code
	}
	return CodeUnexpected
}
