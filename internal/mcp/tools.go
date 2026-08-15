package mcp

import (
	"context"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/husniadil/olympus"
	"github.com/husniadil/olympus/backend"
)

// ToolNames is the registered tool surface, pinned.
//
// Exported and asserted so a tool cannot silently appear or vanish: this door's
// tool names are semver-bound, and a client written against them breaks
// invisibly if one is renamed (behavior §15.7, api §7).
var ToolNames = []string{
	"start_session",
	"new_session",
	"list_sessions",
	"stop_session",
	"session_info",
	"list_panes",
	"type_text",
	"send_text",
	"send_keys",
	"paste_text",
	"capture",
	"wait_for",
	"run_command",
	"start_run",
	"poll_run",
	"exit_status",
	"create_view",
	"scroll_view",
	"list_views",
	"server_env",
	"capabilities",
	"doctor",
	"version",
}

// Parameter and result shapes. They mirror the ergonomic layer and the CLI:
// the door translates, it does not decide, so a default or a field invented
// here would be a second contract (behavior §15.6).

type targetParams struct {
	Target string `json:"target" jsonschema:"the session to address"`
}

type startParams struct {
	Name    string   `json:"name" jsonschema:"the session name"`
	Dir     string   `json:"dir,omitempty" jsonschema:"working directory for the session"`
	Cols    int      `json:"cols,omitempty" jsonschema:"initial width; ignored by backends with no spawn-time sizing"`
	Rows    int      `json:"rows,omitempty" jsonschema:"initial height; ignored by backends with no spawn-time sizing"`
	Command []string `json:"command,omitempty" jsonschema:"argv to spawn instead of a login shell; executed, never typed"`
}

type textParams struct {
	Target string `json:"target" jsonschema:"the session to address"`
	Text   string `json:"text" jsonschema:"the text to deliver"`
}

type keysParams struct {
	Target string   `json:"target" jsonschema:"the session to address"`
	Keys   []string `json:"keys" jsonschema:"named keys, for example enter, escape, c-c, up"`
}

type captureParams struct {
	Targets []string `json:"targets" jsonschema:"one or more sessions to capture in a single call"`
	Colors  bool     `json:"colors,omitempty" jsonschema:"keep ANSI escapes in the captured text"`
	History int      `json:"history,omitempty" jsonschema:"lines of scrollback to include above the visible screen"`
}

type waitParams struct {
	Target   string `json:"target" jsonschema:"the session to address"`
	Pattern  string `json:"pattern" jsonschema:"a regular expression to wait for"`
	Seconds  int    `json:"seconds,omitempty" jsonschema:"how long to wait, in seconds"`
	Interval int    `json:"interval_ms,omitempty" jsonschema:"how often to re-read the screen, in milliseconds"`
}

type listPaneParams struct {
	Target string `json:"target,omitempty" jsonschema:"limit to one session; omit for every pane on the backend"`
}

type commandParams struct {
	Target  string `json:"target" jsonschema:"the session to address"`
	Command string `json:"command" jsonschema:"the command to run; must be a single line"`
	Seconds int    `json:"seconds,omitempty" jsonschema:"how long to wait for the command, in seconds"`
}

type pollParams struct {
	Target string `json:"target" jsonschema:"the session the run was started in"`
	ID     string `json:"id" jsonschema:"the id start_run returned"`
	Lines  int    `json:"lines,omitempty" jsonschema:"scrollback window to search; ignored where scrollback is native"`
}

type markerParams struct {
	Target string `json:"target" jsonschema:"the session to address"`
	Marker string `json:"marker" jsonschema:"the completion marker to look for; there is deliberately no default"`
	Lines  int    `json:"lines,omitempty" jsonschema:"scrollback window to search; ignored where scrollback is native"`
}

type viewParams struct {
	Base    string `json:"base" jsonschema:"the session to look onto"`
	NoMouse bool   `json:"no_mouse,omitempty" jsonschema:"do not enable wheel scrolling into the view's history"`
}

type scrollParams struct {
	View  string `json:"view" jsonschema:"the view to scroll"`
	Lines int    `json:"lines" jsonschema:"lines to scroll; negative scrolls back toward the live bottom"`
}

type listViewParams struct {
	Base string `json:"base,omitempty" jsonschema:"limit to one base session"`
}

