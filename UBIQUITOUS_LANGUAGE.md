# Ubiquitous Language

## Proxy Domain

| Term | Definition | Aliases to avoid |
| ---- | ---------- | ---------------- |
| **Proxy** | The local HTTP service Claude Code sends Anthropic Messages-compatible requests to. | server, gateway |
| **Provider** | An upstream account/API family that can satisfy a routed request. | backend, vendor |
| **Provider auth record** | The stored token record for one **Provider**, containing access token, refresh token, expiry, and provider-specific identity fields. | credential blob, login file |
| **Model alias** | An incoming Claude-style model name that resolves to a provider-specific model. | nickname, shorthand |
| **Direct model** | An incoming model name that already names a specific **Provider** model. | native model, concrete model |
| **Claude Code session** | A client conversation identified by `x-claude-code-session-id`. | conversation, chat session |
| **Provider affinity** | The session-local routing preference that keeps later **Model aliases** on the same **Provider**. | pinning, stickiness |
| **Capability status** | A machine-readable snapshot of routes and **Provider** capabilities exposed by the **Proxy**. | health check, readiness blob |
| **Behavior spec** | The implementation guide derived from the upstream MIT project's observed source behavior. | clean-room spec, ADR |
| **Reasoning effort** | The request or config hint that controls how much upstream reasoning work a **Provider** performs before visible output. | thinking level, compute level |
| **Stream content block** | One Anthropic Messages SSE output block emitted by the **Proxy**, such as visible text or a tool request. | stream chunk, output item |
| **Tool-use block** | A **Stream content block** that tells Claude Code to execute a named tool with JSON input. | function call, tool call |

## Relationships

- A **Proxy** routes each request to exactly one **Provider**.
- A **Provider** can support many **Direct models**.
- A **Model alias** resolves through the configured alias provider unless
  **Provider affinity** exists for the **Claude Code session**.
- A **Provider auth record** belongs to exactly one **Provider**.
- **Capability status** describes what the **Proxy** can do right now; it is
  more detailed than `/healthz`, which only reports process liveness.
- A **Tool-use block** is the Anthropic-facing form of a provider-side function
  call. The provider's argument deltas become `input_json_delta` events.

## Example Dialogue

> **Dev:** "If Claude Code asks for `sonnet`, is that a Codex request?"
>
> **Domain expert:** "`sonnet` is a **Model alias**. It routes through the alias provider unless the **Claude Code session** already has **Provider affinity**."
>
> **Dev:** "So if the same session first used `kimi-k2.6`, later `sonnet` should stay on Kimi?"
>
> **Domain expert:** "Yes. The direct Kimi model establishes **Provider affinity**, and the alias follows it."
>
> **Dev:** "Where does `codex auth status` read from?"
>
> **Domain expert:** "It reads the Codex **Provider auth record**, not a global proxy login."
>
> **Dev:** "Should an orchestrator call `/healthz` or `/status`?"
>
> **Domain expert:** "Use `/healthz` for liveness and **Capability status** for whether Codex messages, token counting, or Kimi routes are actually implemented."

## Flagged Ambiguities

- "Backend" and "provider" were both used earlier. Use **Provider** for the
  upstream API/account family. Reserve "backend interface" only for Go
  implementation internals when needed.
- "Session" can mean a Claude Code conversation or an OAuth login. Use
  **Claude Code session** for request routing and **Provider auth record** for
  stored login state.
- "Health" can mean process liveness or feature readiness. Use `/healthz` for
  liveness and **Capability status** for feature readiness.
- "Max" and "xhigh" are not separate internal levels. Use `max` only for the
  Claude-facing alias and `xhigh` for the Codex upstream **Reasoning effort**.
- "Function call" is provider terminology. Use **Tool-use block** when
  discussing the Anthropic-facing stream emitted to Claude Code.
