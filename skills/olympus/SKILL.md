---
name: olympus
description: Drive real, persistent terminal sessions with the `olympus` CLI (or its MCP tools) when a task needs a TTY, shell state that survives between calls, a long-running process, an interactive program or REPL, or several terminals at once. Not for one-shot commands; the normal shell tool is faster for those.
---

# Olympus

`olympus` creates, drives, reads and tears down terminal sessions on a multiplexer you already have: zmx by default, then tmux, meja and herdr. A session outlives the call that made it. Start one now, and come back to it from another call, another process, or tomorrow.

Use it when the normal shell tool is the wrong shape:

- The program needs a real TTY or stays open: dev servers, watchers, REPLs, TUIs, ssh.
- State must persist across calls: a virtualenv, a directory, a login.
- The work runs longer than one call should block, and you want to poll or read its screen later.
- Several things must run at once and be read together.

Otherwise run the command directly.

## Rules that hold everywhere

### Start with doctor

- Run `olympus doctor` once per task before anything else. It never fails.
- It says which backend answers, where sessions live, and what that backend cannot do.
- Do not assume tmux behavior on zmx, or the reverse.

### Parse JSON, not prose

- Add `--json` whenever you will parse the result. Human output may change; the envelope will not.
- Under `--json`, `run` exits 0 and the command's status is in `data.exit_code`.
- Degraded operations warn instead of failing. Read stderr, or `warnings` in the envelope.

### Sessions

- `start <name>` is create-or-reuse. There is no separate "does it exist" step.
- Use short, purposeful names (`build`, `api`, `repl`), not generated ids.
- Stop what you started: `olympus stop <name>` interrupts first, `--force` skips that. Leave a session only when the user wants it to persist.

### run needs a shell

- `run` wraps the command with start and end markers, so it needs a shell at the prompt.
- Pointed at a REPL or a full-screen program, it times out. Use `send`, `press`, `wait` and `screen` there.

### Typing and waiting

- `type` places text and never submits. `send` confirms the text landed, then submits.
- Prefer `send`. For text left unsubmitted, use `send --no-enter`.
- `wait` matches per line. Match something the program prints, never your prompt: `'\$\s*$'` fails under zsh, fish and themed prompts.
- Do not require a trailing space: `^>>>\s*$`, not `^>>> $`.

### Backend limits

- **Control keys** (`c-c`, `c-o`, `c-x`) do not arrive reliably on zmx. Check `olympus capabilities` for `control_keys` before driving an editor; use `--backend tmux` if it is false.
- **Starting on a command** (`start <name> -- <command>`) is refused on herdr, whose panes run the shell its configuration names. Check `spawn_command`; without it, start a plain session and run the program inside.

### herdr

- A session is a workspace, named by its label or by its id (`w25`) when the label is empty. A window is a tab, and a pane is a pane.
- A verb aimed at a workspace acts on the pane it shows; `w25:p8` reaches exactly that pane.
- `olympus stop` closes the level you named, with everything in it.
- To drive a herdr you already run, point `--socket-path` at its socket (`~/.config/herdr/herdr.sock` by default) or `--server <name>` at a named session. `olympus ls` then lists its workspaces, and `olympus panes <workspace>` their panes.

### Servers

- `olympus servers` lists the level above sessions: tmux socket names, herdr named sessions, zmx's one directory. meja has none.
- `--server <name>` points any verb at one.
- `olympus servers stop <name>` takes every session on it down. Use it only when the user means the whole server.
- `olympus servers start [name]` brings one up without creating anything. After a reboot this is how a herdr server comes back with the panes it was running; no other verb boots one.

### Agents

- `olympus agents` lists coding agents in panes, on every backend, with `status`:
  - `working`, `idle`
  - `blocked`: waiting on a person, such as a permission prompt or a question. Act on it.
  - `unknown`: no evidence. Read the pane's `screen` yourself.
- `detected_by` says how a row was found:
  - `herdr`: herdr's own detection, with native `status`, `title` and `usage`.
  - `command`: a known agent's name in the pane's process tree, or its foreground command where the pane has no `pid`. Status is read off a capture.
- `status_source` is `native` or `screen`. `--last` also reads the line each agent last said.
- `olympus kinds` lists the agent names and the executables and package directories that identify each. It needs no backend. Ask it rather than hardcoding the set.

### Exit codes

