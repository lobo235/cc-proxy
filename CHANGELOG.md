# Changelog

All notable changes to `cc-proxy` will be documented in this file.

This project is a Go reimplementation of Raine Virta's MIT-licensed
`claude-code-proxy`. See `NOTICE` for attribution and upstream license details.

## v0.1.0 - 2026-05-13

Initial public release.

### Added

- Scaffolded the Go `cc-proxy` CLI and local HTTP server.
- Added MIT license, upstream attribution, and behavior-spec documentation.
- Added model routing for Codex, Kimi, Anthropic-style aliases, and Codex
  `-fast` aliases.
- Added upstream-compatible provider auth file storage plus `auth status` and
  `auth logout` behavior for Codex and Kimi.
- Added domain language docs and ADR 0001 for the provider boundary.
- Refactored the provider boundary around a message output sink for translated
  Anthropic-shaped responses.
- Added Codex request translation for user text, system instructions, tools,
  tool choices, assistant tool calls, and user tool results.
- Added Codex Responses API HTTP client with auth, account, session, originator,
  and user-agent headers.
- Added local Codex token counting for `/v1/messages/count_tokens`.
- Added an SSE codec and first Codex text-stream translation path.

### Known Limits

- Codex OAuth login/device flows are not implemented yet.
- Kimi provider request/stream translation is not implemented yet.
- Codex message streaming currently covers the initial text-stream path; richer
  tool-call streaming, non-streaming accumulation, retries, and upstream error
  mapping are still in progress.
