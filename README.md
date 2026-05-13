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

Early scaffold. Implemented so far:

- CLI skeleton with `serve`, `--version`, and provider auth command shape.
- Config loading from env and `config.json`.
- Model/provider routing rules.
- HTTP routes for `/healthz`, `/v1/messages`, and `/v1/messages/count_tokens`.
- Provider interface and not-yet-implemented Codex/Kimi provider stubs.
- Behavior spec in [docs/behavior-spec.md](docs/behavior-spec.md).

## Development

```bash
make test
make build
bin/cc-proxy --version
bin/cc-proxy serve
```

Default listen address is `127.0.0.1:18765`.

## Attribution

This project is based on the MIT-licensed `raine/claude-code-proxy` project at
upstream commit `d5509c5` (`0.0.12`). The original copyright notice and MIT
license text are preserved in [NOTICE](NOTICE).
