import { PluginId } from "@nusashell/domain";
import { ApplicationError } from "../../errors/application-error.js";
import type { AgentToolDefinition } from "../ports/agent-provider.port.js";

export function definition(
  name: string,
  description: string,
  properties: Readonly<Record<string, unknown>> = {},
  required: readonly string[] = Object.keys(properties),
): AgentToolDefinition {
  return { name, description, inputSchema: { type: "object", properties, required } };
}

export function stringSchema(): Readonly<Record<string, unknown>> { return { type: "string" }; }

export function optionalString(value: unknown): string { return typeof value === "string" ? value.trim() : ""; }

export function requireString(value: unknown, name: string): string {
  if (typeof value !== "string" || value.trim().length === 0) throw new ApplicationError("AGENT_INVALID_INPUT", `${name} is required`);
  return value;
}

export function stringRecord(value: unknown): Readonly<Record<string, string>> {
  if (value === undefined) return {};
  if (typeof value !== "object" || value === null || Array.isArray(value)) {
    throw new ApplicationError("AGENT_INVALID_INPUT", "arguments must be an object of strings");
  }
  const out: Record<string, string> = {};
  for (const [key, item] of Object.entries(value)) {
    if (typeof item !== "string") throw new ApplicationError("AGENT_INVALID_INPUT", `Prompt argument must be a string: ${key}`);
    out[key] = item;
  }
  return out;
}

export function parsePluginId(value: unknown): PluginId {
  const parsed = PluginId.create(requireString(value, "pluginId"));
  if (!parsed.ok) throw new ApplicationError("PLUGIN_NOT_FOUND", `Invalid plugin id: ${parsed.error.message}`);
  return parsed.value;
}

export function toProviderToolName(pluginId: string, toolName: string): string {
  const readablePlugin = pluginId.replace(/[^A-Za-z0-9_-]/g, "_").slice(0, 28);
  const readableTool = toolName.replace(/[^A-Za-z0-9_-]/g, "_").slice(0, 20);
  return `mcp_${readablePlugin}_${readableTool}`;
}

export function clampInt(value: unknown, fallback: number, min: number, max: number): number {
  let parsed: number;
  if (typeof value === "number") parsed = value;
  else if (typeof value === "string") parsed = Number.parseInt(value, 10);
  else parsed = NaN;
  if (!Number.isFinite(parsed) || Number.isNaN(parsed)) parsed = fallback;
  return Math.max(min, Math.min(max, parsed));
}

export function docsNotConfigured(): unknown {
  return {
    ok: false,
    error: { code: "docs_not_configured", message: "Documentation index is not configured" },
    meta: { index_ready: false },
  };
}

export function docsNotReady(): unknown {
  return {
    ok: false,
    error: { code: "docs_index_not_ready", message: "Documentation index is not ready" },
    meta: { index_ready: false },
  };
}

export function skillsNotConfigured(): unknown {
  return {
    ok: false,
    error: { code: "skills_not_configured", message: "Skill registry is not configured" },
    meta: { data_is_untrusted: true },
  };
}

export function memoryNotConfigured(): unknown {
  return {
    ok: false,
    error: { code: "memory_not_configured", message: "Memory store is not configured" },
    meta: {},
  };
}

export function skillProtected(skillId: string): unknown {
  return {
    ok: false,
    error: { code: "skill_protected", message: `Skill "${skillId}" is not agent-owned and cannot be mutated by the model` },
    meta: {},
  };
}

export function skillPinned(skillId: string): unknown {
  return {
    ok: false,
    error: { code: "skill_pinned", message: `Skill "${skillId}" is pinned and cannot be deleted` },
    meta: {},
  };
}
