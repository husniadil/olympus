package olympus

import (
	"context"
	"encoding/json"

	"github.com/husniadil/olympus/backend"
)

// Screens is a capture of several targets at once.
//
// The two maps are parallel and keyed by target. A target whose screen is empty
// while its metadata says alt-screen was SKIPPED rather than empty — which is
// the whole reason the metadata travels alongside the text.
type Screens struct {
	Screens  map[string]string             `json:"screens"`
	Meta     map[string]backend.ScreenMeta `json:"meta"`
	Warnings []Warning                     `json:"-"`
}

// MarshalJSON emits empty maps rather than nulls.
//
// api §2 requires an empty collection to serialize as an empty collection, never
// null — and the case that forces this is the ZERO value, which is what a
// failing call hands back. A consumer indexing into the result must not have to
// nil-check a field the contract says is always an object.
func (s Screens) MarshalJSON() ([]byte, error) {
	type shape struct {
		Screens map[string]string             `json:"screens"`
		Meta    map[string]backend.ScreenMeta `json:"meta"`
	}
	out := shape{Screens: s.Screens, Meta: s.Meta}
	if out.Screens == nil {
		out.Screens = map[string]string{}
	}
	if out.Meta == nil {
		out.Meta = map[string]backend.ScreenMeta{}
	}
	return json.Marshal(out)
}

// Capture reads several targets in one call.
//
// It applies the door's own rule, which is stricter than the backend's: the
// metadata for every target is gathered FIRST, and where the alt-screen flag is
// true the capture is skipped entirely and the screen comes back empty
// (behavior §5.3).
//
// The backend layer deliberately does not do this — it never refuses a target,
// it just returns whatever the underlying call yields. The policy belongs here
// because it depends on what the caller is going to do with the answer: a pane
// on the alternate screen has no scrollback, so capturing it only re-reports a
// grid a live consumer already mirrors, and the flag is what makes the empty
// result mean "skipped by design".
func (o *Olympus) Capture(ctx context.Context, targets []string, opts ...ScreenOption) (Screens, error) {
	if len(targets) == 0 {
		return Screens{}, backend.Errorf(backend.CodeUsage, "no targets given to capture")
	}

	var options backend.ScreenOpts
	for _, opt := range opts {
		opt(&options)
	}

	out := Screens{
		Screens: make(map[string]string, len(targets)),
		Meta:    make(map[string]backend.ScreenMeta, len(targets)),
	}
	for _, target := range targets {
		resolved, err := o.resolveTarget(ctx, target)
		if err != nil {
			return Screens{}, err
		}

		meta, err := o.backend.ScreenMeta(ctx, resolved)
		if err != nil {
			return Screens{}, err
		}
		out.Meta[target] = meta
		if meta.AltScreen {
			// Skipped, not captured-and-empty. The flag beside it is what
			// tells the caller which of those two this is.
			out.Screens[target] = ""
			continue
		}

		capture, err := o.backend.Screen(ctx, resolved, options)
		if err != nil {
			return Screens{}, err
		}
		out.Screens[target] = capture.Text
	}

	// Announced once for the whole call, never once per target: a warning per
	// row is noise that trains users to ignore the mechanism (behavior §0.8).
	out.Warnings = append(out.Warnings, warn(o.resolution.Backend, opCapture)...)
	out.Warnings = append(out.Warnings, warn(o.resolution.Backend, opCaptureMeta)...)
	if options.HistoryLines > 0 {
		out.Warnings = append(out.Warnings, warn(o.resolution.Backend, opCaptureHistory)...)
	}
	return out, nil
}

// Panes lists panes: every pane on the backend when target is empty, or one
// session's when it is not.
//
// Listing every pane is a different question from asking about one session, and
// a caller reconciling state needs it: a pane id is the only handle some
// consumers hold, and resolving one requires seeing them all.
func (o *Olympus) Panes(ctx context.Context, target string) ([]backend.Pane, error) {
	resolved := ""
	if target != "" {
		var err error
		resolved, err = o.resolveTarget(ctx, target)
		if err != nil {
			return nil, err
		}
	}

	panes, err := o.backend.Panes(ctx, resolved)
	if err != nil {
		return nil, err
	}
	if panes == nil {
		panes = []backend.Pane{}
	}
	return panes, nil
}

// PaneWarnings are the disclosures that apply to any pane listing.
func (o *Olympus) PaneWarnings() []Warning { return warn(o.resolution.Backend, opPaneListing) }

// Create makes a NEW session and fails if the name is taken.
//
// This is deliberately distinct from Session, which is ensure-semantics. Most
// callers want ensure — that is why it has the shorter name and the `start`
// verb — but a caller that means "this must not already exist" cannot express
// it through ensure, and finding out by reading the outcome afterwards is a
// race rather than a check.
func (o *Olympus) Create(ctx context.Context, name string, opts ...SessionOption) (*Session, error) {
	spec := backend.CreateSpec{Name: name, Cols: DefaultCols, Rows: DefaultRows}
	for _, opt := range opts {
		opt(&spec)
	}

	var row backend.Session
	// The check and the create are one critical section, for the same reason
	// ensure's are: without it two concurrent creates can both observe
	// "absent", and the loser's outcome is backend-defined (behavior §2.6).
	err := engineWithLock(ctx, o, name, func() error {
		sessions, err := o.backend.Sessions(ctx)
		if err != nil {
			return err
		}
		for _, s := range sessions {
			if s.Name == name {
				return backend.Errorf(backend.CodeConflict, "session %s already exists", name)
			}
		}
		row, err = o.backend.Create(ctx, spec)
		if err != nil {
			return err
		}
		row.Outcome = backend.OutcomeCreated
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &Session{ol: o, name: name, row: row}, nil
}
