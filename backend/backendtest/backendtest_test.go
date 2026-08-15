package backendtest

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/husniadil/olympus/backend"
)

// The suite exists to fail. A conformance suite nobody has watched fail is a
// suite nobody has tested: every case here is run against a backend that does
// nothing at all, and a case that passes anyway is asserting nothing.

// inert answers every call with a zero value and no error. It is the shape of a
// backend that compiles and lies.
type inert struct{ caps backend.Capabilities }

func (b inert) Capabilities() backend.Capabilities    { return b.caps }
func (inert) Version(context.Context) (string, error) { return "", nil }
func (inert) Create(context.Context, backend.CreateSpec) (backend.Session, error) {
	return backend.Session{}, nil
}
func (inert) Sessions(context.Context) ([]backend.Session, error)   { return nil, nil }
func (inert) Panes(context.Context, string) ([]backend.Pane, error) { return nil, nil }
func (inert) Probe(context.Context, string) backend.State           { return "" }
func (inert) Kill(context.Context, string) error                    { return nil }
func (inert) Interrupt(context.Context, string) error               { return nil }
func (inert) Type(context.Context, string, string) error            { return nil }
func (inert) Paste(context.Context, string, string) error           { return nil }
func (inert) Press(context.Context, string, ...backend.Key) error   { return nil }
func (inert) Submit(context.Context, string) error                  { return nil }
func (inert) SendAtomic(context.Context, string, string) error      { return nil }
func (inert) Screen(context.Context, string, backend.ScreenOpts) (backend.Capture, error) {
	return backend.Capture{}, nil
}
func (inert) ScreenMeta(context.Context, string) (backend.ScreenMeta, error) {
	return backend.ScreenMeta{}, nil
}
func (inert) Attach(context.Context, string, backend.AttachSpec) (backend.Attachment, error) {
	return backend.Attachment{}, nil
}
func (inert) CreateView(context.Context, string, backend.ViewSpec) (backend.View, error) {
	return backend.View{}, nil
}
func (inert) ScrollView(context.Context, string, int) error           { return nil }
func (inert) Views(context.Context, string) ([]backend.View, error)   { return nil, nil }
func (inert) ServerEnv(context.Context, string) (string, bool, error) { return "", false, nil }

var _ backend.Backend = inert{}
var _ = exec.Command // the interface's attach shape depends on os/exec

// errFatal unwinds a case the way testing.T.Fatalf does.
type errFatal struct{}

// recorder is a Reporter that remembers failures instead of reporting them.
type recorder struct {
	failures []string
	cleanups []func()
}

func (*recorder) Helper() {}

func (r *recorder) Errorf(format string, args ...any) {
	r.failures = append(r.failures, fmt.Sprintf(format, args...))
}

func (r *recorder) Fatalf(format string, args ...any) {
	r.failures = append(r.failures, fmt.Sprintf(format, args...))
	panic(errFatal{})
}

func (r *recorder) Cleanup(fn func()) { r.cleanups = append(r.cleanups, fn) }

func (r *recorder) TempDir() string {
	dir, err := os.MkdirTemp("", "backendtest")
	if err != nil {
		panic(err)
	}
	r.cleanups = append(r.cleanups, func() { _ = os.RemoveAll(dir) })
	return dir
}

// runCase runs one case against a configuration and reports what it complained
// about, rather than failing the enclosing test.
func runCase(c Case, cfg Config) []string {
	r := &recorder{}
	func() {
		defer func() {
			for i := len(r.cleanups) - 1; i >= 0; i-- {
				r.cleanups[i]()
			}
			if v := recover(); v != nil {
				if _, ok := v.(errFatal); !ok {
					panic(v)
				}
			}
		}()
		c.run(r, cfg)
	}()
	return r.failures
}

// Budgets small enough that running the whole suite against a backend that
// will never satisfy it stays worth doing.
var quick = Budgets{
	Warm:   30 * time.Millisecond,
	Screen: 30 * time.Millisecond,
	Settle: 10 * time.Millisecond,
	Poll:   5 * time.Millisecond,
}

