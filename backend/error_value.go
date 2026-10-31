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
// it would say nothing a failed match against the other seven does not already
// say. Read CodeOf when the classification itself is the question.
var (
	ErrUsage       = errors.New("usage")
	ErrNotFound    = errors.New("not found")
	ErrUnavailable = errors.New("backend unavailable")
	ErrTimeout     = errors.New("timed out")
	ErrConflict    = errors.New("conflict")
	ErrUnsupported = errors.New("unsupported")
	ErrBlocked     = errors.New("agent blocked")
)

// sentinels maps a code to the value errors.Is matches it against.
var sentinels = map[Code]error{
	CodeUsage:              ErrUsage,
	CodeSessionNotFound:    ErrNotFound,
	CodeBackendUnavailable: ErrUnavailable,
	CodeTimeout:            ErrTimeout,
	CodeConflict:           ErrConflict,
	CodeUnsupported:        ErrUnsupported,
	CodeAgentBlocked:       ErrBlocked,
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
	// Typed marks a send whose text was typed and may still be in the input
	// box: an AGENT_BLOCKED whose agent started waiting after the typing
	// (behavior §7.5), a TIMEOUT whose box held only part of the text (§7.6),
	// a write a backend made in pieces that failed after one landed (§4.3), or
	// a failure whose text arrived and whose terminator did not (§4.4, §4.7).
	// A second send would type it again.
	Typed bool
	// Reason says which of a code's causes this is, where a caller acts on
	// them differently and the code alone does not tell (api §2's
	// `error.reason`). Empty for every failure that has none.
	Reason Reason
	// Uncertain marks a write the backend accepted and then lost track of, so
	// whether it reached the pane is unknown. It is never retried: a resend
	// could deliver it twice (behavior §4.4).
	Uncertain bool
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

// TypedOf reports whether err is a failed send whose text was already typed
// (behavior §7.5, §7.6). Every other error, and nil, is false.
func TypedOf(err error) bool {
	var classified *Error
	return errors.As(err, &classified) && classified.Typed
}

// A Reason is one of api §2's `error.reason` values. Like a code it is
// semver-bound: a shipped reason is never repurposed or removed.
type Reason string

// ReasonNoInputBox is an AGENT_BLOCKED whose agent draws an input box and
// showed none: something is open over it, or it is not drawn yet (behavior
// §7.5). Without it, an AGENT_BLOCKED is an agent waiting on a person.
const ReasonNoInputBox Reason = "no_input_box"

// ReasonOf reports err's reason, looking through wrapping. Every error that
// has none, and nil, has the empty reason.
func ReasonOf(err error) Reason {
	var classified *Error
	if errors.As(err, &classified) {
		return classified.Reason
	}
	return ""
}

// Unsubmitted classifies the failure of an Enter sent once its text had landed
// (behavior §4.4, §4.7). The text is in the input line, where a second send
// would type it again, so the failure is marked typed: one with a code of its
// own keeps it, so it answers to one sentinel, and any other becomes a
// TIMEOUT. Two come back as they were: an Enter of unknown delivery, which may
// have submitted, and a session that is gone, which has no input line left.
func Unsubmitted(err error, target string) error {
	code := CodeOf(err)
	if UncertainOf(err) || code == CodeSessionNotFound {
		return err
	}
	if code == CodeUnexpected {
		code = CodeTimeout
	}
	typed := Wrapf(code, err, "text was delivered to %s but not submitted, and is still sitting in the input line", target)
	typed.Typed = true
	return typed
}

// UncertainOf reports whether err is a write of unknown delivery (behavior
// §4.4). Every other error, and nil, is false.
func UncertainOf(err error) bool {
	var classified *Error
	return errors.As(err, &classified) && classified.Uncertain
}
