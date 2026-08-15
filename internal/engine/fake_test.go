package engine_test

import (
	"context"
	"sync"

	"github.com/husniadil/olympus/backend"
)

// fakeBackend is a scripted stand-in. It records what was asked of it and lets
// a test decide what the screen shows, so the engines can be driven through
// sequences a real terminal would only produce under load or under a race.
type fakeBackend struct {
	mu sync.Mutex

	screen   string
	meta     backend.ScreenMeta
	caps     backend.Capabilities
	sessions []backend.Session
	panes    []backend.Pane
	state    backend.State

	typed   []string
	pasted  []string
	atomic  []string
	submits int
	kills   []string
	created []backend.CreateSpec

	// onType runs after each Type, so a test can make the screen react the way
	// a pane's echo would.
	onType func(f *fakeBackend, text string)
	// onScreen runs before each capture, for tests that need the screen to
	// change over time.
	onScreen func(f *fakeBackend)

	typeErr   error
	submitErr error
	screenErr error
	createErr error
}

func (f *fakeBackend) Capabilities() backend.Capabilities { return f.caps }

func (f *fakeBackend) Version(context.Context) (string, error) { return "1.0", nil }

func (f *fakeBackend) Create(_ context.Context, spec backend.CreateSpec) (backend.Session, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.created = append(f.created, spec)
	if f.createErr != nil {
		return backend.Session{}, f.createErr
	}
	row := backend.Session{Name: spec.Name, ID: spec.Name, Liveness: backend.LivenessPresent, CWD: spec.Dir}
	f.sessions = append(f.sessions, row)
	f.state = backend.StatePresent
	return row, nil
}

func (f *fakeBackend) Sessions(context.Context) ([]backend.Session, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]backend.Session, len(f.sessions))
	copy(out, f.sessions)
	return out, nil
}

func (f *fakeBackend) Panes(context.Context, string) ([]backend.Pane, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]backend.Pane, len(f.panes))
	copy(out, f.panes)
	return out, nil
}

func (f *fakeBackend) Probe(context.Context, string) backend.State {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.state == "" {
		return backend.StateAbsent
	}
	return f.state
}

func (f *fakeBackend) Kill(_ context.Context, target string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.kills = append(f.kills, target)
	f.state = backend.StateAbsent
	kept := f.sessions[:0]
	for _, s := range f.sessions {
		if s.Name != target {
			kept = append(kept, s)
		}
	}
	f.sessions = kept
	return nil
}

func (f *fakeBackend) Interrupt(context.Context, string) error { return nil }

func (f *fakeBackend) Type(_ context.Context, _, text string) error {
	f.mu.Lock()
	if f.typeErr != nil {
		err := f.typeErr
		f.mu.Unlock()
		return err
	}
	f.typed = append(f.typed, text)
	hook := f.onType
	f.mu.Unlock()
	if hook != nil {
		hook(f, text)
	}
	return nil
}

func (f *fakeBackend) Paste(_ context.Context, _, text string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.pasted = append(f.pasted, text)
	return nil
}

func (f *fakeBackend) Press(context.Context, string, ...backend.Key) error { return nil }

func (f *fakeBackend) Submit(context.Context, string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.submitErr != nil {
		return f.submitErr
	}
	f.submits++
	return nil
}

func (f *fakeBackend) SendAtomic(_ context.Context, _, text string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.atomic = append(f.atomic, text)
	return nil
}

func (f *fakeBackend) Screen(context.Context, string, backend.ScreenOpts) (backend.Capture, error) {
	f.mu.Lock()
	hook := f.onScreen
	f.mu.Unlock()
	if hook != nil {
		hook(f)
	}

	f.mu.Lock()
	defer f.mu.Unlock()
	if f.screenErr != nil {
		return backend.Capture{}, f.screenErr
	}
	return backend.Capture{Text: f.screen}, nil
}

func (f *fakeBackend) ScreenMeta(context.Context, string) (backend.ScreenMeta, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.meta, nil
}

func (f *fakeBackend) Attach(context.Context, string, backend.AttachSpec) (backend.Attachment, error) {
	return backend.Attachment{}, backend.Errorf(backend.CodeUnsupported, "the fake does not attach")
}

func (f *fakeBackend) CreateView(context.Context, string, backend.ViewSpec) (backend.View, error) {
	return backend.View{}, backend.Errorf(backend.CodeUnsupported, "the fake has no views")
}

func (f *fakeBackend) ScrollView(context.Context, string, int) error {
	return backend.Errorf(backend.CodeUnsupported, "the fake has no views")
}

func (f *fakeBackend) Views(context.Context, string) ([]backend.View, error) {
	return nil, backend.Errorf(backend.CodeUnsupported, "the fake has no views")
}

func (f *fakeBackend) ServerEnv(context.Context, string) (string, bool, error) {
	return "", false, backend.Errorf(backend.CodeUnsupported, "the fake has no server environment")
}

func (f *fakeBackend) setScreen(text string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.screen = text
}

func (f *fakeBackend) counts() (typed int, submits int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.typed), f.submits
}

var _ backend.Backend = (*fakeBackend)(nil)
