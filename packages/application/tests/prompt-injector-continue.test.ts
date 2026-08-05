import { describe, expect, it } from "vitest";
import { injectPrompts, type PromptVars } from "../src/agent/services/prompt-injector.js";
import type { AgentPrompt } from "../src/agent/ports/prompt-loader.port.js";
import type { AgentMessage } from "../src/agent/ports/agent-provider.port.js";

const vars: PromptVars = {
  currentDate: "2026-08-05",
  environment: "test",
  runtimeOs: "linux (ubuntu)",
  availableTools: "mcp_list, todo",
};

const staticPrompts: AgentPrompt[] = [
  { name: "system", content: "System prompt", isTemplate: false },
  { name: "mcp-tools", content: "MCP tools prompt", isTemplate: false },
];

describe("injectPrompts — continue steering", () => {
  it("injects the continue prompt as a user message before conversation messages", () => {
    const messages: AgentMessage[] = [{ role: "user", content: "earlier" }, { role: "assistant", content: "ok" }];
    const { messages: out, summary } = injectPrompts(
      staticPrompts,
      vars,
      messages,
      undefined,
      undefined,
      undefined,
      undefined,
      undefined,
      "Continue pursuing open CURRENT TASKS.",
    );
    const userMessages = out.filter((m) => m.role === "user");
    // First user message is the continue steering, before the durable history.
    expect(userMessages[0]?.content).toBe("Continue pursuing open CURRENT TASKS.");
    const continueIdx = out.findIndex((m) => m.role === "user" && m.content === "Continue pursuing open CURRENT TASKS.");
    const historyIdx = out.findIndex((m) => m.role === "user" && m.content === "earlier");
    expect(continueIdx).toBeGreaterThanOrEqual(0);
    expect(historyIdx).toBeGreaterThan(continueIdx);
    expect(summary.hasContinue).toBe(true);
  });

  it("does not inject a continue message when the prompt is undefined", () => {
    const messages: AgentMessage[] = [{ role: "user", content: "hi" }];
    const { messages: out, summary } = injectPrompts(staticPrompts, vars, messages);
    expect(out.some((m) => m.role === "user" && typeof m.content === "string" && m.content.includes("Continue"))).toBe(false);
    expect(summary.hasContinue).toBe(false);
  });
});
