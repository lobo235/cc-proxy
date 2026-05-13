# cc-proxy Behavior Spec

Source: `raine/claude-code-proxy` at upstream commit `d5509c5`, version
`0.0.12`, MIT licensed.

This is an implementation guide for the Go rewrite, not a clean-room legal
artifact. The upstream project is used directly as a reference and credited in
`NOTICE`.

## Purpose

Expose enough of the Anthropic Messages API for Claude Code to use alternate
upstream providers. The upstream implementation supports ChatGPT/Codex and Kimi
Code accounts.

## Commands

- `cc-proxy serve`: start local HTTP proxy.
- `cc-proxy --version`, `-v`, or `version`: print version and exit.
- `cc-proxy codex auth login`: Codex browser OAuth flow.
- `cc-proxy codex auth device`: Codex device OAuth flow.
- `cc-proxy codex auth status`: print Codex account/token status; non-zero if
  unauthenticated.
- `cc-proxy codex auth logout`: remove stored Codex credentials.
- `cc-proxy kimi auth login`: Kimi device OAuth flow.
- `cc-proxy kimi auth status`: print Kimi user/token status; non-zero if
  unauthenticated.
- `cc-proxy kimi auth logout`: remove stored Kimi credentials.

Unknown commands print usage and exit non-zero.

## Server

- Binds to `127.0.0.1` only.
- Default port is `18765`.
- `PORT` overrides config-file port.
- Idle timeout in upstream implementation is 255 seconds.
- Logs request/response metadata and stream completion.
- `GET /healthz` returns JSON `{"ok":true}`.
- `POST /v1/messages` handles Claude Code turn requests.
- `POST /v1/messages?beta=true` behaves the same as `/v1/messages`.
- `POST /v1/messages/count_tokens` returns JSON `{"input_tokens": <n>}`.
- Unknown routes return Anthropic-shaped error JSON:
  `{"type":"error","error":{"type":"not_found","message":"..."}}`.
- Invalid JSON returns HTTP 400 with error type `invalid_request_error`.
- Internal handler errors return HTTP 500 with error type `internal_error`.
- Client aborts are treated as status 499 in the upstream implementation.

## Model Routing

Incoming model names may include a Claude Code context-window suffix such as
`[1m]`; strip that suffix before routing.

Provider routing:

- Codex models: `gpt-5.2`, `gpt-5.3-codex`,
  `gpt-5.3-codex-spark`, `gpt-5.4`, `gpt-5.4-mini`, `gpt-5.5`.
- Codex fast aliases: each Codex model with `-fast` suffix. The suffix resolves
  to the base model and requests priority service tier.
- Kimi models: `kimi-for-coding`, `kimi-k2.6`, `k2.6`.
- Anthropic-style aliases: `haiku`, `claude-haiku-4-5`,
  `claude-haiku-4-5-20251001`, `sonnet`, `claude-sonnet-4-6`, `opus`,
  `claude-opus-4-7`.

Anthropic-style aliases route through the configured alias provider. Default
alias provider is `codex`; accepted values are `codex` and `kimi`. Per-session
affinity can pin aliases to the provider used earlier in a session.

Unknown models return HTTP 400 with error type `invalid_request_error` and a
message listing supported models.

## Session State

Requests may include `x-claude-code-session-id`.

The upstream implementation keeps in-memory session state:

- sequence counter
- last-seen timestamp
- alias-provider affinity

Session state expires after 30 minutes idle. At most 10,000 sessions are kept;
oldest sessions are evicted first.

Codex upstream requests set:

- `session_id`
- `x-client-request-id`
- `x-codex-window-id` as `<session-id>:0`
- prompt cache key from session id in translated request

Kimi translated requests use `prompt_cache_key` when a session id exists.

## Configuration

Config sources:

1. environment variables
2. config file
3. built-in defaults

Config file path:

- macOS: `~/.config/claude-code-proxy/config.json`
- other platforms:
  `${XDG_CONFIG_HOME:-$HOME/.config}/claude-code-proxy/config.json`

State/log path:

- `${XDG_STATE_HOME:-$HOME/.local/state}/claude-code-proxy`

Important config:

- `PORT` / `port`: listen port, default `18765`.
- `CCP_ALIAS_PROVIDER` / `aliasProvider`: `codex` or `kimi`, default `codex`.
- `CCP_LOG_STDERR` / `log.stderr`: mirror logs to stderr when set.
- `CCP_LOG_VERBOSE` / `log.verbose`: log redacted request-shape and
  translation summaries for contract debugging.
- `CCP_CODEX_MODEL` / `codex.model`: force all Codex requests to one model.
- `CCP_CODEX_EFFORT` / `codex.effort`: force Codex reasoning effort.
  Accepted values are `none`, `minimal`, `low`, `medium`, `high`, `xhigh`,
  and Claude-facing alias `max`.
- `CCP_CODEX_SERVICE_TIER` / `codex.serviceTier`: `fast`, `priority`, or
  `flex`; `fast` maps to `priority`.
- `CCP_CODEX_BASE_URL` / `codex.baseUrl`: Codex endpoint override.
- `CCP_CODEX_ORIGINATOR`, `CCP_ORIGINATOR`, `codex.originator`: Codex
  `originator` header.
- `CCP_CODEX_USER_AGENT`, `CCP_USER_AGENT`, `codex.userAgent`: Codex
  `User-Agent`.
- `CCP_KIMI_OAUTH_HOST` / `kimi.oauthHost`: Kimi OAuth host override.
- `CCP_KIMI_BASE_URL` / `kimi.baseUrl`: Kimi API base URL.
- `CCP_KIMI_USER_AGENT`, `CCP_USER_AGENT`, `kimi.userAgent`: Kimi
  `User-Agent`.

Malformed `config.json` is reported to stderr and ignored. Invalid key types
are warned and skipped without discarding the whole file.

## Auth Storage

On macOS, upstream stores credentials in Keychain:

- service `claude-code-proxy.codex`
- service `claude-code-proxy.kimi`

On other platforms, tokens are mode-0600 JSON files:

- `${XDG_CONFIG_HOME:-$HOME/.config}/claude-code-proxy/codex/auth.json`
- `${XDG_CONFIG_HOME:-$HOME/.config}/claude-code-proxy/kimi/auth.json`

Legacy fallback path is always `~/.config/claude-code-proxy/<provider>/auth.json`.

Kimi also stores a persistent device id:

- `${XDG_CONFIG_HOME:-$HOME/.config}/claude-code-proxy/kimi/device_id`

## Request Translation

Common behavior:

- System text blocks become upstream instructions/system messages.
- System blocks starting with `x-anthropic-billing-header:` are dropped.
- String message content is normalized to a single text block.
- User image blocks become provider-specific image URL/data URL parts.
- Tool result text content is forwarded; unsupported content is represented
  with placeholder text.
- Tool choice maps `auto`, `none`, `any`, and named tool choices to each
  provider's wire shape.

Codex translation:

- Uses ChatGPT Codex Responses API endpoint
  `https://chatgpt.com/backend-api/codex/responses`.
- Anthropic messages become Responses `input` items.
- Assistant `tool_use` becomes Responses `function_call` with same call id.
- User `tool_result` becomes `function_call_output`; image blocks inside tool
  results are omitted with `[image omitted: <media_type>]`.
- Tool `input_schema` becomes Responses function `parameters`. Object schemas
  missing `properties` are normalized with `properties: {}` because Codex
  rejects otherwise-valid MCP schemas that omit the field. Array schemas
  missing `items` are normalized with `items: {}` for the same reason.
- Requests are always sent upstream with `stream: true` and `store: false`.
- JSON-schema output config maps to Responses `text.format` with strict schema.
- `output_config.effort` values `none`, `minimal`, `low`, `medium`, `high`,
  and `xhigh` pass through to Codex. Claude-facing alias `max` maps to Codex
  `xhigh`. Invalid effort errors.
- `CCP_CODEX_EFFORT` / `codex.effort` takes precedence over request
  `output_config.effort`.