3 session does not exist, 4 backend unreachable, 5 timed out, 6 someone else holds the session, 7 backend has no such concept. `run` without `--json` exits with the command's own status.

## Playbooks

### Run a command and get its output

```sh
olympus start build --dir /path/to/repo
olympus run build 'go test ./...' --json --timeout 5m
```

`data.exit_code` and `data.output` carry the result. With no target (`olympus run 'cmd'`) the command runs in a throwaway session removed afterwards.

### Long-running command, checked later

```sh
olympus run build 'make deploy' --detach      # returns a run id
olympus poll build <id> --json                # pending / completed / died, exit code, output
olympus screen build --history 200            # the raw terminal, if poll is not enough
```

### Drive a REPL or interactive program

```sh
olympus start repl
olympus send repl 'python3'
olympus wait repl '^>>>\s*$' --timeout 30s
olympus send repl 'import sys; print(sys.version)'
olympus wait repl '^\d+\.\d+'
olympus screen repl
olympus paste repl "$(cat snippet.py)" --enter   # multi-line, one submit at the end
olympus press repl enter            # key names are lowercase: enter, escape, c-c, up
```

Read with `screen` before and after sending when the program's state is unclear. `wait` is for the moment the state changes; `screen` is for seeing what it is.

`send` into a coding agent that is waiting on a person (a permission prompt, a question) fails with `AGENT_BLOCKED`, exit 8, and types nothing. So does a send into an agent that has something open over its input box, such as Claude Code's rewind list or model picker. Read the prompt with `screen`, and answer or close it with `press` only when that is what you mean to do. An `AGENT_BLOCKED` marked `typed` is the exception: the prompt opened after the text was typed, so the text may still be in the input box, and sending again would type it twice.

### Several sessions at once

```sh
olympus start api --dir ./api ; olympus start web --dir ./web
olympus send api 'npm run dev'  ; olympus send web 'npm run dev'
olympus wait api 'listening on' ; olympus wait web 'ready in'
olympus screen api web          # both screens in one call
olympus panes --json            # every pane across every session
```

### Coordinate with a process inside a session

- Inside: `olympus self` says which session this process is in, and `olympus status --set ready` records a status.
- Outside: `olympus status build --wait ready --timeout 2m` blocks until it says so.

Use this instead of matching output when you control both sides.

### Hand the terminal to the user

- `olympus attach <name>` gives the user the live session. Do not call it from automation: it needs a terminal and returns the multiplexer client's exit code.
- On tmux, `attach <name>:<window> --bare` shows one window as a plain pane through a throwaway view, without moving other clients. `view create <name> --window <w>` makes the same view and keeps it.
- On a herdr server that advertises `client_view_focus`, `attach <target> --bare --client-tag <tag>` names the client. `olympus clients --tag <tag> --json` then reports the workspace (`session_id`), tab (`window_id`) and pane (`pane_id`) it shows, including a pane the user focused with the client's own keys.
- `olympus clients` lists every client. Elsewhere it exits 7.

## MCP

If `olympus mcp` is configured as an MCP server, the tools are the same operations. All 34:

| Group | Tools |
|---|---|
| Sessions | `start_session`, `new_session`, `list_sessions`, `session_info`, `session_status`, `focus_session`, `rename_session`, `stop_session`, `self`, `list_panes` |
| Input | `type_text`, `send_text`, `press_keys`, `paste_text` |
| Reading | `screen`, `wait_for` |
| Running | `run_command`, `start_run`, `poll_run`, `exit_status` |
| Views | `create_view`, `scroll_view`, `focus_view`, `list_views` |
| Servers | `list_servers`, `start_server`, `stop_server`, `list_clients` |
| Agents | `list_agents`, `list_kinds` |
| Diagnostics | `server_env`, `capabilities`, `doctor`, `version` |

- Select a server by name with `OLYMPUS_SERVER` in the server's environment.
- `watch` (a stream) and `attach` (interactive) are CLI-only.

Everything above applies unchanged; only the spelling differs.

## Choosing

- Command with a shell at the prompt, want its exit code: `run`.
- Command that outlives the call: `run --detach`, then `poll`.
- Program that expects keystrokes: `send`, `press`, `wait`, `screen`.
- Need to see rather than wait: `screen`, with `--history N` for scrollback.
- Two processes that signal each other: `status --set` and `status --wait`.
- The user should take over: `attach`, and say so rather than doing it for them.
