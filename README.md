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
- Codex `/v1/messages/count_tokens` support with a local estimate.
- Initial Codex text-stream translation path for `/v1/messages`.
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

For daily use, `scripts/claude-gpt` starts `cc-proxy` in the background when
needed and then forwards all arguments to `claude`. It defaults to
`--model gpt-5.5` unless you pass your own `--model`.

```bash
scripts/claude-gpt --bare -p --effort max "Reply with exactly: proxy-ok"
```

## Attribution

This project is based on the MIT-licensed `raine/claude-code-proxy` project at
upstream commit `d5509c5` (`0.0.12`). The original copyright notice and MIT
license text are preserved in [NOTICE](NOTICE).
