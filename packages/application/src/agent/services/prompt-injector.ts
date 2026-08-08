import type { AgentMessage, AgentPromptCachePolicy } from "../ports/agent-provider.port.js";
import type { AgentPrompt } from "../ports/prompt-loader.port.js";

export interface PromptVars {
  readonly currentDate: string;
  /** Local wall-clock time of the host machine, captured for this turn. */
  readonly currentTime?: string;
  /** IANA timezone resolved from the host machine, e.g. `Asia/Jakarta`. */
  readonly timeZone?: string;
  readonly environment: string;
  /** Host OS/runtime, e.g. `linux (ubuntu)`, `docker (debian)`, `windows`, `macos`. */
  readonly runtimeOs: string;
  readonly availableTools: string;
  readonly workspace?: string;
  /** Comma-separated list of connected+enabled ACP provider IDs (e.g. "cursor, gemini"). */
  readonly availableSubagents?: string;
  /** The user-configured default ACP provider ID (e.g. "gemini"). */
  readonly defaultSubagent?: string;
}

const STATIC_PROMPT_NAMES = ["system", "mcp-tools"];

/**
 * Cache-friendly system-prefix contract (#28).
 *
 * LLM prompt caches match the request prefix byte-for-byte, so anything
 * volatile must live at the END of the system block, never in the middle.
 * injectPrompts guarantees two segments:
 *
 * 1. Stable prefix (byte-identical per run, never passed through applyVars):
 *    system.md -> mcp-tools.md
 * 2. A constant marker (SYSTEM_PREFIX_END_MARKER) that anchors the boundary.
 * 3. Dynamic tail (assembled per turn):
 *    subagent -> user prompt -> hidden runtime-context checkpoint
 *    (MCP, skills, TODO).
 *
 * Runtime facts such as date, workspace, memory, skills, and MCP catalog are
 * supplied by the ephemeral hydration transcript, never copied into the
 * stable prefix.
 */
export const SYSTEM_PREFIX_END_MARKER =
  "=== STABLE SYSTEM PREFIX END / DYNAMIC TAIL BEGIN ===";

/** Calendar date on the host machine for this turn's instant. */
export function stableCurrentDate(now: Date = new Date()): string {
  return `${now.getFullYear()}-${String(now.getMonth() + 1).padStart(2, "0")}-${String(now.getDate()).padStart(2, "0")}`;
}

export function machineCurrentTime(now: Date = new Date()): string {
  return `${String(now.getHours()).padStart(2, "0")}:${String(now.getMinutes()).padStart(2, "0")}:${String(now.getSeconds()).padStart(2, "0")}`;
}

export function machineTimeZone(): string {
  return Intl.DateTimeFormat().resolvedOptions().timeZone || "local machine time";
}

export interface PromptInjectionSummary {
  readonly totalSystemMessages: number;
  readonly totalSystemChars: number;
  readonly hasSubagentPrompt: boolean;
  readonly hasMemory: boolean;
  readonly hasTodo: boolean;
  readonly hasUserPrompt: boolean;
  readonly hasSkillsCatalog: boolean;
  readonly hasContinue: boolean;
  readonly subagentVars: { readonly availableSubagents: boolean; readonly defaultSubagent: boolean };
  toDebugLine(traceId: string): string;
}

export interface InjectPromptsResult {
  readonly messages: AgentMessage[];
  readonly summary: PromptInjectionSummary;
  /** Cache plan derived from assembly boundaries, not message text heuristics. */
  readonly promptCache: AgentPromptCachePolicy;
}

/**
 * Prepend the cache-stable system prompts before conversation messages.
 * A user-supplied prompt is injected after the static prefix. Compaction
 * summary messages from the conversation stay before durable user messages.
 *
 * Returns `{ messages, summary }` where `summary` is built from the structural
 * decisions made during assembly — no string heuristics on the output.
 */
