Run one agentic post-conversation pass over this conversation and curate
long-term memory and agent-owned skills, per your system instructions.

## Memory
Gather evidence (transcript, files, `docs`, web when needed), then:
- Update or trim `memory/user.md` (user rules, preferences, stable context) only on clear, durable, explicit statements or corrections from the user; not on one-off choices made during this session.
- Update or trim `memory/soul.md` (agent working knowledge) when the conversation teaches conventions, gotchas, or reusable fixes.
- Save new durable facts as fragments first, with category/tags; promote to `user.md`/`soul.md` only if the fact clearly belongs in the always-injected hot tier. Replace or delete fragments that are contradicted, stale, or obsolete.
- If research was needed to verify or fill a gap, tag what you learned by its actual source (agent-tier world knowledge vs. user-stated fact) and note approximate recency for anything that can go stale.
- Consider `model_override` separately if the transcript shows clear evidence of wrong model catalog metadata; it is not memory and is not saved as a fragment.

Do NOT save task status, work logs, implementation details, secrets, or anything likely to be stale by the next session.

## Skills
Only create, update, or delete a skill when there is clear evidence for it:
- No existing skill covers a reusable procedure from this conversation.
- An existing skill is outdated or proved inefficient and should be improved.
- An agent-owned skill is obsolete, superseded, or was never useful; verify ownership first and pass `owned_by: "agent"` when deleting.
Never modify user-owned or builtin skills.

## Boundaries
No exec, no arbitrary file writes, no automation, no interactive tools, no project-memory writes. At most 10 mutating memory/skill calls per run (read-only research is not counted against this, but keep it proportional to what actually needs verifying).

## If nothing durable to save
If you considered anything and decided it was too borderline to save, name it in one line first. Then respond with exactly:

`Nothing to save.`