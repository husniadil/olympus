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
	"session_status",
	"self",
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

type stopParams struct {
	Target string `json:"target" jsonschema:"the session to end"`
	Force  bool   `json:"force,omitempty" jsonschema:"skip the graceful attempt entirely"`
	// Presses and InterruptSeconds shape the graceful attempt. The timeout
	// bounds the POLL phase only, so total wall time is presses*gap plus it.
	Presses          int `json:"presses,omitempty" jsonschema:"interrupts to send before waiting (default 1)"`
	InterruptSeconds int `json:"interrupt_timeout_seconds,omitempty" jsonschema:"how long to wait for the interrupt before forcing (default 2)"`
}

type startParams struct {
	Name    string   `json:"name" jsonschema:"the session name"`
	Dir     string   `json:"dir,omitempty" jsonschema:"working directory for the session"`
	Cols    int      `json:"cols,omitempty" jsonschema:"initial width; ignored by backends with no spawn-time sizing"`
	Rows    int      `json:"rows,omitempty" jsonschema:"initial height; ignored by backends with no spawn-time sizing"`
	Command []string `json:"command,omitempty" jsonschema:"argv to spawn instead of a login shell; executed, never typed"`
	// KeepCorpse applies on the create path only and is unsupported on a
	// backend with no corpse concept, which rejects it before doing anything.
	KeepCorpse bool `json:"keep_corpse,omitempty" jsonschema:"leave a dead session to inspect after its command exits"`
}

// sessionOptions turns the shared create parameters into options, so
// start_session and new_session cannot drift apart.
func sessionOptions(in startParams) []olympus.SessionOption {
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
	if in.KeepCorpse {
		opts = append(opts, olympus.KeepCorpse())
	}
	return opts
}