export function injectPrompts(
  prompts: readonly AgentPrompt[],
  vars: PromptVars,
  messages: readonly AgentMessage[],
  userPrompt?: string,
  memoryPrompt?: string,
  subagentPrompt?: string,
  todoPrompt?: string,
  skillsCatalogPrompt?: string,
  continuePrompt?: string,
): InjectPromptsResult {
  const staticPrompts = prompts.filter(
    (prompt) => STATIC_PROMPT_NAMES.includes(prompt.name) && !prompt.isTemplate,
  );
  const out: AgentMessage[] = [];
  let staticChars = 0;
  let subagentChars = 0;
  let userPromptChars = 0;
  let memoryChars = 0;
  let stableSystemMessages = 0;

  for (const prompt of staticPrompts) {
    out.push({ role: "system", content: prompt.content });
    staticChars += prompt.content.length;
    stableSystemMessages += 1;
  }

  // Runtime state must not poison the cacheable system prefix. It is attached
  // as one hidden user checkpoint below after the dynamic system tail.
  const hasSkillsCatalog = Boolean(skillsCatalogPrompt);

  const hasSubagentPrompt = Boolean(subagentPrompt);
  if (subagentPrompt) {
    const rendered = applyVars(subagentPrompt, vars);
    out.push({ role: "system", content: rendered });
    subagentChars += rendered.length;
  }

  const hasUserPrompt = Boolean(userPrompt);
  if (userPrompt) {
    out.push({ role: "system", content: userPrompt });
    userPromptChars += userPrompt.length;
  }

  const hasMemory = Boolean(memoryPrompt);

  const hasTodo = Boolean(todoPrompt);

  for (const message of messages) {
    if (message.role === "system") {
      if (typeof message.content === "string" && message.content.startsWith("Conversation summary:")) {
        out.push(message);
      }
      continue;
    }
    out.push(message);
  }

  // Continue steering is a synthetic follow-up user message. It must come
  // after the durable conversation history: placing it before history makes
  // the model interpret the old user request as a later instruction and can
  // produce context-free fragments (for example `Input:` or raw code). It is
  // sent only to this provider request; the desktop never persists it, so it
  // stays hidden from the room UI. Keeping it outside the system block also
  // preserves the stable system-prefix cache boundary.
  const hasContinue = Boolean(continuePrompt);
  if (continuePrompt) {
    out.push({ role: "user", content: continuePrompt });
  }

  const totalSystemMessages = out.filter((m) => m.role === "system").length;
  const totalSystemChars = staticChars + subagentChars + userPromptChars + memoryChars;
  const availableSubagents = Boolean(vars.availableSubagents && vars.availableSubagents.trim());
  const defaultSubagent = Boolean(vars.defaultSubagent && vars.defaultSubagent.trim());

  const summary: PromptInjectionSummary = {
    totalSystemMessages,
    totalSystemChars,
    hasSubagentPrompt,
    hasMemory,
    hasTodo,
    hasUserPrompt,
    hasSkillsCatalog,
    hasContinue,
    subagentVars: { availableSubagents, defaultSubagent },
    toDebugLine(traceId: string): string {
      return (
        `prompt.injection traceId=${traceId} systemMessages=${totalSystemMessages}` +
        ` systemChars=${totalSystemChars}` +
        ` hasSubagent=${hasSubagentPrompt} hasMemory=${hasMemory} hasTodo=${hasTodo} hasUserPrompt=${hasUserPrompt}` +
        ` hasSkillsCatalog=${hasSkillsCatalog} hasContinue=${hasContinue}` +
        ` subagentVars.available=${availableSubagents} subagentVars.default=${defaultSubagent}`
      );
    },
  };

  return {
    messages: out,
    summary,
    promptCache: {
      mode: "auto",
      ...(stableSystemMessages > 0 ? { stableSystemMessages } : {}),
    },
  };
}

export function applyVars(text: string, vars: PromptVars): string {
  return text
    .replace(/\{\{current_date\}\}/g, vars.currentDate)
    .replace(/\{\{current_time\}\}/g, vars.currentTime ?? "")
    .replace(/\{\{time_zone\}\}/g, vars.timeZone ?? "local machine time")
    .replace(/\{\{environment\}\}/g, vars.environment)
    .replace(/\{\{runtime_os\}\}/g, vars.runtimeOs)
    .replace(/\{\{available_tools\}\}/g, vars.availableTools)
    .replace(/\{\{workspace\}\}/g, vars.workspace || "the user's home directory")
    .replace(/\{\{available_subagents\}\}/g, vars.availableSubagents ?? "")
    .replace(/\{\{default_subagent\}\}/g, vars.defaultSubagent ?? "");
}