- Reasoning effort requests include `reasoning.encrypted_content` except
  explicit `none`, which asks Codex for no reasoning content.
- Codex reasoning blocks are not forwarded to Claude Code.

Kimi translation:

- Uses Kimi Code endpoint `https://api.kimi.com/coding/v1/chat/completions`.
- All incoming Kimi aliases resolve to wire model `kimi-for-coding`.
- Anthropic messages become OpenAI-style chat messages.
- Tool results become `role:"tool"` messages.
- Image blocks in tool results pass through as `image_url` parts.
- Requests are always sent upstream with `stream: true`,
  `stream_options.include_usage: true`, and `thinking: {type:"enabled"}`.
- `max_tokens` defaults to 32,000 and is capped at 32,000.
- Reasoning effort defaults to `medium`; `max` maps to `high`.
- Kimi reasoning content is forwarded as Anthropic `thinking` blocks unless the
  incoming request disables thinking.

## Streaming

Provider SSE is reduced to Anthropic Messages SSE:

- `message_start`
- `content_block_start`
- `content_block_delta`
- `content_block_stop`
- `message_delta`
- `message_stop`

Codex text output items become Anthropic `text` content blocks.

Codex `function_call` output items become Anthropic `tool_use` content blocks:

- `call_id` is preferred for the Anthropic tool-use `id`; item `id` is the
  fallback.
- `name` is forwarded as the tool name.
- Arguments stream through `response.function_call_arguments.delta` as
  `input_json_delta.partial_json`.
- If Codex only provides finalized arguments, the finalized JSON string is
  emitted as one `input_json_delta`.
- Any emitted `tool_use` block makes final `message_delta.stop_reason`
  `tool_use`.

For non-streaming Anthropic requests, the upstream stream is accumulated into a
single Anthropic JSON response.

## Error Mapping

- Unknown model: HTTP 400, `invalid_request_error`.
- Invalid service tier/effort: HTTP 400, `invalid_request_error`.
- Upstream 401/403: matching status, `authentication_error`.
- Upstream 429: HTTP 429, `rate_limit_error`, preserve `retry-after` when
  available.
- Other upstream non-OK: upstream status, `api_error`.
- Stream accumulation errors: HTTP 502, `api_error`, unless rate-limit shaped.

## Retry

Upstream 429 retries:

- initial delay: 2 seconds
- backoff factor: 2
- max delay: 30 seconds
- max retries: 3
- numeric and HTTP-date `Retry-After` are honored up to the max delay
- exponential fallback uses equal jitter

## Logging

Logs are JSON lines at
`${XDG_STATE_HOME:-$HOME/.local/state}/claude-code-proxy/proxy.log`.
When the helper scripts launch the proxy, pre-start launcher output is written
to `${XDG_STATE_HOME:-$HOME/.local/state}/claude-code-proxy/launcher.log`.

Default logs include:

- server startup with version and log path
- HTTP method/path/status/duration/byte count, excluding healthy `/healthz`
  probes
- message routing decisions with request id, provider, incoming model,
  upstream model, service tier, session sequence, and hashed session id
- Codex upstream response status, duration, and content type
- provider and upstream errors with bounded, redacted upstream error snippets

Verbose logging is enabled with `CCP_LOG_VERBOSE=1` or `log.verbose=true`.
Verbose logs add request-shape summaries and translated Codex request summaries:
message count, message bytes, tool count, tool-schema bytes, MCP tool count,
bounded tool-name samples, system kind, system bytes, output effort, stream
flag, upstream input item count, tool count, service tier, and reasoning effort.
Full prompts, tool arguments, auth headers, and provider tokens are not logged
by default.

Logs are mirrored to stderr when `CCP_LOG_STDERR` is set.

## Known Limits

- Codex image blocks nested inside tool results are omitted.
- Codex reasoning blocks are dropped.
- Codex built-in tool streams such as file search and code interpreter are not
  translated yet.
- Kimi reasoning blocks are forwarded as thinking.
- Session title/background requests are forwarded upstream.
