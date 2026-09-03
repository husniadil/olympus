---
name: olympus
description: Drive real, persistent terminal sessions with the `olympus` CLI (or its MCP tools) when a task needs a TTY, shell state that survives between calls, a long-running process, an interactive program or REPL, or several terminals at once. Not for one-shot commands; the normal shell tool is faster for those.
---

# Olympus

`olympus` creates, drives, reads, and tears down terminal sessions on a multiplexer you already have (zmx by default, tmux, then meja, then herdr). A session outlives the call that made it: start one now, come back to it from another call, another process, or tomorrow.

Use it when the normal shell tool is the wrong shape:

- The program needs a real TTY or stays open (dev servers, watchers, REPLs, TUIs, ssh).
- State must persist across calls: a virtualenv activated, a directory entered, a login done.
- The work runs longer than one call should block, and you want to poll or read its screen later.
- Several things must run at once and be read together.

Otherwise run the command directly.

## Rules that hold everywhere

- Run `olympus doctor` once per task before anything else. It never fails; it says which backend answers, where sessions live, and what that backend cannot do. Do not assume tmux behavior on zmx or the reverse.
- Add `--json` whenever you will parse the result. Human output may change; the JSON envelope will not. A command's failure is in `data.exit_code`, and the process exits 0 under `--json`.
- `start <name>` is create-or-reuse. There is no separate "does it exist" step to get wrong. Give sessions short, purposeful names (`build`, `api`, `repl`), not generated ids.
- `run` is for shell commands: it wraps the command with start and end markers, so it needs a shell at the prompt. Pointed at a REPL or a full-screen program, it times out. Drive those with `send`, `press`, `wait`, and read them with `screen`.
- `type` places text and never submits. `send` confirms the text landed on screen, then submits. Prefer `send`; use `type` only when you need the text left unsubmitted, or `send --no-enter`.
- `wait` matches per line against the screen. Match something the program itself prints, never your prompt: `'\$\s*$'` is your prompt and fails under zsh, fish, and themed prompts. Do not require a trailing space: `^>>>\s*$`, not `^>>> $`.
- Degraded operations warn, they do not fail. Read stderr (or `warnings` in the envelope) so a narrower result is not mistaken for a full one.
- Control keys (`c-c`, `c-o`, `c-x`) are not deliverable on zmx. Check `olympus capabilities` for `control_keys` before driving an editor or anything that needs them; use `--backend tmux` if it does.
- Starting a session ON a command (`--command`) is refused on herdr: its panes run the shell its own configuration names. Check `olympus capabilities` for `spawn_command`; without it, start a plain session and run the program inside it.
- On herdr every pane is a session, named by its pane label or, far more often, by its pane id (`w25:p8`). To drive a herdr you already run, point `--socket-path` at its socket (`~/.config/herdr/herdr.sock` by default); `olympus ls` then shows every pane on it. Olympus will not stop a server it did not start — close the sessions you own instead.
- Servers are the level above sessions. `olympus servers` lists them (tmux socket names, herdr named sessions, zmx's one directory) and `--server <name>` points any verb at one by name; on meja neither exists. `olympus servers stop <name>` takes every session on it down, so use it only when the user means the whole server.
- Stop what you started. `olympus stop <name>` is graceful first, `--force` skips that. Leave a session only when the user wants it to persist.

Exit codes worth recognizing: 3 session does not exist, 4 backend unreachable, 5 timed out, 6 someone else holds the session, 7 backend has no such concept. `run` without `--json` exits with the command's own status so it composes in a pipeline.

## Playbooks

### Run a command and get its output

```sh
olympus start build --dir /path/to/repo
olympus run build 'go test ./...' --json --timeout 5m
```

`data.exit_code` and `data.output` carry the result. No target (`olympus run 'cmd'`) uses a throwaway session that is cleaned up afterwards.

### Long-running command, checked later

```sh
olympus run build 'make deploy' --detach      # returns a run id
olympus poll build <id> --json                # pending / completed / died, exit code, output so far
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

### Several sessions at once

```sh
olympus start api --dir ./api ; olympus start web --dir ./web
olympus send api 'npm run dev'  ; olympus send web 'npm run dev'
olympus wait api 'listening on' ; olympus wait web 'ready in'
olympus screen api web          # both screens in one call
olympus panes --json            # every pane across every session
```

### Coordinate with a process running inside a session

From inside: `olympus self` says which session this process is in, and `olympus status --set ready` records a status on it. From outside: `olympus status build --wait ready --timeout 2m` blocks until it says so. Use this instead of pattern-matching on output when you control both sides.

### Hand the terminal to the user

`olympus attach <name>` gives the user the live session. Do not call it from automation; it needs a terminal and returns the multiplexer client's exit code, not the session's.

## MCP

If `olympus mcp` is configured as an MCP server, the tools are the same operations under the same names with underscores. All 27 of them:

- Sessions: `start_session`, `new_session`, `list_sessions`, `session_info`, `session_status`, `stop_session`, `self`, `list_panes`
- Input: `type_text`, `send_text`, `press_keys`, `paste_text`
- Reading: `screen`, `wait_for`
- Running: `run_command`, `start_run`, `poll_run`, `exit_status`
- Views: `create_view`, `scroll_view`, `list_views`
- Servers: `list_servers`, `stop_server` (select one by name with `OLYMPUS_SERVER`)
- Diagnostics: `server_env`, `capabilities`, `doctor`, `version`

Two operations are CLI-only by nature: `watch` (a stream) and `attach` (interactive). Everything in this skill applies unchanged; only the spelling differs.

## Choosing

- Command with a shell at the prompt, want its exit code: `run`.
- Command that outlives the call: `run --detach` then `poll`.
- Program that expects keystrokes: `send` / `press` / `wait` / `screen`.
- Need to see rather than wait: `screen`, with `--history N` for scrollback.
- Two processes that should signal each other: `status --set` / `status --wait`.
- Want the user to take over: `attach`, and say so rather than doing it for them.
