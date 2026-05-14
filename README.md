# cc-proxy

`cc-proxy` is a Go reimplementation of
[raine/claude-code-proxy](https://github.com/raine/claude-code-proxy), an
MIT-licensed proxy that lets Claude Code talk to alternate upstream providers
through an Anthropic-compatible local HTTP surface.

Canonical public repository: <https://github.com/lobo235/cc-proxy>

The upstream project by Raine Virta did the original design and implementation
work. This repo exists to rebuild that behavior in Go while preserving clear
attribution and MIT license compliance. See [NOTICE](NOTICE).

## Current Status

Early public implementation. Implemented so far:

- CLI with `serve`, `--version`, `auth status`, and `auth logout`.
- Codex device auth via `cc-proxy codex auth device`.
- Config loading from env and `config.json`.
- Model/provider routing rules.
- HTTP routes for `/healthz`, `/status`, `/v1/messages`, and
  `/v1/messages/count_tokens`.
- Provider auth file storage compatible with the upstream Linux paths.
- Codex request translation for text, system prompts, tools, tool calls, and
  tool results.
- Codex `/v1/messages/count_tokens` support using the upstream input-token
  endpoint when available, with a local estimate fallback.
- Initial Codex text and function-call stream translation path for
  `/v1/messages`.
- Kimi routes and richer Codex streaming behavior are still in progress.
- Behavior spec in [docs/behavior-spec.md](docs/behavior-spec.md).

## Development

```bash
make test
make build
bin/cc-proxy --version
bin/cc-proxy codex auth device
bin/cc-proxy codex auth status
bin/cc-proxy serve
curl http://127.0.0.1:18765/status
```

Default listen address is `127.0.0.1:18765`.

To smoke-test Claude Code against Codex `gpt-5.5` through the proxy:

```bash
ANTHROPIC_BASE_URL=http://127.0.0.1:18765 \
ANTHROPIC_API_KEY=cc-proxy \
claude --bare -p --model gpt-5.5 --effort max "Reply with exactly: proxy-ok"
```

Reasoning effort values `none`, `minimal`, `low`, `medium`, `high`, and
`xhigh` are forwarded to Codex. Claude Code's `max` effort is translated to
Codex `xhigh`.

Claude Code may internally spawn subagents with Anthropic model names such as
`claude-sonnet-4-6`. Within a `claude-gpt` session, those alias requests inherit
the explicit Codex model already used by the session, so subagents continue to
run against OpenAI even if Claude Code labels the background agent as Sonnet in
its UI.

For daily use, `scripts/claude-gpt` starts `cc-proxy` in the background when
needed and then forwards all arguments to `claude`. It defaults to
`--model gpt-5.5[200k]` unless you pass your own `--model`. The `[200k]`
suffix is accepted by Claude Code and tells it to use a conservative context
window for compaction decisions; the proxy strips that suffix before sending
the model to Codex. Earlier versions used `[1m]`, but that can let long Claude
Code sessions grow past the Codex backend's actual context window before
compaction runs.

`scripts/claude-gpt` preserves the normal Claude Code harness by default, so
your configured MCP servers, skills, agents, core slash commands, project
memory, and default tools are available.

The launcher also applies the upstream project recommendations for Claude Code:

- `ANTHROPIC_SMALL_FAST_MODEL=gpt-5.4-mini[200k]` unless already set.
- `CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1` unless already set.
- `CLAUDE_CODE_DISABLE_NONSTREAMING_FALLBACK=1` unless already set.

Override launcher defaults with environment variables:

```bash
CLAUDE_GPT_TOOLS=default scripts/claude-gpt ...
CLAUDE_GPT_TOOLS= scripts/claude-gpt --tools default ...
CLAUDE_GPT_PROXY_VERBOSE=0 scripts/claude-gpt ...
```

Core slash commands such as `/clear`, `/compact`, and `/exit` stay enabled by
default. To disable slash commands for a one-off minimal print-mode request:

```bash
CLAUDE_GPT_DISABLE_SLASH_COMMANDS=1 scripts/claude-gpt -p ...
```

For a one-off small-context session with no ambient MCP/user/project harness,
enable lean mode:

```bash
CLAUDE_GPT_LEAN=1 scripts/claude-gpt ...
```

Lean mode adds `--bare`, `--strict-mcp-config`, and a compact built-in tool set
of `Bash,Edit,MultiEdit,Read,Write,Grep,Glob,LS,TodoWrite`. You can also
control those flags independently with `CLAUDE_GPT_BARE=1`,
`CLAUDE_GPT_STRICT_MCP_CONFIG=1`, and `CLAUDE_GPT_TOOLS=...`.

```bash
scripts/claude-gpt -p --effort max "Reply with exactly: proxy-ok"
```

## Logs and Troubleshooting

When launched through `scripts/claude-gpt`, the proxy is managed by
`scripts/cc-proxy-ensure`. Runtime logs are written as JSON lines to:

- proxy log: `${XDG_STATE_HOME:-$HOME/.local/state}/claude-code-proxy/proxy.log`
- launcher log:
  `${XDG_STATE_HOME:-$HOME/.local/state}/claude-code-proxy/launcher.log`

Useful commands:

```bash
scripts/cc-proxy-ensure status
scripts/cc-proxy-ensure log
scripts/cc-proxy-ensure tail
scripts/cc-proxy-ensure restart
```

Default logging records request routing, HTTP status, upstream Codex status,
token usage, and provider errors with request IDs. Usage logs include Codex
input, cached input, output, reasoning, and total token counts when the upstream
response provides them.

Set `CCP_LOG_VERBOSE=1` before starting the proxy to add redacted request-shape
and translation summaries, selected upstream response headers, and
`count_tokens` fallback details for contract debugging. Verbose request-shape
logs split request size into message bytes, tool-schema bytes, system-prompt
bytes, MCP tool count, and a bounded sample of tool names. That is the first
place to check when Claude Code sends unexpectedly large prompts.

`scripts/claude-gpt` starts a stopped managed proxy with verbose logging enabled
by default so these diagnostics are available during normal proxy testing. Set
`CLAUDE_GPT_PROXY_VERBOSE=0` if you want the launcher to start the proxy with
normal logging instead. If the proxy is already running, restart it with
`CCP_LOG_VERBOSE=1 scripts/cc-proxy-ensure restart` to turn verbose logging on.

### Context Size

Claude Code sends the active conversation plus the currently available tool
schemas to the configured Anthropic-compatible endpoint. When your normal MCP
servers, skills, agents, and project tooling are loaded, those schemas can be a
large repeated prefix. `cc-proxy` preserves that harness by default because those
tools are part of the value of Claude Code.

The proxy forwards translated Codex requests statelessly (`store=false`) with a
stable `prompt_cache_key`. That allows OpenAI prompt caching to help with
repeated prefixes, but it still means the full translated input is sent on each
turn. The Codex backend currently rejects stored Responses API requests with
`Store must be set to false`, so `cc-proxy` does not expose a
`previous_response_id`/stored-response mode.

By default, the Codex `prompt_cache_key` is the Claude Code session id. Set
`CCP_CODEX_CACHE_KEY_STRATEGY=stable` to derive the cache key from the upstream
model instead, which lets repeated fresh `claude-gpt -p` invocations share a
backend cache shard when their prefix bytes match:

```bash
CCP_LOG_VERBOSE=1 CCP_CODEX_CACHE_KEY_STRATEGY=stable scripts/cc-proxy-ensure restart
```

For launcher-driven startup, `CLAUDE_GPT_CACHE_KEY_STRATEGY=stable` passes the
same setting through when `scripts/claude-gpt` starts a stopped proxy. If the
proxy is already running, restart it with the `CCP_CODEX_CACHE_KEY_STRATEGY`
environment variable so the running process picks up the strategy.

`/v1/messages/count_tokens` attempts the Codex input-token endpoint derived from
the configured responses URL by appending `/input_tokens`. Override it with
`CCP_CODEX_INPUT_TOKENS_URL` if the backend exposes token counting at a
different path. If the upstream endpoint is missing or rejects the request, the
proxy falls back to its local estimator and logs `source=estimate`.

## Attribution

This project is based on the MIT-licensed `raine/claude-code-proxy` project at
upstream commit `d5509c5` (`0.0.12`). The original copyright notice and MIT
license text are preserved in [NOTICE](NOTICE).
