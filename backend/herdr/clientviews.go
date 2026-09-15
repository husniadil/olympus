package herdr

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net"
	"strconv"
	"syscall"
	"time"

	"github.com/husniadil/olympus/backend"
)

// A herdr that can move ONE client's view answers `ping` with
// `capabilities.client_view_focus`, and then takes three things this backend
// uses in place of the walk (§8.10): a client launched with `--workspace
// <id>` comes up on that workspace without moving the server's focus or any
// other client, `--client-tag <tag>` names it, and the socket's
// `client.list` and `client.view.focus` read and move it by that name.
//
// Those two requests have no CLI verb, so they, and the ping that says they
// are there, go over the API socket directly: one JSON line out, one back,
// on a connection of their own, which is the whole of herdr's request
// framing. Every other request this backend makes still goes through the
// CLI.

// apiCallBudget bounds one request over the socket where the caller's
// context sets no deadline of its own. Every request here answers from the
// server's memory, so a server that has not answered in this long is not
// going to.
const apiCallBudget = 5 * time.Second

// clientTagPrefix begins every tag a bare attach gives its client (§17.1),
// so a client Olympus launched reads as one in `client.list`.
const clientTagPrefix = "olympus-client-"

// call sends one request to the server's API socket and returns its result.
// A refusal is classified the way the CLI's envelope is (classify), so a
// workspace that is gone is not-found through either door.
func (h *Herdr) call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	if err := h.validateSocketPath(); err != nil {
		return nil, err
	}
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, apiCallBudget)
		defer cancel()
	}
	request, err := json.Marshal(struct {
		ID     string `json:"id"`
		Method string `json:"method"`
		Params any    `json:"params"`
	}{"olympus:" + method, method, params})
	if err != nil {
		return nil, backend.Wrapf(backend.CodeUnexpected, err, "encoding the herdr request %s", method)
	}
	var dialer net.Dialer
	conn, err := dialer.DialContext(ctx, "unix", h.socketPath)
	if err != nil {
		return nil, backend.Wrapf(backend.CodeBackendUnavailable, err, "reaching the herdr server at %s", h.socketPath)
	}
	defer conn.Close()
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}
	if _, err := conn.Write(append(request, '\n')); err != nil {
		return nil, backend.Wrapf(backend.CodeBackendUnavailable, err, "sending the herdr request %s", method)
	}
	line, err := bufio.NewReader(conn).ReadString('\n')
	if err != nil {
		return nil, backend.Wrapf(backend.CodeBackendUnavailable, err, "reading the herdr answer to %s", method)
	}
	var reply struct {
		Result json.RawMessage `json:"result"`
		Error  *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(line), &reply); err != nil {
		return nil, backend.Wrapf(backend.CodeUnexpected, err, "reading the herdr answer to %s", method)
	}
	if reply.Error != nil {
		return nil, classify(errors.New(reply.Error.Message), line, "", []string{method})
	}
	return reply.Result, nil
}

// serverCaps is what a server's `ping` says it can do for one client.
type serverCaps struct {
	// viewFocus is `client_view_focus`: a client is launched onto a
	// workspace with a tag, and read and moved by it.
	viewFocus bool
	// viewAck is `client_view_ack`: `client.list` says whether a client has
	// applied the view it is shown, and `client.view.wait`, or
	// `client.view.focus` with `wait`, answers once it has.
	viewAck bool
	// viewPane is `client_view_pane`: `client.view.focus` takes a pane and
	// focuses it on its tab for that client, without zooming it.
	viewPane bool
}

// clientViews reports whether this backend's server moves one client's view
// (`client_view_focus`).
func (h *Herdr) clientViews(ctx context.Context) (bool, error) {
	caps, err := h.capabilities(ctx)
	return caps.viewFocus, err
}

// capabilities reads the server's capabilities, asked once per handle and
// kept: the answer belongs to the server, which does not change what it is
// while it runs. A failed ask is not kept, and a server this handle starts or
// stops forgets it.
func (h *Herdr) capabilities(ctx context.Context) (serverCaps, error) {
	h.mu.Lock()
	known, caps := h.capsKnown, h.caps
	h.mu.Unlock()
	if known {
		return caps, nil
	}
	result, err := h.call(ctx, "ping", struct{}{})
	if err != nil {
		return serverCaps{}, err
	}
	caps, err = parsePong(result)
	if err != nil {
		return serverCaps{}, err
	}
	h.mu.Lock()
	h.capsKnown, h.caps = true, caps
	h.mu.Unlock()
	return caps, nil
}

// forgetCapabilities drops what was read about the server, for a handle that
// has just started or stopped one: the next server on the socket may be
// another build.
func (h *Herdr) forgetCapabilities() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.capsKnown, h.caps = false, serverCaps{}
}

