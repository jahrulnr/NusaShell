import type { SkillSummary } from "../../skill/ports/skill-registry.port.js";

/**
 * Soft character budget for the skills catalog block. Targets ~2% of a
 * typical agentic window while keeping a sane floor for small skill sets.
 * Codex/goclaw use similar ~3k char caps for the always-injected catalog.
 */
export const SKILLS_CATALOG_BUDGET_CHARS = 3000;

/**
 * Maximum characters for a single skill description in the catalog.
 * Long descriptions are truncated so one verbose skill cannot starve the
 * rest of the inventory. Matches goclaw's per-skill desc cap.
 */
export const SKILLS_CATALOG_DESC_CAP = 400;

/**
 * Builtin skill IDs that always get priority ordering at the top of the
 * catalog, in display order. These are the authoring/creator skills that
 * the system prompt explicitly calls out — keeping them first ensures the
 * model sees them even when the budget truncates the list.
 */
const PRIORITY_BUILTIN_IDS = ["mcp-creator", "skill-creator"];

/**
 * Build a budgeted skills catalog system-prompt block from registry summaries.
 *
 * The catalog is a name + description inventory (Layer 1) — never full
 * SKILL.md bodies. Skills are ordered with priority builtins first, then
 * alphabetical. Descriptions are clamped to {@link SKILLS_CATALOG_DESC_CAP}
 * chars. The total block is clamped to {@link SKILLS_CATALOG_BUDGET_CHARS};
 * when truncated, a tail note directs the model to `skill_list` /
 * `skill_search` for the rest.
 *
 * Returns `undefined` when there are no skills (no block injected), mirroring
 * `formatMemoryPrompt`'s empty → undefined contract.
 */
export function buildSkillsCatalogPrompt(
  summaries: readonly SkillSummary[],
  budget: number = SKILLS_CATALOG_BUDGET_CHARS,
): string | undefined {
  if (summaries.length === 0) return undefined;

  const ordered = orderSkills(summaries);
  const header = "## Available skills\n\nSkills are instruction packages — not MCP tools. Before domain-heavy work (writing research, SDLC roles, plugin authoring), match the task to a description below, then `skill_read` that skill's `SKILL.md` completely before other tools. For vague matches use `skill_search`. Skip for simple Q&A.\n";
  const tail = "\nMore skills exist — call `skill_list` or `skill_search` to see them.";

  const lines: string[] = [];
  let used = header.length;
  let truncated = false;

  for (const skill of ordered) {
    const desc = clampDescription(skill.description, SKILLS_CATALOG_DESC_CAP);
    const line = `- \`${skill.id}\`: ${desc}`;
    // Reserve tail length so the truncation note always fits.
    const tailReserve = tail.length;
    if (used + line.length + 1 > budget - tailReserve) {
      truncated = true;
      break;
    }
    lines.push(line);
    used += line.length + 1;
  }

  const body = lines.join("\n");
  return truncated ? `${header}${body}\n${tail}` : `${header}${body}`;
}

/**
 * Order skills: priority builtins first (in PRIORITY_BUILTIN_IDS order),
 * then the rest alphabetically by id. Stable and predictable across turns.
 */
function orderSkills(summaries: readonly SkillSummary[]): readonly SkillSummary[] {
  const priorityIndex = new Map(PRIORITY_BUILTIN_IDS.map((id, i) => [id, i]));
  return [...summaries].sort((a, b) => {
    const pa = priorityIndex.get(a.id);
    const pb = priorityIndex.get(b.id);
    if (pa !== undefined && pb !== undefined) return pa - pb;
    if (pa !== undefined) return -1;
    if (pb !== undefined) return 1;
    return a.id.localeCompare(b.id);
  });
}

/**
 * Clamp a description to `cap` chars, appending an ellipsis when truncated.
 * Empty descriptions become a placeholder so the catalog line is not bare.
 */
function clampDescription(description: string, cap: number): string {
  const trimmed = description.trim();
  if (!trimmed) return "(no description)";
  if (trimmed.length <= cap) return trimmed;
  return `${trimmed.slice(0, cap - 1)}…`;
}
