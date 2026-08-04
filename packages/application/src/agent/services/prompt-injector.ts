import type { AgentMessage } from "../ports/agent-provider.port.js";
import type { AgentPrompt } from "../ports/prompt-loader.port.js";

export interface PromptVars {
  readonly currentDate: string;
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

export interface PromptInjectionSummary {
  readonly totalSystemMessages: number;
  readonly totalSystemChars: number;
  readonly hasSubagentPrompt: boolean;
  readonly hasMemory: boolean;
  readonly hasTodo: boolean;
  readonly hasUserPrompt: boolean;
  readonly subagentVars: { readonly availableSubagents: boolean; readonly defaultSubagent: boolean };
  toDebugLine(traceId: string): string;
}

export interface InjectPromptsResult {
  readonly messages: AgentMessage[];
  readonly summary: PromptInjectionSummary;
}

/**
 * Prepend system and developer prompts before conversation messages.
 * Static prompts are injected as-is; the developer prompt gets {{var}}
 * substitution. A user-supplied prompt is injected after the static prompts
 * and before the developer prompt. Compaction summary messages from the
 * conversation are preserved between the developer prompt and user messages.
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
): InjectPromptsResult {
  const staticPrompts = prompts.filter(
    (prompt) => STATIC_PROMPT_NAMES.includes(prompt.name) && !prompt.isTemplate,
  );
  const developerPrompt = prompts.find(
    (prompt) => prompt.name === "developer" && prompt.isTemplate,
  );

  const out: AgentMessage[] = [];
  let staticChars = 0;
  let developerChars = 0;
  let subagentChars = 0;
  let userPromptChars = 0;
  let memoryChars = 0;
  let todoChars = 0;

  for (const prompt of staticPrompts) {
    out.push({ role: "system", content: prompt.content });
    staticChars += prompt.content.length;
  }

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

  if (developerPrompt) {
    const rendered = applyVars(developerPrompt.content, vars);
    out.push({ role: "system", content: rendered });
    developerChars += rendered.length;
  }

  const hasMemory = Boolean(memoryPrompt);
  if (memoryPrompt) {
    out.push({ role: "system", content: memoryPrompt });
    memoryChars += memoryPrompt.length;
  }

  const hasTodo = Boolean(todoPrompt);
  if (todoPrompt) {
    out.push({ role: "system", content: todoPrompt });
    todoChars += todoPrompt.length;
  }

  for (const message of messages) {
    if (message.role === "system") {
      if (typeof message.content === "string" && message.content.startsWith("Conversation summary:")) {
        out.push(message);
      }
      continue;
    }
    out.push(message);
  }

  const totalSystemMessages = out.filter((m) => m.role === "system").length;
  const totalSystemChars = staticChars + developerChars + subagentChars + userPromptChars + memoryChars + todoChars;
  const availableSubagents = Boolean(vars.availableSubagents && vars.availableSubagents.trim());
  const defaultSubagent = Boolean(vars.defaultSubagent && vars.defaultSubagent.trim());

  const summary: PromptInjectionSummary = {
    totalSystemMessages,
    totalSystemChars,
    hasSubagentPrompt,
    hasMemory,
    hasTodo,
    hasUserPrompt,
    subagentVars: { availableSubagents, defaultSubagent },
    toDebugLine(traceId: string): string {
      return (
        `prompt.injection traceId=${traceId} systemMessages=${totalSystemMessages}` +
        ` systemChars=${totalSystemChars}` +
        ` hasSubagent=${hasSubagentPrompt} hasMemory=${hasMemory} hasTodo=${hasTodo} hasUserPrompt=${hasUserPrompt}` +
        ` subagentVars.available=${availableSubagents} subagentVars.default=${defaultSubagent}`
      );
    },
  };

  return { messages: out, summary };
}

export function applyVars(text: string, vars: PromptVars): string {
  return text
    .replace(/\{\{current_date\}\}/g, vars.currentDate)
    .replace(/\{\{environment\}\}/g, vars.environment)
    .replace(/\{\{runtime_os\}\}/g, vars.runtimeOs)
    .replace(/\{\{available_tools\}\}/g, vars.availableTools)
    .replace(/\{\{workspace\}\}/g, vars.workspace || "the user's home directory")
    .replace(/\{\{available_subagents\}\}/g, vars.availableSubagents ?? "")
    .replace(/\{\{default_subagent\}\}/g, vars.defaultSubagent ?? "");
}
