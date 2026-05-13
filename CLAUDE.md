# cc-proxy project guidance

## Wiki integration

<!-- wiki-template:begin -->

Persistent project knowledge lives at `https://wiki.big.netlobo.com/p/cc-proxy/`.
The `wiki_*` MCP tools (loaded via user-scope `claude mcp add wiki ...`)
provide reads + writes against this project's namespace.

### Day-to-day capture

- Session start: `wiki_get_start_here(project: "cc-proxy")`.
- Plan-mode plan accepted: `wiki_write_article(... kind: "plan", category: "plans" ...)`.
- Decision moment: `wiki_write_article(... kind: "decision", category: "decisions" ...)` then `wiki_set_decision_status` if accepted.
- New env var added: `wiki_record_env_var(...)`.
- Deploy lands: `wiki_record_deploy(...)`.
- Future-work item captured in conversation: `wiki_write_article(... kind: "future-work" ...)` (status via `wiki_set_future_work_status` once picked up).

### Source of truth

- `/p/cc-proxy/future-work/` is the canonical future-work tracker. Don't create a local `FUTURE_WORK.md`.
- `/p/cc-proxy/decisions/` is the canonical ADR home. Don't create a local `docs/adr/`. New ADRs go directly to the wiki via `wiki_write_article(kind: "decision", category: "decisions", slug: "adr-NNNN-<kebab-title>")`.
- `CHANGELOG.md` (in-repo) stays the source of truth for release notes -- tightly coupled to the SemVer/tag flow described in the global `CLAUDE.md`.

### Slash commands available (machine-installed)

- `/wiki-capture-decision [cc-proxy] [title]` -- write a current-conversation ADR.
- `/wiki-capture-plan [cc-proxy] [title]` -- write a `kind=plan` article.
- `/wiki-onboard cc-proxy` -- re-run the bootstrap (idempotent; refreshes this managed zone on every run).

<!-- wiki-template:end -->