// parsePong reads `client_view_focus` and `client_view_ack` out of a ping's
// result. A server that predates a capability, or every capability, reports
// none: that is false.
func parsePong(result json.RawMessage) (serverCaps, error) {
	var pong struct {
		Type         string `json:"type"`
		Capabilities *struct {
			ClientViewFocus bool `json:"client_view_focus"`
			ClientViewAck   bool `json:"client_view_ack"`
			ClientViewPane  bool `json:"client_view_pane"`
		} `json:"capabilities"`
	}
	if err := json.Unmarshal(result, &pong); err != nil || pong.Type != "pong" {
		return serverCaps{}, backend.Errorf(backend.CodeUnexpected, "herdr answered a ping with %s", result)
	}
	if pong.Capabilities == nil {
		return serverCaps{}, nil
	}
	return serverCaps{
		viewFocus: pong.Capabilities.ClientViewFocus,
		viewAck:   pong.Capabilities.ClientViewAck,
		viewPane:  pong.Capabilities.ClientViewPane,
	}, nil
}

// newClientTag draws the name one bare client is addressed by. Random rather
// than derived from the attach, since two attaches onto one target are two
// clients, and herdr does not hold tags unique.
func newClientTag() (string, error) {
	var nonce [8]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return "", backend.Wrapf(backend.CodeUnexpected, err, "drawing a client tag")
	}
	return clientTagPrefix + hex.EncodeToString(nonce[:]), nil
}

// A clientRow is one client in `client.list`, and the client a
// `client.view.focus` or `client.view.wait` answers with. The last four
// fields are reported only by a server with `client_view_ack`, and read as
// false and empty elsewhere.
type clientRow struct {
	ClientID    uint64 `json:"client_id"`
	ClientTag   string `json:"client_tag"`
	WorkspaceID string `json:"workspace_id"`
	TabID       string `json:"tab_id"`
	// PaneID is the focused pane of the tab the client shows, and Zoomed
	// whether that tab is zoomed.
	PaneID string `json:"pane_id"`
	Zoomed bool   `json:"zoomed"`
	// SnapshotAcks is whether the client acknowledges the snapshots it
	// applies, without which it cannot be waited on.
	SnapshotAcks bool `json:"snapshot_acks"`
	// ViewApplied is whether the client has acknowledged a snapshot showing
	// its current view, so input it sends now reaches PaneID.
	ViewApplied bool `json:"view_applied"`
}

// findClient picks the client carrying a tag out of a `client.list` result.
func findClient(result json.RawMessage, tag string) (clientRow, bool, error) {
	var list struct {
		Clients []clientRow `json:"clients"`
	}
	if err := json.Unmarshal(result, &list); err != nil {
		return clientRow{}, false, backend.Wrapf(backend.CodeUnexpected, err, "reading the herdr client list")
	}
	for _, c := range list.Clients {
		if c.ClientTag == tag {
			return c, true, nil
		}
	}
	return clientRow{}, false, nil
}

// taggedClient reads where the client carrying a tag is. Not found is not an
// error: a client that has not connected yet, or has gone, is simply absent.
func (h *Herdr) taggedClient(ctx context.Context, tag string) (clientRow, bool, error) {
	result, err := h.call(ctx, "client.list", struct{}{})
	if err != nil {
		return clientRow{}, false, err
	}
	return findClient(result, tag)
}

// Clients lists the clients attached to this backend's server, and what each
// shows (§13.5). Only a server that advertises `client_view_focus` reports
// per client where it is; one that does not is unsupported, since an empty
// list would claim it has no clients. No server running is an empty list
// (§3.3): there is nothing to find, and nothing went wrong asking.
func (h *Herdr) Clients(ctx context.Context) ([]backend.Client, error) {
	caps, err := h.capabilities(ctx)
	if err != nil {
		if noServerAt(err) {
			return []backend.Client{}, nil
		}
		return nil, err
	}
	if !caps.viewFocus {
		return nil, backend.Errorf(backend.CodeUnsupported,
			"this herdr server does not advertise client_view_focus, so it cannot say which client shows what")
	}
	result, err := h.call(ctx, "client.list", struct{}{})
	if err != nil {
		if noServerAt(err) {
			return []backend.Client{}, nil
		}
		return nil, err
	}
	return clientsOf(result, caps.viewAck)
}

// noServerAt reports whether a request failed because nothing is listening
// on the socket: no socket file, or one a stopped server left behind.
func noServerAt(err error) bool {
	return errors.Is(err, syscall.ENOENT) || errors.Is(err, syscall.ECONNREFUSED)
}

// clientsOf reads a `client.list` result into rows. The pane and the zoom are
// reported only by a server with `client_view_ack`, and whether the view is
// applied only for a client there that acknowledges what it applies;
// elsewhere they are left unset rather than read as false.
func clientsOf(result json.RawMessage, viewAck bool) ([]backend.Client, error) {
	var list struct {
		Clients []clientRow `json:"clients"`
	}
	if err := json.Unmarshal(result, &list); err != nil {
		return nil, backend.Wrapf(backend.CodeUnexpected, err, "reading the herdr client list")
	}
	rows := make([]backend.Client, 0, len(list.Clients))
	for _, c := range list.Clients {
		row := backend.Client{
			ID:        strconv.FormatUint(c.ClientID, 10),
			Tag:       c.ClientTag,
			SessionID: c.WorkspaceID,
			WindowID:  c.TabID,
			PaneID:    c.PaneID,
		}
		if viewAck && c.PaneID != "" {
			zoomed := c.Zoomed
			row.Zoomed = &zoomed
		}
		if viewAck && c.SnapshotAcks {
			applied := c.ViewApplied
			row.ViewApplied = &applied
		}
		rows = append(rows, row)
	}
	return rows, nil
}