type textParams struct {
	Target string `json:"target" jsonschema:"the session to address"`
	Text   string `json:"text" jsonschema:"the text to deliver"`
	// Submit applies to type_text: an unverified terminator afterwards.
	Submit bool `json:"submit,omitempty" jsonschema:"send the terminator afterwards, without verifying the text landed"`
	// NoSubmit applies to send_text: verify without submitting.
	NoSubmit bool `json:"no_submit,omitempty" jsonschema:"confirm the text landed but leave it unsubmitted"`
	// Atomic applies to send_text: deliver and submit as one unit, skipping the
	// on-screen check. It cannot be combined with verification.
	Atomic bool `json:"atomic,omitempty" jsonschema:"deliver and submit as one unit, without verifying; single-line only"`
	// Seconds applies to send_text: one attempt's verify budget, spent twice.
	Seconds int `json:"timeout_seconds,omitempty" jsonschema:"per-attempt verify budget in seconds, spent twice (default 5)"`
	// Enter applies to paste_text: submit the final line afterwards.
	Enter bool `json:"enter,omitempty" jsonschema:"submit the final line afterwards, retrying the terminator once"`
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

type statusParams struct {
	Target  string `json:"target" jsonschema:"the session to address"`
	Set     string `json:"set,omitempty" jsonschema:"record this status on the session instead of reading it"`
	Wait    string `json:"wait,omitempty" jsonschema:"block until the session reports exactly this status"`
	Seconds int    `json:"seconds,omitempty" jsonschema:"how long to wait, in seconds"`
}

// A statusResult is what the status tool answers with, in every mode, so a
// caller does not need three shapes for one tool.
type statusResult struct {
	Session string `json:"session"`
	Status  string `json:"status"`
}

type listPaneParams struct {
	Target string `json:"target,omitempty" jsonschema:"limit to one session; omit for every pane on the backend"`
}

type commandParams struct {
	Target  string `json:"target,omitempty" jsonschema:"the session to run in; omit when throwaway is set"`
	Command string `json:"command" jsonschema:"the command to run; must be a single line"`
	Seconds int    `json:"timeout_seconds,omitempty" jsonschema:"how long to wait for the command, in seconds (default 60)"`
	// Throwaway runs in a session created for this run and killed afterwards.
	// It cannot be combined with starting a detached run: the session is gone
	// the moment the run returns, leaving nothing to poll.
	Throwaway bool `json:"throwaway,omitempty" jsonschema:"run in a throwaway session, killed afterwards; excludes target"`
}

type pollParams struct {
	Target string `json:"target" jsonschema:"the session the run was started in"`
	ID     string `json:"id,omitempty" jsonschema:"the id start_run returned"`
	// CommandID is the same value under the name start_run hands back.
	//
	// The asymmetry is historical: this tool has always taken `id` and
	// start_run has always returned `command_id`, and both are shipped, so
	// neither can be renamed (§7). A new name CAN be added, and the obvious
	// thing for a caller to try is the one they were just given — getting it
	// wrong costs a round trip and a schema error that reads like a broken tool.
	CommandID string `json:"command_id,omitempty" jsonschema:"the id start_run returned; the same thing as id, under the name start_run uses"`
	Lines     int    `json:"lines,omitempty" jsonschema:"scrollback window to search; ignored where scrollback is native"`
}

// runID resolves the two accepted spellings to one value, preferring the
// original so a caller that sends both is not surprised by which won.
func (p pollParams) runID() string {
	if p.ID != "" {
		return p.ID
	}
	return p.CommandID
}

type markerParams struct {
	Target string `json:"target" jsonschema:"the session to address"`
	Marker string `json:"marker" jsonschema:"the completion marker to look for, separator included — for the wrapper 'cmd; echo DONE:$?' the marker is 'DONE:', not 'DONE'; there is deliberately no default"`
	Lines  int    `json:"lines,omitempty" jsonschema:"scrollback window to search; ignored where scrollback is native"`
}

type viewParams struct {
	Base    string `json:"base" jsonschema:"the session to look onto"`
	Name    string `json:"name,omitempty" jsonschema:"view session name (default olympus-view-<base>-<nonce>)"`
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
			session, err := ol.Session(ctx, in.Name, sessionOptions(in)...)
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
		func(ctx context.Context, ol *olympus.Olympus, in stopParams) (olympus.Stopped, []olympus.Warning, error) {
			var opts []olympus.StopOption
			if in.Force {
				opts = append(opts, olympus.Force())
			}
			if in.Presses > 0 {
				opts = append(opts, olympus.Presses(in.Presses))
			}
			if in.InterruptSeconds > 0 {
				opts = append(opts, olympus.InterruptTimeout(time.Duration(in.InterruptSeconds)*time.Second))
			}
			stopped, err := ol.Stop(ctx, in.Target, opts...)
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
				if err := s.Type(ctx, in.Text); err != nil {
					return acknowledged{}, err
				}
				if in.Submit {
					return acknowledged{Target: s.Name()}, s.Submit(ctx)
				}
				return acknowledged{Target: s.Name()}, nil
			})
		})

	addTool(s, "send_text", "Deliver text, confirm it is on screen, and only then submit it.",
		func(ctx context.Context, ol *olympus.Olympus, in textParams) (acknowledged, []olympus.Warning, error) {
			return withSession(ctx, ol, in.Target, func(s *olympus.Session) (acknowledged, error) {
				if in.Atomic {
					// Verification and atomicity cannot be combined: a
					// verified send's terminator is a separate call, and any
					// cross-invocation retry re-types the text and doubles it.
					if in.NoSubmit {
						return acknowledged{}, backend.Errorf(backend.CodeUsage,
							"atomic and no_submit cannot be combined: an atomic send is defined by delivering the terminator with the text")
					}
					return acknowledged{Target: s.Name()}, s.SendAtomic(ctx, in.Text)
				}
				var opts []olympus.SendOption
				if in.NoSubmit {
					opts = append(opts, olympus.WithoutSubmit())
				}
				if in.Seconds > 0 {
					opts = append(opts, olympus.VerifyBudget(time.Duration(in.Seconds)*time.Second))
				}
				return acknowledged{Target: s.Name()}, s.Send(ctx, in.Text, opts...)
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
				if in.Enter {
					return acknowledged{Target: s.Name()}, s.PasteAndSubmit(ctx, in.Text)
				}
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
			session, err := ol.Create(ctx, in.Name, sessionOptions(in)...)
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

	addTool(s, "session_status", "Read, set, or wait for a session's status: an opaque label a process INSIDE a session leaves for whoever drives it from outside. It answers what a capture cannot — a program at a prompt and a program mid-work render identically. Olympus never interprets the value and defines no vocabulary of states. Not every backend can carry one; `capabilities` reports it as session_status.",
		func(ctx context.Context, ol *olympus.Olympus, in statusParams) (statusResult, []olympus.Warning, error) {
			session, err := ol.Open(ctx, in.Target)
			if err != nil {
				return statusResult{}, nil, err
			}
			out := statusResult{Session: in.Target}
			switch {
			case in.Set != "" && in.Wait != "":
				return statusResult{}, nil, olympus.ErrUsage
			case in.Set != "":
				if err := session.SetStatus(ctx, in.Set); err != nil {
					return statusResult{}, nil, err
				}
				out.Status = in.Set
			case in.Wait != "":
				var opts []olympus.WaitOption
				if in.Seconds > 0 {
					opts = append(opts, olympus.WaitTimeout(time.Duration(in.Seconds)*time.Second))
				}
				got, err := session.WaitForStatus(ctx, in.Wait, opts...)
				if err != nil {
					return statusResult{}, nil, err
				}
				out.Status = got
			default:
				got, err := session.Status(ctx)
				if err != nil {
					return statusResult{}, nil, err
				}
				out.Status = got
			}
			return out, nil, nil
		})

	addFreestandingTool(s, "self", "Report which session this MCP server is running inside, if any. Being outside one is an answer, not a failure. Nested sessions report no single address, because the environment cannot say which is inner.",
		func(ctx context.Context, _ emptyParams) (olympus.Identity, []olympus.Warning, error) {
			// Answers about this PROCESS, so the handle's configured backend
			// is deliberately not consulted: it describes what this server
			// would address, not where it is.
			here, err := olympus.Self(ctx)
			if err != nil && !here.Inside {
				return olympus.Identity{}, nil, err
			}
			return here, nil, nil
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
			if in.Throwaway {
				if in.Target != "" {
					return olympus.Result{}, nil, backend.Errorf(backend.CodeUsage,
						"a throwaway run creates its own session, so it takes no target")
				}
				result, warnings, err := ol.RunOnce(ctx, in.Command)
				return result, warnings, err
			}
			return withSession(ctx, ol, in.Target, func(s *olympus.Session) (olympus.Result, error) {
				return s.Exec(ctx, in.Command, opts...)
			})
		})

	addTool(s, "start_run", "Start a command and return an id to poll. Nothing is written down; the id is the whole handle.",
		func(ctx context.Context, ol *olympus.Olympus, in commandParams) (olympus.Started, []olympus.Warning, error) {
			if in.Throwaway {
				// A throwaway session is killed the moment the run returns,
				// leaving nothing to poll (behavior §6.10).
				return olympus.Started{}, nil, backend.Errorf(backend.CodeUsage,
					"a detached run needs a target: a throwaway session is killed when the run returns, leaving nothing to poll")
			}
			return withSession(ctx, ol, in.Target, func(s *olympus.Session) (olympus.Started, error) {
				job, err := s.Start(ctx, in.Command)
				if err != nil {
					return olympus.Started{}, err
				}
				return olympus.Started{CommandID: job.ID()}, nil
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
			id := in.runID()
			if id == "" {
				// Neither spelling given. Loosening the schema so either name
				// works means the schema can no longer require one, so the
				// requirement moves here rather than disappearing — a poll with
				// no id would otherwise scan the scrollback for a marker that
				// cannot exist and report died.
				return olympus.PollResult{}, nil, backend.Errorf(backend.CodeUsage,
					"polling needs the id start_run returned, as either id or command_id")
			}
			result, err := session.Poll(ctx, id, opts...)
			return result, result.Warnings, err
		})

	addTool(s, "exit_status", "Read a caller-supplied completion marker off the screen, for the wrapper pattern `cmd; echo DONE:$?`. The marker is the whole prefix, separator included — `DONE:`, not `DONE` — and the exit code is the token immediately after it; Olympus skips no separator of its own. The marker is always yours to choose; there is no default. An unmatched marker reports not-found, which legitimately means the command has not finished, so a wrong marker waits forever without erroring.",
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
			if in.Name != "" {
				opts = append(opts, olympus.WithViewName(in.Name))
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

	addFreestandingTool(s, "doctor", "Report what is installed, which backend resolves and why, where sessions live, and what each backend can do.",
		func(ctx context.Context, _ emptyParams) (olympus.Diagnosis, []olympus.Warning, error) {
			return olympus.Diagnose(ctx), nil, nil
		})

	// A version tool must exist so a consumer can floor-check without shelling
	// out, and it reports the same literal the server identity carries
	// (behavior §15.6).
	addFreestandingTool(s, "version", "Report the Olympus version.",
		func(ctx context.Context, _ emptyParams) (versionResult, []olympus.Warning, error) {
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
