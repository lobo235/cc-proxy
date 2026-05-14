# Context/Speed Troubleshooting Agent Prompt

You are a separate agent investigating `cc-proxy`, a Go proxy that lets Claude
Code speak to OpenAI/Codex models such as `gpt-5.5` through Claude Code's normal
Anthropic-compatible harness.

## Goal

Find practical ways to make `claude-gpt` feel closer to native Codex speed while
preserving the value of the Claude Code harness:

- MCP tools must keep loading.
- Skills must keep loading.
- Slash commands such as `/clear`, `/exit`, and `/compact` must keep working.
- Project agents and normal Claude Code tools must keep working.
- Do not solve the problem by broadly disabling tools, MCPs, skills, agents, or
  slash-command support.

## Current Observations

The current implementation is intentionally stateless toward Codex:

- `store` is always `false`.
- `prompt_cache_key` is set from the Claude Code session id.
- `previous_response_id` / stored Responses API mode is not available.
- The Codex backend rejected `store:true` with:
  `{"detail":"Store must be set to false"}`.

Verbose logs show the rough size problem:

- Fresh `claude-gpt` sessions can send roughly 180-250 KB request bodies.
- Normal harness startup may include roughly 58 tools.
- Long active sessions have reached roughly 800 KB+ request bodies.
- Tool schemas may account for 120-145 KB.
- MCP tool count can reach 80+ in larger project sessions.
- Prompt caching often works after the first large request, but the full body is
  still sent from `cc-proxy` to Codex on each turn.

Logs live at:

```text
/home/lobo235/.local/state/claude-code-proxy/proxy.log
```

The launcher is:

```text
scripts/claude-gpt
```

The proxy manager is:

```text
scripts/cc-proxy-ensure
```

## Constraints

- Preserve Claude Code's normal harness by default.
- Keep OpenAI/Codex requests compatible with the current Codex backend.
- Do not reintroduce `CCP_CODEX_STATEFUL_RESPONSES`; that path was removed
  because Codex rejected stored responses.
- Avoid hidden behavior that surprises users. Any lossy optimization must be
  opt-in, observable, and reversible.
- Add useful verbose logging for any optimization so request-shape changes are
  measurable.
- Use TDD for code changes: failing test first, implementation, then green tests.
- Use the project domain language consistently. If terminology is unclear,
  update or propose updates to `UBIQUITOUS_LANGUAGE.md`.

## Suggested Investigation Paths

Investigate these from first principles; do not assume they are correct:

1. **Measure what is actually repeated.**
   Build or improve log analysis that separates:
   - Claude-to-proxy raw request size
   - translated Codex input size
   - system instruction bytes
   - tool schema bytes
   - MCP tool count
   - message-history bytes
   - cached vs uncached input tokens

2. **Find whether Codex supports another state primitive.**
   Search official OpenAI/Codex docs and inspect real Codex CLI traffic if
   available. We already know stored Responses with `store:true` are rejected by
   this backend. Look for alternatives such as conversation ids, prompt/session
   ids, cache-control semantics, or Codex-specific headers.

3. **Improve prompt caching alignment.**
   Verify that the stable prefix order is as cache-friendly as possible:
   instructions first, tool schemas stable, then messages. If we are reordering
   tools or changing generated compatibility instructions turn-to-turn, fix that.

4. **Avoid accidental context churn.**
   Determine whether `cc-proxy` itself adds any changing text to instructions or
   tool schemas on every request. If so, make it stable or move it later so it
   does not poison prompt-cache reuse.

5. **Explore safe, opt-in compaction aids.**
   Claude Code owns conversation compaction, but `cc-proxy` may be able to:
   - detect compaction-shaped requests more reliably,
   - lower compaction effort,
   - log compaction progress/shape,
   - surface statusline-compatible usage estimates.

6. **Explore selective schema caching only if contract-safe.**
   Do not remove tools from normal requests. If you propose tool-schema elision
   or references, prove the Codex backend supports it and that tool calls still
   work. This must be behind a feature flag and tested with MCP tools, file
   tools, skills, subagents, and slash commands.

## Deliverables

Produce one of these:

1. A small, tested implementation that measurably improves speed/context without
   breaking harness features.
2. A narrow experimental branch/flag with clear logs and rollback behavior.
3. A written investigation report explaining why no safe implementation exists
   yet, with exact evidence from logs/docs/API behavior.

Include:

- What you measured.
- What changed.
- How to test it with `claude-gpt`.
- What logs should look like when it works.
- What failure modes remain.

## Commands To Prefer

```bash
go test ./...
make build
CCP_LOG_VERBOSE=1 scripts/cc-proxy-ensure restart
timeout 60 scripts/claude-gpt --model gpt-5.5 --effort low -p "Reply with exactly: proxy-ok"
tail -n 120 /home/lobo235/.local/state/claude-code-proxy/proxy.log
```

Do not run destructive git commands. Do not remove MCPs, skills, slash commands,
or project agents as the primary optimization.
