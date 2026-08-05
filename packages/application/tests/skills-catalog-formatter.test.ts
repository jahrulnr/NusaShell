import { describe, expect, it } from "vitest";
import {
  buildSkillsCatalogPrompt,
  SKILLS_CATALOG_BUDGET_CHARS,
  SKILLS_CATALOG_DESC_CAP,
} from "../src/index.js";
import type { SkillSummary } from "../src/index.js";

function makeSummary(id: string, description: string, overrides: Partial<SkillSummary> = {}): SkillSummary {
  return {
    id,
    name: id,
    description,
    fileCount: 1,
    updatedAt: "2026-08-05T00:00:00Z",
    ...overrides,
  };
}

describe("buildSkillsCatalogPrompt", () => {
  it("returns undefined when there are no skills", () => {
    expect(buildSkillsCatalogPrompt([])).toBeUndefined();
  });

  it("includes header + each skill id and description", () => {
    const prompt = buildSkillsCatalogPrompt([
      makeSummary("mcp-creator", "Teaches MCP plugin authoring."),
      makeSummary("creative-writing-research", "Guides long-form writing research."),
    ]);
    expect(prompt).toContain("## Available skills");
    expect(prompt).toContain("`mcp-creator`: Teaches MCP plugin authoring.");
    expect(prompt).toContain("`creative-writing-research`: Guides long-form writing research.");
  });

  it("places priority builtins (mcp-creator, skill-creator) first, then alphabetical", () => {
    const prompt = buildSkillsCatalogPrompt([
      makeSummary("zeta-skill", "Z skill."),
      makeSummary("mcp-creator", "MCP authoring."),
      makeSummary("alpha-skill", "Alpha skill."),
      makeSummary("skill-creator", "Skill authoring."),
    ]);
    expect(prompt).toBeDefined();
    const lines = prompt!.split("\n").filter((l) => l.startsWith("- `"));
    const ids = lines.map((l) => l.match(/`([^`]+)`/)?.[1]);
    expect(ids).toEqual(["mcp-creator", "skill-creator", "alpha-skill", "zeta-skill"]);
  });

  it("clamps long descriptions to SKILLS_CATALOG_DESC_CAP with ellipsis", () => {
    const longDesc = "A".repeat(SKILLS_CATALOG_DESC_CAP + 100);
    const prompt = buildSkillsCatalogPrompt([makeSummary("verbose", longDesc)]);
    expect(prompt).toBeDefined();
    // The description in the output should be capped and end with ellipsis
    const descMatch = prompt!.match(/`verbose`: (.+)$/m);
    expect(descMatch).toBeDefined();
    const desc = descMatch?.[1] ?? "";
    expect(desc.length).toBe(SKILLS_CATALOG_DESC_CAP);
    expect(desc.endsWith("…")).toBe(true);
  });

  it("uses a placeholder for empty descriptions", () => {
    const prompt = buildSkillsCatalogPrompt([makeSummary("empty-desc", "")]);
    expect(prompt).toContain("`empty-desc`: (no description)");
  });

  it("truncates when budget is exceeded and appends tail note", () => {
    // Create many skills with long descriptions to exceed a small budget
    const skills = Array.from({ length: 50 }, (_, i) =>
      makeSummary(`skill-${i}`, `Skill number ${i} with a description.`),
    );
    const smallBudget = 300;
    const prompt = buildSkillsCatalogPrompt(skills, smallBudget);
    expect(prompt).toBeDefined();
    expect(prompt!.length).toBeLessThanOrEqual(smallBudget + 200); // header + tail allowance
    expect(prompt).toContain("More skills exist");
    expect(prompt).toContain("skill_list");
  });

  it("does not append tail note when all skills fit", () => {
    const prompt = buildSkillsCatalogPrompt([
      makeSummary("mcp-creator", "MCP authoring."),
      makeSummary("skill-creator", "Skill authoring."),
    ]);
    expect(prompt).not.toContain("More skills exist");
  });

  it("never includes full SKILL.md body content (only id + description)", () => {
    const prompt = buildSkillsCatalogPrompt([
      makeSummary("my-skill", "A skill with a body that should not appear."),
    ]);
    expect(prompt).toBeDefined();
    // The catalog line should only contain id + description, not body markers
    // like frontmatter delimiters or body headings.
    const catalogLine = prompt!.split("\n").find((l) => l.startsWith("- `my-skill`"));
    expect(catalogLine).toBeDefined();
    expect(catalogLine).not.toContain("---");
    expect(catalogLine).not.toContain("frontmatter");
    expect(catalogLine).not.toContain("## ");
  });

  it("respects default budget constant", () => {
    expect(SKILLS_CATALOG_BUDGET_CHARS).toBe(3000);
    expect(SKILLS_CATALOG_DESC_CAP).toBe(400);
  });
});
