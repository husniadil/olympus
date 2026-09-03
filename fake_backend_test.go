package olympus

import (
	"context"
	"io"
	"strings"

	"github.com/husniadil/olympus/backend"
)

// A fakeBackend is a scripted stand-in for the ergonomic layer's own tests.
//
// The suites in olympus_test drive real multiplexers, which is the right shape
// for anything the backend decides — but not for a rule this layer owns and a
// real backend may never exercise: no installed backend reports the alternate
// screen on demand, and none can be asked to resolve a pane id that does not
// exist. Those are decisions made here, so they are tested against a backend
// that answers exactly what the case needs.
//
// Everything not scripted answers unsupported or empty. A test that reaches one
// of those is asking the fake a question it was not built to answer.
type fakeBackend struct {
	caps     backend.Capabilities
	panes    []backend.Pane
	sessions []backend.Session
	meta     backend.ScreenMeta
	text     string

	// screenOpts records what each capture ASKED for, which is the only way to
	// observe that a history request was dropped rather than merely unanswered.
	screenOpts []backend.ScreenOpts
	// scrolled records the target every ScrollView was given, AFTER resolution.
	scrolled []string
	// focused records the target every FocusView was given, AFTER resolution.
	focused []string
	typed   []string
	submits int
	// submitFailures is how many of the next terminators are dropped.
	submitFailures int
	// onType runs after each Type, so a case that needs the screen to react
	// the way a pane's echo would can script it. A run's sentinel id is
	// generated inside the engine, so this is the only way a test can put that
	// run's own markers on the screen.
	onType func(f *fakeBackend, text string)
}

// fakeOlympus wires a fake under the ergonomic layer, with no lock: these cases
// are single-goroutine and locking is §11's subject, not theirs.
func fakeOlympus(f *fakeBackend) *Olympus {
	return &Olympus{
		backend:    f,
		resolution: Resolution{Backend: f.caps.Backend, Reason: ReasonFlag},
	}
}

func (f *fakeBackend) Capabilities() backend.Capabilities      { return f.caps }
func (f *fakeBackend) Version(context.Context) (string, error) { return "1.0", nil }

func (f *fakeBackend) Create(context.Context, backend.CreateSpec) (backend.Session, error) {
	return backend.Session{}, backend.Errorf(backend.CodeUnsupported, "the fake does not create")
}

func (f *fakeBackend) Sessions(context.Context) ([]backend.Session, error) {
	return f.sessions, nil
}

func (f *fakeBackend) Panes(context.Context, string) ([]backend.Pane, error) { return f.panes, nil }

func (f *fakeBackend) Probe(context.Context, string) backend.State { return backend.StatePresent }

func (f *fakeBackend) Kill(context.Context, string) error      { return nil }
func (f *fakeBackend) Interrupt(context.Context, string) error { return nil }

func (f *fakeBackend) Type(_ context.Context, _, text string) error {
	f.typed = append(f.typed, text)
	if f.onType != nil {
		f.onType(f, text)
	}
	return nil
}

func (f *fakeBackend) Paste(context.Context, string, string) error         { return nil }
func (f *fakeBackend) Press(context.Context, string, ...backend.Key) error { return nil }

func (f *fakeBackend) Submit(context.Context, string) error {
	if f.submitFailures > 0 {
		f.submitFailures--
		return backend.Errorf(backend.CodeUnexpected, "the terminator was dropped")
	}
	f.submits++
	return nil
}

func (f *fakeBackend) SendAtomic(context.Context, string, string) error { return nil }

func (f *fakeBackend) Screen(_ context.Context, _ string, opts backend.ScreenOpts) (backend.Capture, error) {
	f.screenOpts = append(f.screenOpts, opts)
	return backend.Capture{Text: f.text}, nil
}

func (f *fakeBackend) ScreenMeta(context.Context, string) (backend.ScreenMeta, error) {
	return f.meta, nil
}

func (f *fakeBackend) Follow(context.Context, string) (io.ReadCloser, error) {
	return io.NopCloser(strings.NewReader(f.text)), nil
}

func (f *fakeBackend) Attach(context.Context, string, backend.AttachSpec) (backend.Attachment, error) {
	return backend.Attachment{}, backend.Errorf(backend.CodeUnsupported, "the fake does not attach")
}

func (f *fakeBackend) SetStatus(context.Context, string, string) error { return nil }
func (f *fakeBackend) Status(context.Context, string) (string, error)  { return "", nil }

func (f *fakeBackend) CreateView(context.Context, string, backend.ViewSpec) (backend.View, error) {
	return backend.View{}, backend.Errorf(backend.CodeUnsupported, "the fake has no views")
}

func (f *fakeBackend) ScrollView(_ context.Context, view string, _ int) error {
	f.scrolled = append(f.scrolled, view)
	return nil
}

func (f *fakeBackend) FocusView(_ context.Context, view string, _, _ int) (string, error) {
	f.focused = append(f.focused, view)
	return "%7", nil
}

func (f *fakeBackend) Views(context.Context, string) ([]backend.View, error) { return nil, nil }

func (f *fakeBackend) ServerEnv(context.Context, string) (string, bool, error) {
	return "", false, backend.Errorf(backend.CodeUnsupported, "the fake has no server environment")
}

var _ backend.Backend = (*fakeBackend)(nil)
