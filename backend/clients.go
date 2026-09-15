package backend

import (
	"context"
	"strings"
	"unicode"
	"unicode/utf8"
)

// A Client is one client attached to a server, and what it shows (behavior
// §13.5). It is the answer to "which session, window and pane is this
// person's client on right now", which a server whose clients each keep their
// own view holds per client and nowhere else.
//
// On herdr a session is a workspace, a window is a tab and a pane is a pane
// (§3.6), and every id here is one a target takes. What the server does not
// report is omitted: Zoomed and ViewApplied are pointers because false is an
// answer, and a server that cannot give one must not be read as giving it.
type Client struct {
	// ID is the server's own number for the client, as a string.
	ID string `json:"id"`
	// Tag is the name the client was launched with, empty for one launched
	// with none.
	Tag string `json:"tag,omitempty"`
	// SessionID and WindowID are the session and window the client shows.
	SessionID string `json:"session_id,omitempty"`
	WindowID  string `json:"window_id,omitempty"`
	// PaneID is the focused pane of the window the client shows, which is
	// where what the client sends goes.
	PaneID string `json:"pane_id,omitempty"`
	// Zoomed is whether that window shows PaneID alone. Nil where the server
	// does not report it.
	Zoomed *bool `json:"zoomed,omitempty"`
	// ViewApplied is whether the client has applied the view the server holds
	// for it, so that what it sends now reaches PaneID. Nil where the server
	// does not report it, or the client does not acknowledge what it applies.
	ViewApplied *bool `json:"view_applied,omitempty"`
}

// A ClientLister lists the clients attached to the server this handle
// addresses. Optional: a backend whose server cannot say which client shows
// what does not implement it, and the layer above reports CodeUnsupported. A
// backend that implements it answers CodeUnsupported itself for a server that
// cannot (§13.5).
type ClientLister interface {
	Clients(ctx context.Context) ([]Client, error)
}

// maxClientTagBytes is the longest tag a server holds (§13.5).
const maxClientTagBytes = 128

// CheckClientTag reports whether a caller's client tag is one a server holds:
// one to 128 bytes of UTF-8, with no control character. Anything else is
// CodeUsage, since the caller could have validated it (§12).
func CheckClientTag(tag string) error {
	switch {
	case tag == "":
		return Errorf(CodeUsage, "a client tag cannot be empty")
	case len(tag) > maxClientTagBytes:
		return Errorf(CodeUsage, "a client tag is at most %d bytes; %q is %d", maxClientTagBytes, tag, len(tag))
	case !utf8.ValidString(tag):
		return Errorf(CodeUsage, "a client tag must be UTF-8; %q is not", tag)
	case strings.IndexFunc(tag, unicode.IsControl) >= 0:
		return Errorf(CodeUsage, "a client tag cannot carry a control character; %q does", tag)
	}
	return nil
}
