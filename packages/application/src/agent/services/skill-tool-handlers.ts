import { ApplicationError } from "../../errors/application-error.js";
import type { SkillRegistryPort } from "../../skill/ports/skill-registry.port.js";
import type { SkillProvenancePort } from "../../skill/ports/skill-provenance.port.js";
import type { SkillUsagePort, UsageBumpKind } from "../../skill/ports/skill-usage.port.js";
import type { LoggerPort } from "../../plugin/ports/logger.port.js";
import type { SkillApprovalStagingPort } from "./mcp-agent-tool-gateway.js";
import type { WriteOrigin } from "./mcp-agent-tool-gateway.js";
import { clampInt, requireString, optionalString, skillsNotConfigured, skillProtected, skillPinned } from "./gateway-utils.js";

export async function execSkillList(
  registry: SkillRegistryPort | undefined,
  args: Readonly<Record<string, unknown>>,
): Promise<unknown> {
  if (!registry) return skillsNotConfigured();
  const limit = clampInt(args.limit, 50, 1, 100);
  const all = await registry.list();
  const skills = all.slice(0, limit);
  return {
    ok: true,
    data: { skills },
    meta: { count: skills.length, truncated: all.length > limit, data_is_untrusted: true },
  };
}

export async function execSkillSearch(
  registry: SkillRegistryPort | undefined,
  args: Readonly<Record<string, unknown>>,
): Promise<unknown> {
  if (!registry) return skillsNotConfigured();
  const query = requireString(args.query, "query");
  const limit = clampInt(args.limit, 20, 1, 50);
  const matches = await registry.search(query, limit + 1);
  const skills = matches.slice(0, limit);
  return {
    ok: true,
    data: { skills },
    meta: { count: skills.length, truncated: matches.length > limit, data_is_untrusted: true },
  };
}

export async function execSkillRead(
  registry: SkillRegistryPort | undefined,
  skillUsage: SkillUsagePort | undefined,
  logger: LoggerPort | undefined,
  args: Readonly<Record<string, unknown>>,
): Promise<unknown> {
  if (!registry) return skillsNotConfigured();
  const skillId = requireString(args.skill_id, "skill_id");
  const path = optionalString(args.path) || "SKILL.md";
  const offset = clampInt(args.offset, 0, 0, 10_000_000);
  const maxChars = clampInt(args.max_chars, 20_000, 1, 100_000);
  try {
    const file = await registry.read(skillId, path, offset, maxChars);
    void bumpUsage(skillUsage, logger, skillId, "view");
    return { ok: true, data: file, meta: { data_is_untrusted: true } };
  } catch {
    return {
      ok: false,
      error: { code: "not_found", message: "Skill or skill file not found" },
      meta: { data_is_untrusted: true },
    };
  }
}

export async function execSkillManage(
  registry: SkillRegistryPort | undefined,
  provenance: SkillProvenancePort | undefined,
  skillUsage: SkillUsagePort | undefined,
  approvalStaging: SkillApprovalStagingPort | undefined,
  logger: LoggerPort | undefined,
  writeOrigin: WriteOrigin,
  writeApprovalEnabled: boolean,
  args: Readonly<Record<string, unknown>>,
): Promise<unknown> {
  if (!registry) return skillsNotConfigured();
  if (!provenance) return skillsNotConfigured();
  const action = requireString(args.action, "action");
  const skillId = requireString(args.name, "name");
  const content = optionalString(args.content);
  const filePath = optionalString(args.path);
  const shouldStage = writeOrigin === "background_review" && writeApprovalEnabled && approvalStaging;
  try {
    switch (action) {
      case "create": {
        if (!content) throw new ApplicationError("AGENT_INVALID_INPUT", "content is required for create");
        if (shouldStage) {
          const pending = await approvalStaging!.stage(skillId, "create", "SKILL.md", content);
          return { ok: true, data: { staged: true, id: pending.id }, meta: { provenance: "agent", staged: true } };
        }
        const detail = await registry.create(skillId, content);
        await provenance.markAgent(skillId);
        void bumpUsage(skillUsage, logger, skillId, "patch");
        return { ok: true, data: detail, meta: { provenance: "agent" } };
      }
      case "edit": {
        if (!content) throw new ApplicationError("AGENT_INVALID_INPUT", "content is required for edit");
        const origin = await provenance.get(skillId);
        if (origin !== "agent") return skillProtected(skillId);
        if (shouldStage) {
          const pending = await approvalStaging!.stage(skillId, "edit", "SKILL.md", content);
          return { ok: true, data: { staged: true, id: pending.id }, meta: { provenance: "agent", staged: true } };
        }
        const result = await registry.write(skillId, "SKILL.md", content);
        void bumpUsage(skillUsage, logger, skillId, "patch");
        return { ok: true, data: result, meta: { provenance: "agent" } };
      }
      case "write_file": {
        if (!content) throw new ApplicationError("AGENT_INVALID_INPUT", "content is required for write_file");
        if (!filePath) throw new ApplicationError("AGENT_INVALID_INPUT", "path is required for write_file");
        const origin = await provenance.get(skillId);
        if (origin !== "agent") return skillProtected(skillId);
        if (shouldStage) {
          const pending = await approvalStaging!.stage(skillId, "write_file", filePath, content);
          return { ok: true, data: { staged: true, id: pending.id }, meta: { provenance: "agent", staged: true } };
        }
        const result = await registry.write(skillId, filePath, content);
        void bumpUsage(skillUsage, logger, skillId, "patch");
        return { ok: true, data: result, meta: { provenance: "agent" } };
      }
      case "delete": {
        const origin = await provenance.get(skillId);
        if (origin !== "agent") return skillProtected(skillId);
        if (skillUsage) {
          const usage = await skillUsage.getRecord(skillId);
          if (usage.pinned) return skillPinned(skillId);
        }
        if (shouldStage) {
          const pending = await approvalStaging!.stage(skillId, "delete", "", "");
          return { ok: true, data: { staged: true, id: pending.id }, meta: { provenance: "agent", staged: true } };
        }
        await registry.delete(skillId);
        await provenance.clear(skillId);
        return { ok: true, data: { deleted: skillId }, meta: { provenance: "agent" } };
      }
      default:
        throw new ApplicationError("AGENT_INVALID_INPUT", `Unsupported skill_manage action: ${action}`);
    }
  } catch (error) {
    const message = error instanceof Error ? error.message : String(error);
    const code = message.includes("already exists") ? "skill_exists"
      : message.includes("60 characters") ? "description_too_long"
      : "skill_error";
    return { ok: false, error: { code, message }, meta: {} };
  }
}

function bumpUsage(skillUsage: SkillUsagePort | undefined, logger: LoggerPort | undefined, skillId: string, kind: UsageBumpKind): Promise<void> {
  if (!skillUsage) return Promise.resolve();
  return skillUsage.record(skillId, kind).catch((error) => {
    logger?.warn("skill usage bump failed skill=%s kind=%s: %s", skillId, kind, error instanceof Error ? error.message : String(error));
  });
}
