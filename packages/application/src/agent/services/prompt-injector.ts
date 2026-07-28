import type { AgentMessage } from "../ports/agent-provider.port.js";
import type { AgentPrompt } from "../ports/prompt-loader.port.js";

export interface PromptVars {
  readonly currentDate: string;
  readonly environment: string;
  readonly availableTools: string;
}

const STATIC_PROMPT_NAMES = ["system", "mcp-tools"];

/**
 * Prepend system and developer prompts before conversation messages.
 * Static prompts are injected as-is; the developer prompt gets {{var}}
 * substitution. Compaction summary messages from the conversation are
 * preserved between the developer prompt and user messages.
 */
export function injectPrompts(
  prompts: readonly AgentPrompt[],
  vars: PromptVars,
  messages: readonly AgentMessage[],
): AgentMessage[] {
  const staticPrompts = prompts.filter(
    (prompt) => STATIC_PROMPT_NAMES.includes(prompt.name) && !prompt.isTemplate,
  );
  const developerPrompt = prompts.find(
    (prompt) => prompt.name === "developer" && prompt.isTemplate,
  );

  const out: AgentMessage[] = [];

  for (const prompt of staticPrompts) {
    out.push({ role: "system", content: prompt.content });
  }

  if (developerPrompt) {
    out.push({ role: "system", content: applyVars(developerPrompt.content, vars) });
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

  return out;
}

export function applyVars(text: string, vars: PromptVars): string {
  return text
    .replace(/\{\{current_date\}\}/g, vars.currentDate)
    .replace(/\{\{environment\}\}/g, vars.environment)
    .replace(/\{\{available_tools\}\}/g, vars.availableTools);
}
