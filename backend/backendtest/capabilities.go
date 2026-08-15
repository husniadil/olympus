package backendtest

import (
	"errors"
	"fmt"
	"time"

	"github.com/husniadil/olympus/backend"
)

func viewCases() []Case {
	return []Case{
		{
			Name: "§13 capabilities name their own backend",
			Fn: func(e *Env) {
				caps := e.Backend.Capabilities()
				if caps.Backend == "" {
					e.T.Errorf("capabilities do not name a backend, so a caller holding them cannot tell what they describe")
				}
			},
		},
		{
			Name: "§9 views are supported or answer unsupported, matching the declared capability",
			Fn: func(e *Env) {
				// The rule under test is that the capability and the error
				// agree. A backend that advertises views and then refuses
				// them, or refuses with the wrong code, sends consumers down
				// a branch that cannot work — and §12 requires unsupported to
				// be distinguishable from unavailable, since only one of them
				// is worth retrying.
				base := e.StartShell()
				name := fmt.Sprintf("olympus-view-%s-%d", base, time.Now().UnixNano()%10000)

				view, err := e.Backend.CreateView(e.Ctx(), base, backend.ViewSpec{Name: name, Mouse: true})
				if !e.Backend.Capabilities().Views {
					if err == nil {
						e.T.Fatalf("a backend that does not advertise views created one anyway")
					}
					if !errors.Is(err, backend.ErrUnsupported) {
						e.T.Errorf("creating a view is %q, want %q — unsupported is not unavailable", backend.CodeOf(err), backend.CodeUnsupported)
					}
					return
				}

				if err != nil {
					e.T.Fatalf("a backend that advertises views failed to create one: %v", err)
				}
				if view.Base != base {
					e.T.Errorf("view names base %q, want %q", view.Base, base)
				}

				views, err := e.Backend.Views(e.Ctx(), base)
				if err != nil {
					e.T.Fatalf("listing views: %v", err)
				}
				var found bool
				for _, v := range views {
					if v.Name == view.Name {
						found = true
					}
				}
				if !found {
					e.T.Errorf("the created view is not in the listing for its base")
				}
			},
		},
		{
			Name: "§9.2 a view's lifetime is independent of its base",
			Fn: func(e *Env) {
				if !e.Backend.Capabilities().Views {
					return
				}
				base := e.StartShell()
				name := fmt.Sprintf("olympus-view-%s-%d", base, time.Now().UnixNano()%10000)
				view, err := e.Backend.CreateView(e.Ctx(), base, backend.ViewSpec{Name: name, Mouse: true})
				if err != nil {
					e.T.Fatalf("creating a view: %v", err)
				}

				if err := e.Backend.Kill(e.Ctx(), view.Name); err != nil {
					e.T.Fatalf("killing the view: %v", err)
				}
				if got := e.Backend.Probe(e.Ctx(), base); got != backend.StatePresent {
					e.T.Errorf("the base session is %q after its view was killed, want %q — the window and pane are shared but the lifetimes are not", got, backend.StatePresent)
				}
			},
		},
		{
			Name: "§12 an unset server-environment key is a negative answer, not an error",
			Fn: func(e *Env) {
				// The distinction §12 insists on: a backend that HAS the
				// concept answers "asked, and it is not set". A backend that
				// does not answers unsupported — the question itself does not
				// apply. Neither is an error about the key.
				_, present, err := e.Backend.ServerEnv(e.Ctx(), "OLYMPUS_NO_SUCH_KEY")

				if !e.Backend.Capabilities().ServerEnv {
					if err == nil {
						e.T.Fatalf("a backend without a server environment answered a read of one")
					}
					if !errors.Is(err, backend.ErrUnsupported) {
						e.T.Errorf("reading a server-environment key is %q, want %q", backend.CodeOf(err), backend.CodeUnsupported)
					}
					return
				}

				if err != nil {
					e.T.Fatalf("reading an unset key returned an error: %v", err)
				}
				if present {
					e.T.Errorf("a key that was never set reports present")
				}
			},
		},
	}
}

// statusCases prove a backend either carries a session's status or refuses it.
//
// A backend that has nowhere to keep one MUST refuse the write. Accepting it
// and reading back nothing is the worst of the three outcomes: a caller waiting
// on a state it can never receive has no failure to react to, only silence.
func statusCases() []Case {
	return []Case{
		{
			Name: "§13.1 a session carries an opaque status, or answers unsupported",
			Fn: func(e *Env) {
				name := e.StartShell()

				err := e.Backend.SetStatus(e.Ctx(), name, "waiting on review")
				if !e.Backend.Capabilities().SessionStatus {
					if err == nil {
						e.T.Fatalf("a backend that does not advertise a session status accepted one anyway, so a caller will wait forever for a state that can never be reported")
					}
					if !errors.Is(err, backend.ErrUnsupported) {
						e.T.Errorf("setting a status is %q, want %q — unsupported is not unavailable", backend.CodeOf(err), backend.CodeUnsupported)
					}
					return
				}
				if err != nil {
					e.T.Fatalf("a backend that advertises a session status failed to set one: %v", err)
				}

				got, err := e.Backend.Status(e.Ctx(), name)
				if err != nil {
					e.T.Fatalf("reading back a status: %v", err)
				}
				// Opaque: the value is returned exactly as given, because the
				// backend has promised not to interpret it.
				if got != "waiting on review" {
					e.T.Errorf("status is %q, want %q", got, "waiting on review")
				}
			},
		},
		{
			Name: "§13.1 a session that has reported nothing reads as empty, not as an error",
			Fn: func(e *Env) {
				name := e.StartShell()
				got, err := e.Backend.Status(e.Ctx(), name)

				if !e.Backend.Capabilities().SessionStatus {
					// The READ must refuse too, not only the write. A backend
					// that answers empty here is indistinguishable from one
					// whose session has simply not reported yet, and a caller
					// cannot tell "nothing to wait for, ever" from "not yet".
					if err == nil {
						e.T.Fatalf("a backend that cannot carry a status answered %q instead of refusing, so a caller cannot tell it apart from a session that has not reported yet", got)
					}
					if !errors.Is(err, backend.ErrUnsupported) {
						e.T.Errorf("reading a status is %q, want %q", backend.CodeOf(err), backend.CodeUnsupported)
					}
					return
				}

				if err != nil {
					// Empty is a real answer, the same tri-state rule as
					// presence: a caller must be able to tell "has reported
					// nothing" from "could not ask".
					e.T.Fatalf("reading an unset status is an error: %v", err)
				}
				if got != "" {
					e.T.Errorf("a session that never reported a status reads as %q", got)
				}
			},
		},
	}
}