type envParams struct {
	Key string `json:"key" jsonschema:"the environment key to read"`
}

type emptyParams struct{}

type acknowledged struct {
	Target string `json:"target"`
}

type startedRun struct {
	CommandID string `json:"command_id"`
}

type markerResult struct {
	Found bool `json:"found"`
	// ExitCode is present only when a well-formed marker was found. A marker
	// that is present but malformed reports not-found, which is a false
	// negative in the safe direction.
	ExitCode *int `json:"exit_code,omitempty"`
}

type envResult struct {
	Key     string `json:"key"`
	Present bool   `json:"present"`
	Value   string `json:"value,omitempty"`
}

type versionResult struct {
	Version string `json:"version"`
}

func register(s *sdk.Server) {
	addTool(s, "start_session", "Create a session, or reuse one that is already alive.",
		func(ctx context.Context, ol *olympus.Olympus, in startParams) (backend.Session, []olympus.Warning, error) {
			var opts []olympus.SessionOption
			if in.Dir != "" {
				opts = append(opts, olympus.In(in.Dir))
			}
			if in.Cols > 0 || in.Rows > 0 {
				opts = append(opts, olympus.Size(in.Cols, in.Rows))
			}
			if len(in.Command) > 0 {
				opts = append(opts, olympus.Command(in.Command...))
			}
			session, err := ol.Session(ctx, in.Name, opts...)
			if err != nil {
				return backend.Session{}, nil, err
			}
			row := session.Row()
			row.Outcome = session.Outcome()
			return row, nil, nil
		})

	addTool(s, "list_sessions", "List sessions on the resolved backend. Sessions are backend-scoped and never migrate.",
		func(ctx context.Context, ol *olympus.Olympus, _ emptyParams) ([]backend.Session, []olympus.Warning, error) {
			sessions, err := ol.Sessions(ctx)
			if sessions == nil {
				// Never null: an empty collection is an empty list.
				sessions = []backend.Session{}
			}
			return sessions, nil, err
		})

	addTool(s, "stop_session", "End a session, interrupting it before forcing. Reports gone, graceful, or killed; all three are successes.",
		func(ctx context.Context, ol *olympus.Olympus, in targetParams) (olympus.Stopped, []olympus.Warning, error) {
			stopped, err := ol.Stop(ctx, in.Target)
			return stopped, stopped.Warnings, err
		})

	addTool(s, "session_info", "Report a session's detail and whether it is present. Does NOT fail on a target that does not exist: state is present, absent, or error.",
		func(ctx context.Context, ol *olympus.Olympus, in targetParams) (olympus.Info, []olympus.Warning, error) {
			info, err := ol.Info(ctx, in.Target)
			return info, info.Warnings, err
		})

	addTool(s, "type_text", "Place literal text in the input line WITHOUT submitting it.",
		func(ctx context.Context, ol *olympus.Olympus, in textParams) (acknowledged, []olympus.Warning, error) {
			return withSession(ctx, ol, in.Target, func(s *olympus.Session) (acknowledged, error) {
				return acknowledged{Target: s.Name()}, s.Type(ctx, in.Text)
			})
		})

	addTool(s, "send_text", "Deliver text, confirm it is on screen, and only then submit it.",
		func(ctx context.Context, ol *olympus.Olympus, in textParams) (acknowledged, []olympus.Warning, error) {
			return withSession(ctx, ol, in.Target, func(s *olympus.Session) (acknowledged, error) {
				return acknowledged{Target: s.Name()}, s.Send(ctx, in.Text)
			})
		})

	addTool(s, "send_keys", "Send named keys. Key names are Olympus's own and are translated per backend.",
		func(ctx context.Context, ol *olympus.Olympus, in keysParams) (acknowledged, []olympus.Warning, error) {
			keys := make([]backend.Key, 0, len(in.Keys))
			for _, name := range in.Keys {
				keys = append(keys, backend.Key(name))
			}
			return withSession(ctx, ol, in.Target, func(s *olympus.Session) (acknowledged, error) {
				return acknowledged{Target: s.Name()}, s.Press(ctx, keys...)
			})
		})

	addTool(s, "paste_text", "Place multi-line text in the input line. The final line is never submitted without a separate terminator.",
		func(ctx context.Context, ol *olympus.Olympus, in textParams) (acknowledged, []olympus.Warning, error) {
			return withSession(ctx, ol, in.Target, func(s *olympus.Session) (acknowledged, error) {
				return acknowledged{Target: s.Name()}, s.Paste(ctx, in.Text)
			})
		})

	addTool(s, "capture", "Read the screens of one or more sessions in a single call. A target on the alternate screen is skipped rather than captured: its screen is empty with meta.alt_screen set, which is what distinguishes skipped-by-design from nothing-there.",
		func(ctx context.Context, ol *olympus.Olympus, in captureParams) (olympus.Screens, []olympus.Warning, error) {
			var opts []olympus.ScreenOption
			if in.Colors {
				opts = append(opts, olympus.WithColors())
			}
			if in.History > 0 {
				opts = append(opts, olympus.WithHistory(in.History))
			}
			captured, err := ol.Capture(ctx, in.Targets, opts...)
			return captured, captured.Warnings, err
		})

	addTool(s, "new_session", "Create a session, failing with a conflict if the name is taken. Use start_session to create-or-reuse; this is for a caller that means the session must not already exist.",
		func(ctx context.Context, ol *olympus.Olympus, in startParams) (backend.Session, []olympus.Warning, error) {
			var opts []olympus.SessionOption
			if in.Dir != "" {
				opts = append(opts, olympus.In(in.Dir))
			}
			if in.Cols > 0 || in.Rows > 0 {
				opts = append(opts, olympus.Size(in.Cols, in.Rows))
			}
			if len(in.Command) > 0 {
				opts = append(opts, olympus.Command(in.Command...))
			}
			session, err := ol.Create(ctx, in.Name, opts...)
			if err != nil {
				return backend.Session{}, nil, err
			}
			return session.Row(), nil, nil
		})

	addTool(s, "list_panes", "List panes: every pane on the backend, or one session's. A pane id is not unique across rows once views exist, since a base and its views share the underlying pane.",
		func(ctx context.Context, ol *olympus.Olympus, in listPaneParams) ([]backend.Pane, []olympus.Warning, error) {
			panes, err := ol.Panes(ctx, in.Target)
			return panes, ol.PaneWarnings(), err
		})

	addTool(s, "capabilities", "Report what the resolved backend can do. Branch on these rather than on an unsupported error.",
		func(ctx context.Context, ol *olympus.Olympus, _ emptyParams) (backend.Capabilities, []olympus.Warning, error) {
			return ol.Capabilities(), nil, nil
		})

	addTool(s, "wait_for", "Block until a regular expression appears on the screen.",
		func(ctx context.Context, ol *olympus.Olympus, in waitParams) (olympus.Screen, []olympus.Warning, error) {
			session, err := ol.Open(ctx, in.Target)
			if err != nil {
				return olympus.Screen{}, nil, err
			}
			var opts []olympus.WaitOption
			if in.Seconds > 0 {
				opts = append(opts, olympus.WaitTimeout(time.Duration(in.Seconds)*time.Second))
			}
			if in.Interval > 0 {
				opts = append(opts, olympus.WaitInterval(time.Duration(in.Interval)*time.Millisecond))
			}
			screen, err := session.WaitFor(ctx, in.Pattern, opts...)
			return screen, screen.Warnings, err
		})

	addTool(s, "run_command", "Run a command and wait for it. A non-zero exit code is a RESULT in exit_code, not a failure.",
		func(ctx context.Context, ol *olympus.Olympus, in commandParams) (olympus.Result, []olympus.Warning, error) {
			var opts []olympus.RunOption
			if in.Seconds > 0 {
				opts = append(opts, olympus.RunTimeout(time.Duration(in.Seconds)*time.Second))
			}
			return withSession(ctx, ol, in.Target, func(s *olympus.Session) (olympus.Result, error) {
				return s.Exec(ctx, in.Command, opts...)
			})
		})

	addTool(s, "start_run", "Start a command and return an id to poll. Nothing is written down; the id is the whole handle.",
		func(ctx context.Context, ol *olympus.Olympus, in commandParams) (startedRun, []olympus.Warning, error) {
			return withSession(ctx, ol, in.Target, func(s *olympus.Session) (startedRun, error) {
				job, err := s.Start(ctx, in.Command)
				if err != nil {
					return startedRun{}, err
				}
				return startedRun{CommandID: job.ID()}, nil
			})
		})

	addTool(s, "poll_run", "Ask whether a detached run has finished. Branch on status first: exit_code is present only when completed.",
		func(ctx context.Context, ol *olympus.Olympus, in pollParams) (olympus.PollResult, []olympus.Warning, error) {
			session, err := ol.Open(ctx, in.Target)
			if err != nil {
				if backend.CodeOf(err) == backend.CodeSessionNotFound {
					// Poll answers about the COMMAND, never about the backend:
					// a target that never existed and one that vanished are
					// indistinguishable from here (behavior §6.8).
					return olympus.PollResult{State: "died", Reason: "the session is no longer present"}, nil, nil
				}
				return olympus.PollResult{}, nil, err
			}
			var opts []olympus.RunOption
			if in.Lines > 0 {
				opts = append(opts, olympus.PollWindow(in.Lines))
			}
			result, err := session.Poll(ctx, in.ID, opts...)
			return result, result.Warnings, err
		})

	addTool(s, "exit_status", "Read a caller-supplied completion marker off the screen. The marker is always yours to choose; there is no default.",
		func(ctx context.Context, ol *olympus.Olympus, in markerParams) (markerResult, []olympus.Warning, error) {
			return withSession(ctx, ol, in.Target, func(s *olympus.Session) (markerResult, error) {
				code, found, err := s.ExitStatus(ctx, in.Marker, in.Lines)
				if err != nil || !found {
					return markerResult{Found: false}, err
				}
				return markerResult{Found: true, ExitCode: &code}, nil
			})
		})

	addTool(s, "create_view", "Create an independently-scrollable view onto a session. Not every backend has this concept.",
		func(ctx context.Context, ol *olympus.Olympus, in viewParams) (backend.View, []olympus.Warning, error) {
			var opts []olympus.ViewOption
			if in.NoMouse {
				opts = append(opts, olympus.WithoutMouse())
			}
			view, err := ol.CreateView(ctx, in.Base, opts...)
			return view, nil, err
		})

	addTool(s, "scroll_view", "Scroll a view back into its history, leaving its base untouched.",
		func(ctx context.Context, ol *olympus.Olympus, in scrollParams) (acknowledged, []olympus.Warning, error) {
			return acknowledged{Target: in.View}, nil, ol.ScrollView(ctx, in.View, in.Lines)
		})

	addTool(s, "list_views", "List views, optionally for one base session.",
		func(ctx context.Context, ol *olympus.Olympus, in listViewParams) ([]backend.View, []olympus.Warning, error) {
			views, err := ol.Views(ctx, in.Base)
			if views == nil {
				views = []backend.View{}
			}
			return views, nil, err
		})

	addTool(s, "server_env", "Read a key from the multiplexer server's global environment. An unset key is a real answer, not an error.",
		func(ctx context.Context, ol *olympus.Olympus, in envParams) (envResult, []olympus.Warning, error) {
			value, present, err := ol.ServerEnv(ctx, in.Key)
			if err != nil {
				return envResult{}, nil, err
			}
			return envResult{Key: in.Key, Present: present, Value: value}, nil, nil
		})

	addTool(s, "doctor", "Report what is installed, which backend resolves and why, where sessions live, and what each backend can do.",
		func(ctx context.Context, ol *olympus.Olympus, _ emptyParams) (olympus.Diagnosis, []olympus.Warning, error) {
			return olympus.Diagnose(ctx), nil, nil
		})

	// A version tool must exist so a consumer can floor-check without shelling
	// out, and it reports the same literal the server identity carries
	// (behavior §15.6).
	addTool(s, "version", "Report the Olympus version.",
		func(ctx context.Context, ol *olympus.Olympus, _ emptyParams) (versionResult, []olympus.Warning, error) {
			return versionResult{Version: olympus.Version}, nil, nil
		})
}

// withSession opens a handle without creating anything, so no tool can bring a
// session into being by being asked about it.
func withSession[Out any](ctx context.Context, ol *olympus.Olympus, target string, fn func(*olympus.Session) (Out, error)) (Out, []olympus.Warning, error) {
	var zero Out
	session, err := ol.Open(ctx, target)
	if err != nil {
		return zero, nil, err
	}
	out, err := fn(session)
	return out, nil, err
}