func inertConfig(caps backend.Capabilities) Config {
	return Config{
		New:     func(Reporter) backend.Backend { return inert{caps: caps} },
		Budgets: quick,
		// Declared, so the §2.8.1 cases fail on the behavior they assert and
		// not merely on the missing declaration.
		Expect: Expectations{
			InterruptShellBacked: InterruptStops,
			InterruptExecSpawned: InterruptIneffective,
		},
	}
}

func TestEveryCaseFailsAgainstABackendThatDoesNothing(t *testing.T) {
	// Two configurations, because several rules are conditioned on a declared
	// capability: a case about views cannot fail against a backend that
	// truthfully has none. Every case must therefore fail in at least one of
	// them, or it is not testing anything.
	configs := map[string]Config{
		"without optional capabilities": inertConfig(backend.Capabilities{}),
		"with optional capabilities": inertConfig(backend.Capabilities{
			Views:                true,
			ServerEnv:            true,
			TracksAltScreen:      true,
			RendersCurrentScreen: true,
		}),
	}

	for _, c := range Cases() {
		t.Run(c.Name, func(t *testing.T) {
			for name, cfg := range configs {
				if failures := runCase(c, cfg); len(failures) > 0 {
					t.Logf("%s: %s", name, failures[0])
					return
				}
			}
			t.Errorf("this case passed against a backend that does nothing at all, in every configuration — it asserts nothing")
		})
	}
}

// A backend reading a failure needs to know which rule it broke, not only which
// line failed.
func TestEveryCaseNamesItsSpecificationSection(t *testing.T) {
	seen := map[string]bool{}
	for _, c := range Cases() {
		if !strings.HasPrefix(c.Name, "§") {
			t.Errorf("case %q does not open with the specification section it enforces", c.Name)
		}
		if seen[c.Name] {
			t.Errorf("two cases are named %q, so a failure cannot be traced to one of them", c.Name)
		}
		seen[c.Name] = true
	}
	if len(seen) == 0 {
		t.Fatal("the suite has no cases")
	}
}

// §2.8.1's outcomes vary per backend and per session shape. A backend that
// simply forgets to declare one must be told so, not silently given another
// backend's answer.
func TestUndeclaredInterruptOutcomeIsItselfAFailure(t *testing.T) {
	cfg := inertConfig(backend.Capabilities{})
	cfg.Expect = Expectations{}

	for _, c := range Cases() {
		if !strings.Contains(c.Name, "§2.8.1") {
			continue
		}
		failures := runCase(c, cfg)
		if len(failures) == 0 || !strings.Contains(failures[0], "has not declared") {
			t.Errorf("case %q did not insist on a declared outcome; it reported %v", c.Name, failures)
		}
	}
}

// The names the suite generates land in a shared namespace. They must not
// collide with the identifiers Olympus reserves for real sessions (§17.1), or a
// test session becomes indistinguishable from one created for a caller.
func TestGeneratedNamesStayOutOfTheReservedNamespace(t *testing.T) {
	e := &Env{T: t}
	for i := 0; i < 100; i++ {
		name := e.Name()
		if strings.HasPrefix(name, "olympus-") {
			t.Fatalf("generated name %q is inside the reserved namespace", name)
		}
		if len(name) > 40 {
			t.Errorf("generated name %q is %d bytes, which risks the socket-path budget of §2.5", name, len(name))
		}
	}
}

func TestGeneratedNamesAreUnique(t *testing.T) {
	e := &Env{T: t}
	seen := map[string]bool{}
	for i := 0; i < 100; i++ {
		name := e.Name()
		if seen[name] {
			t.Fatalf("generated name %q twice", name)
		}
		seen[name] = true
	}
}

func TestPlausibleEpochRejectsTheSilentZero(t *testing.T) {
	// The failure this guards against: a wrong format variable expands to the
	// empty string with exit 0, so the column parses as zero rather than
	// erroring. Zero must not be plausible.
	if PlausibleEpoch(0) {
		t.Error("zero is treated as a plausible creation time, so a silently missing timestamp would pass")
	}
	if !PlausibleEpoch(time.Now().Unix()) {
		t.Error("now is not treated as a plausible creation time")
	}
	if PlausibleEpoch(time.Now().Add(24 * time.Hour).Unix()) {
		t.Error("a timestamp a day in the future is treated as plausible")
	}
}
