# cc-proxy project guidance

## Wiki integration

<!-- wiki-template:begin -->

Persistent project knowledge lives at `https://wiki.big.netlobo.com/p/cc-proxy/`.
The `wiki_*` MCP tools (loaded via user-scope `claude mcp add wiki ...`)
provide reads + writes against this project's namespace.

### Day-to-day capture

- Session start: `wiki_get_start_here(project: "cc-proxy")`.
- Reading before answering: use `wiki_get_index` or search first, then `wiki_get_summary`, and only call `wiki_get_article` when exact body text is needed.
- Wiki refs: when the user provides `wiki:ss:<uuid>`, `wiki://screenshot/<uuid>`, `/p/<project>/articles/<slug>`, `/g/<category>/<slug>`, or a wiki URL, call `wiki_resolve_ref` first. Use fallback URLs only if the harness cannot consume MCP image/resource content.
- Plan-mode plan accepted: `wiki_write_article(... kind: "plan", category: "plans" ...)`.
- Decision moment: `wiki_write_article(... kind: "decision", category: "decisions" ...)` then `wiki_set_decision_status` if accepted.
- New env var added: `wiki_record_env_var(...)`.
- Deploy lands: `wiki_record_deploy(...)`.
- Future-work item captured in conversation: `wiki_write_article(... kind: "future-work" ...)` (status via `wiki_set_future_work_status` once picked up).
- Future-work item started: read the article first, verify it is not already implemented, then mark it `in-flight` with `wiki_set_future_work_status`.
- Future-work item completed: after implementation and verification, immediately mark it `landed` with `wiki_set_future_work_status` and include a note naming the commit, PR, or concrete evidence. Use `dropped` only when intentionally closing without implementation.
- Structured metadata: when writing articles, put important machine-readable descriptors in `frontmatter` JSON (meeting times, participants, source IDs, external IDs, review state, etc.). Keep `bodyMd` for readable markdown; do not embed YAML frontmatter at the top of the body.
- Reusable cross-project knowledge: use `wiki_propose_global_article(...)`, usually with `kind: "reference"` or `kind: "decision"`. Use global scope for overall best practices, useful homelab environment context, recurring issues seen in multiple projects, or canonical engineering rules. Do not use it for routine project-local notes.
- After proposing global content, tell the user it is pending in Inbox and must be approved, edited, moved to a project, sent to the maintainer agent for revision, or rejected before it becomes canonical global knowledge.
- Use MCP tools for normal Claude-agent work. Use the stable `/api/v1` REST article publisher only for non-MCP producers such as meeting recorders, hooks, and shell scripts with project-scoped bearer tokens.

### Source of truth

- `/p/cc-proxy/future-work/` is the canonical future-work tracker. Don't create a local `FUTURE_WORK.md`.
- `/p/cc-proxy/decisions/` is the canonical ADR home. Don't create a local `docs/adr/`. New ADRs go directly to the wiki via `wiki_write_article(kind: "decision", category: "decisions", slug: "adr-NNNN-<kebab-title>")`.
- `/g/reference/` is the canonical home for reusable global context. Global articles are true projectless `/g/` content, not articles in a fake `global` project.
- `CHANGELOG.md` (in-repo) stays the source of truth for release notes — tightly coupled to the SemVer/tag flow described in the global `CLAUDE.md`.

### Slash commands available (machine-installed)

- `/wiki-capture-decision [cc-proxy] [title]` — write a current-conversation ADR.
- `/wiki-capture-plan [cc-proxy] [title]` — write a `kind=plan` article.
- `/wiki-onboard cc-proxy` — re-run the bootstrap (idempotent; refreshes this managed zone on every run).

<!-- wiki-template:end -->
