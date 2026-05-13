# Context

`cc-proxy` is a Go reimplementation of Raine Virta's MIT-licensed
`claude-code-proxy`. It presents a local Anthropic Messages-compatible surface
for Claude Code and routes each request to a configured upstream provider such
as Codex or Kimi.

## Domain Terms

- **Proxy**: the local HTTP service Claude Code talks to.
- **Provider**: an upstream account/API family the proxy can call, currently
  Codex or Kimi.
- **Model alias**: an incoming Claude-style model name that resolves to a
  provider-specific model.
- **Provider auth record**: the stored access/refresh token record for one
  provider.
- **Claude Code session**: the client session identified by
  `x-claude-code-session-id`, used for provider affinity and upstream request
  correlation.
- **Provider affinity**: the in-memory choice that keeps Claude-style aliases
  on the same provider for a session once a direct provider model has been used.

## Attribution

The upstream TypeScript project remains the behavioral reference and is credited
in `NOTICE`. This repository's own implementation, tests, and documentation are
maintained as a Go project under `github.com/lobo235/cc-proxy`.
