# Changelog

All notable changes to `cc-proxy` will be documented in this file.

This project is a Go reimplementation of Raine Virta's MIT-licensed
`claude-code-proxy`. See `NOTICE` for attribution and upstream license details.

## [Unreleased]

### Added

- Opt-in stable Codex `prompt_cache_key` strategy via
  `CCP_CODEX_CACHE_KEY_STRATEGY=stable` (default `session`). When enabled the
  key is derived from `cc-proxy:codex:<model>` so repeated one-shot
  `claude-gpt -p` invocations share a backend cache shard and the cold-cache
  penalty on the first turn often disappears. Cache validation is still done by
  prefix bytes, so a mismatched prefix simply behaves like cold cache.
- `scripts/claude-gpt` now accepts `CLAUDE_GPT_CACHE_KEY_STRATEGY=stable` and
  passes it through when it starts a stopped proxy.
- Verbose `codex request translated` log now reports `body_bytes`,
  `instructions_bytes`, `tools_bytes`, `input_bytes`, `prefix_fingerprint`
  (sha256-12 of instructions + tools JSON), and `cache_key_strategy`. The
  fingerprint catches prefix drift across turns within a session — a stable
  value confirms the prompt cache prefix is byte-stable.
- Verbose `codex usage` log now reports `cached_pct` alongside
  `cached_input_tokens` so cache health is readable at a glance.

### Fixed

- Codex stream translation now treats terminal `response.completed` and
  upstream error events as the end of the downstream Anthropic SSE stream
  instead of waiting for the upstream socket to close. This prevents
  `claude-gpt` sessions from hanging for minutes after a tool result such as
  `Error editing file`.
- HTTP request logging no longer triggers Go's `superfluous response.WriteHeader`
  warning when an error path attempts to write a status after streaming has
  already begun.
- `scripts/claude-gpt` now passes the local proxy token through
  `ANTHROPIC_AUTH_TOKEN` while leaving `ANTHROPIC_API_KEY` empty, matching
  Claude Code's current auth expectations for a custom Anthropic base URL.
- Raised the SSE parser per-line cap from 4 MiB to 32 MiB so Codex events
  carrying large encrypted reasoning content or large tool-call argument blobs
  no longer fail with `bufio.Scanner: token too long`. The failure mode looked
  to operators like Claude Code's "API Error: Stream ended without receiving
  any events" because the proxy had already returned `200 OK` with
  `text/event-stream` before the parse aborted.
- When the upstream stream errors before any event reaches the client, the
  proxy now salvages with a minimal `message_start` + `error` + `message_stop`
  sequence and logs `codex stream translation failed` at WARN. Operators see a
  real error event instead of an empty SSE response.
- Codex stream error events now preserve the upstream error message in the
  downstream Anthropic SSE error and log the upstream error type/code/message at
  WARN, making failures like Claude Code's generic `API Error: Upstream error`
  diagnosable from proxy logs.

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
