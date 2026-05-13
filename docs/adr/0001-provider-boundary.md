# ADR 0001: Provider Boundary Writes Anthropic-Shaped Output

## Status

Accepted

## Context

`cc-proxy` receives Anthropic Messages-compatible requests from Claude Code, then
routes each turn to an upstream provider such as Codex or Kimi. Providers differ
in request format, authentication, retry behavior, streaming format, and
non-stream accumulation behavior.

The initial scaffold exposed provider calls as methods returning `*http.Response`.
That was convenient for the stub, but it implies that the provider returns a
client-ready HTTP response. In reality, providers call upstream APIs and must
translate those upstream responses into Anthropic-shaped JSON or SSE before the
proxy writes to Claude Code.

## Decision

The provider boundary will evolve toward a turn-executor shape:

- the server decodes the Anthropic envelope, resolves model routing, maintains
  session affinity, and constructs request metadata;
- the provider owns request translation, auth loading/refresh, upstream HTTP
  calls, retries, provider stream parsing, Anthropic stream emission, and
  non-stream accumulation;
- provider message execution writes Anthropic-shaped output to a small sink
  interface supplied by the server;
- count-token calls return structured `input_tokens` results;
- auth status/logout remain separate from the hot message path.

## Consequences

This keeps Codex/Kimi wire details out of `internal/server` and avoids wrapping
mapped streams back into artificial `*http.Response` objects.

The tradeoff is that provider tests use fake sinks instead of plain returned
values. That is acceptable because the primary behavior is streaming output, not
a single in-memory value.

## Alternatives Considered

- Keep returning `*http.Response`: simple now, but leaks upstream transport
  artifacts into the server and becomes awkward once streams are translated.
- Return a proxy-owned `Reply` with status, headers, and body: better than raw
  upstream response, but still requires pipes or buffering for streaming.
- Expose separate rich interfaces for streams, events, auth, and count tokens:
  flexible, but too wide before the concrete providers require that shape.
