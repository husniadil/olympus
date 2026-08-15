package backend_test

import (
	"context"
	"io"
	"os/exec"
	"strings"
	"testing"

	"github.com/husniadil/olympus/backend"
)

// stub implements the whole interface and nothing else. Its purpose is the
// compile-time assertion below: a change to Backend that a real backend cannot
// satisfy fails here, in the package that owns the contract, rather than in
// each backend separately.
type stub struct{}

func (stub) Capabilities() backend.Capabilities      { return backend.Capabilities{} }
func (stub) Version(context.Context) (string, error) { return "", nil }
func (stub) Create(context.Context, backend.CreateSpec) (backend.Session, error) {
	return backend.Session{}, nil
}
func (stub) Sessions(context.Context) ([]backend.Session, error)   { return nil, nil }
func (stub) Panes(context.Context, string) ([]backend.Pane, error) { return nil, nil }
func (stub) Probe(context.Context, string) backend.State           { return backend.StateAbsent }
func (stub) Kill(context.Context, string) error                    { return nil }
func (stub) Interrupt(context.Context, string) error               { return nil }
func (stub) Type(context.Context, string, string) error            { return nil }
func (stub) Paste(context.Context, string, string) error           { return nil }
func (stub) Press(context.Context, string, ...backend.Key) error   { return nil }
func (stub) Submit(context.Context, string) error                  { return nil }
func (stub) SendAtomic(context.Context, string, string) error      { return nil }
func (stub) Screen(context.Context, string, backend.ScreenOpts) (backend.Capture, error) {
	return backend.Capture{}, nil
}
func (stub) ScreenMeta(context.Context, string) (backend.ScreenMeta, error) {
	return backend.ScreenMeta{}, nil
}
func (stub) Follow(context.Context, string) (io.ReadCloser, error) {
	return io.NopCloser(strings.NewReader("")), nil
}
func (stub) Attach(context.Context, string, backend.AttachSpec) (backend.Attachment, error) {
	return backend.Attachment{}, nil
}
func (stub) CreateView(context.Context, string, backend.ViewSpec) (backend.View, error) {
	return backend.View{}, nil
}
func (stub) ScrollView(context.Context, string, int) error           { return nil }
func (stub) Views(context.Context, string) ([]backend.View, error)   { return nil, nil }
func (stub) ServerEnv(context.Context, string) (string, bool, error) { return "", false, nil }

var _ backend.Backend = stub{}

// api §5 never gave views a payload shape, since the door contract was written
// before the interface. These names are semver-bound from first release for the
// same reason every other row's are, so they are pinned here too.
func TestViewMarshalsToItsSpecShape(t *testing.T) {
	assertJSON(t, backend.View{
		Name:     "olympus-view-build-a1b2",
		Base:     "build",
		ID:       "$9",
		Attached: false,
	}, `{"name":"olympus-view-build-a1b2","base":"build","id":"$9","attached":false}`)
}

// The key vocabulary is Olympus's own, translated by each backend, so a caller
// never has to know tmux's spelling to press a key on zmx.
func TestKeySpellingsAreNeutral(t *testing.T) {
	spellings := []struct{ name, got, want string }{
		{"KeyEnter", string(backend.KeyEnter), "enter"},
		{"KeyEscape", string(backend.KeyEscape), "escape"},
		{"KeyTab", string(backend.KeyTab), "tab"},
		{"KeyBackspace", string(backend.KeyBackspace), "backspace"},
		{"KeyUp", string(backend.KeyUp), "up"},
		{"KeyDown", string(backend.KeyDown), "down"},
		{"KeyLeft", string(backend.KeyLeft), "left"},
		{"KeyRight", string(backend.KeyRight), "right"},
		{"KeyCtrlC", string(backend.KeyCtrlC), "c-c"},
		{"KeyCtrlD", string(backend.KeyCtrlD), "c-d"},
		{"KeyPageUp", string(backend.KeyPageUp), "page-up"},
		{"KeyPageDown", string(backend.KeyPageDown), "page-down"},
	}
	for _, s := range spellings {
		if s.got != s.want {
			t.Errorf("%s is %q, want %q", s.name, s.got, s.want)
		}
	}
}

// §8.7: a viewer must drop resize as well as input. Encoding the roles as a
// closed vocabulary is what stops "read-only" being re-decided per backend.
func TestAttachRoleSpellings(t *testing.T) {
	if got, want := string(backend.RoleController), "controller"; got != want {
		t.Errorf("RoleController is %q, want %q", got, want)
	}
	if got, want := string(backend.RoleViewer), "viewer"; got != want {
		t.Errorf("RoleViewer is %q, want %q", got, want)
	}
}

// An Attachment hands back what to run and how to clean up, never a running
// process: the PTY, the signal handling and the terminal restore of §8.2 belong
// to one shared engine, not to each backend.
func TestAttachmentCarriesACommandAndItsCleanup(t *testing.T) {
	cleaned := false
	att := backend.Attachment{
		Cmd:     exec.Command("true"),
		Cleanup: func() error { cleaned = true; return nil },
	}
	if att.Cmd == nil {
		t.Fatal("Attachment has no command to run")
	}
	if err := att.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if !cleaned {
		t.Error("Close did not run the cleanup")
	}
	// §8.8: a spontaneous exit must still reap. A backend needing no cleanup
	// leaves it nil, and the engine must not have to nil-check at every site.
	if err := (backend.Attachment{}).Close(); err != nil {
		t.Errorf("Close with no cleanup returned %v", err)
	}
}
