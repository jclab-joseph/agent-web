# agent-web

A web UI for running Claude Code and Codex sessions on your machine, built on
[clicodebridge](https://github.com/jclab-joseph/clicodebridge). It runs the real
interactive CLI on a pseudo-terminal, so plugins, skills, slash commands, Remote Control
and the rest keep working.

- **Persistent sessions.** Sessions, their history, and the images and files the agent
  shows are stored in SQLite. They survive server restarts.
- **Idle stop and resume.** A CLI that has been idle for 5 minutes (`--idle-timeout`) is
  stopped. The next message, command or **Resume** click relaunches it with `--resume`, so
  the conversation continues where it left off. A turn in progress or an unanswered
  prompt is never stopped.
- **Working directories.** Pick a directory you used before, or type a new path with
  directory autocompletion. You can create the directory if it does not exist.
- Chat with Markdown rendering, tool calls and results, thinking, and attachments.
- Permission prompts and questions appear as cards.
- Model, effort and mode can be changed at any time. While the CLI is stopped, the change
  is saved and applied at the next start.
- A live **Terminal** tab (xterm.js) shows the CLI's own screen and accepts typing.
- The version comes from the git tag. It is shown by `agent-web --version` and at the
  bottom of the sidebar.

## Install

Download an archive for your platform from the
[releases](https://github.com/jclab-joseph/agent-web/releases). Builds are available for
`linux_amd64`, `linux_arm64`, `darwin_amd64` and `darwin_arm64`. The binary embeds the
web UI.

```sh
tar xzf agent-web_v0.1.0_linux_amd64.tar.gz
./agent-web_v0.1.0_linux_amd64/agent-web --version
```

On macOS, remove the download quarantine first:
`xattr -d com.apple.quarantine ./agent-web`.

`claude` (and optionally `codex`) must be on `PATH` and logged in.

## Run

```sh
agent-web                                  # http://127.0.0.1:8787
agent-web --addr 0.0.0.0:8787 --token s3cret
```

| Flag | Env | Default | |
| --- | --- | --- | --- |
| `--addr` | `AGENT_WEB_ADDR` | `127.0.0.1:8787` | Listen address |
| `--token` | `AGENT_WEB_TOKEN` | | Access token. The UI asks for it once, then keeps an HttpOnly cookie. |
| `--data-dir` | `AGENT_WEB_DATA_DIR` | `~/.config/agent-web` (Linux), `~/Library/Application Support/agent-web` (macOS) | Holds `agent-web.db` |
| `--idle-timeout` | | `5m` | Stop an idle CLI after this long. `0` never stops it. |
| `--claude`, `--codex` | | `claude`, `codex` | CLI executables |
| `--drift-dir` | | | Save screens where a Claude recognizer disagrees with the CLI |
| `--version` | | | Print the version and exit |

**Security.** Sessions run with your user's permissions, so agent-web refuses to listen
on a non-loopback address without a token. Without a token it also rejects requests
whose `Host` is not a loopback name, which blocks DNS rebinding. It rejects every
cross-origin request that changes state. Files the agent sends are served with a
sandboxing Content-Security-Policy. Use TLS (a reverse proxy) when you expose it beyond
your machine.

## Development

Requirements: Go 1.26+, Node 22.21+ or 24, pnpm 10.

```sh
make            # go generate + pnpm gen:api, build the UI, build bin/agent-web
make test       # go vet, go test -race, vue type-check

make dev        # Go server on :8787 (data in ./.data)
cd frontend && pnpm dev   # UI with hot reload on :3000, proxies /api to :8787
```

### API and generated code

The API is written code-first with [Huma](https://huma.rocks). The TypeScript client is
generated from it:

```
internal/server/*.go ──go generate──▶ openapi/openapi.yaml ──pnpm gen:api──▶ frontend/app/api/*
                      (cmd/openapi-gen)                      (@hey-api/openapi-ts)
```

After you change an endpoint or a type in `internal/model`, run `make generate` and
commit the generated files. The release workflow fails if they are out of date. The
running server also serves the document at `/api/openapi.yaml` and interactive docs at
`/api/docs`.

The SSE endpoints (`streamEvents`, `streamTerminal`) also go through the generated
client. It reconnects and resumes after the last event `seq`.

### Layout

```
cmd/agent-web/        main: flags, server, shutdown
cmd/openapi-gen/      writes openapi/openapi.yaml (go generate)
internal/manager/     sessions: start/resume, idle watchdog, event recording, subscriptions
internal/store/       SQLite (modernc.org/sqlite, no cgo): sessions, events, attachments, workdirs
internal/model/       API types (also the stored event format)
internal/server/      Huma handlers, SSE, auth and origin checks, SPA serving
internal/webui/       embeds the built UI (dist/ is filled by `make frontend`)
internal/version/     version info set with -ldflags
frontend/             Nuxt 4 SPA (TypeScript, pnpm)
```

### How a session's lifecycle works

- **Create:** the session row is stored, the CLI starts with a new conversation id, and
  the directory is recorded under recent working directories.
- **Events:** the CLI's events are stored with an increasing `seq` and streamed to
  browsers. Lifecycle events (`started`, `resumed`, `suspended`, `stopped`, `shutdown`)
  mark the CLI's own starts and stops in the history.
- **Idle:** a watchdog stops a CLI whose state has been `idle` for the timeout while no
  request was in progress. A `busy` or `waiting` CLI counts as active.
- **Resume:** the next action that needs the CLI (send, command, start) relaunches it
  with `--resume <cli session id>` and the stored model, effort, mode and options. A
  session that never got a prompt has nothing to resume, so it gets a fresh conversation.
- **Server restart:** every running CLI is stopped (`shutdown`), and every session comes
  back as stopped and resumable.

## Release

Push a tag. `.github/workflows/release.yml` then builds the UI once, cross-compiles the
four targets with `CGO_ENABLED=0`, and attaches the archives and `checksums.txt` to a
GitHub release:

```sh
git tag v0.1.0
git push origin v0.1.0
```

## Known issues

- With **Trust the workspace automatically** on, Claude Code occasionally exits during
  the very first start in a new directory: the trust dialog is answered with "No, exit".
  Seen 2 of about 10 times with Claude Code 2.1.311. The cause is in clicodebridge's
  automatic trust answer and has not been pinned down yet. The session shows the CLI's
  last screen. **Start CLI** usually succeeds, because the trust dialog is answered again
  on the next start. Leaving the option off shows the trust prompt as a card instead.
