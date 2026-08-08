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
  it("injects a hidden synthetic user follow-up after conversation messages", () => {
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
    const continueIdx = out.findIndex((m) => m.role === "user" && m.content === "Continue pursuing open CURRENT TASKS.");
    const historyIdx = out.findIndex((m) => m.role === "user" && m.content === "earlier");
    expect(continueIdx).toBeGreaterThanOrEqual(0);
    expect(continueIdx).toBeGreaterThan(historyIdx);
    expect(out[continueIdx]?.role).toBe("user");
    expect(summary.hasContinue).toBe(true);
  });

  it("keeps synthetic continuation outside the stable system cache prefix", () => {
    const result = injectPrompts(
      staticPrompts,
      vars,
      [{ role: "user", content: "earlier" }],
      undefined,
      undefined,
      undefined,
      undefined,
      undefined,
      "Continue pursuing open CURRENT TASKS.",
    );
    const stableCount = result.promptCache.stableSystemMessages ?? 0;
    expect(result.messages.slice(0, stableCount).every((message) => message.role === "system")).toBe(true);
    expect(result.messages[stableCount]?.role).toBe("user");
    expect(result.messages.at(-1)?.content).toBe("Continue pursuing open CURRENT TASKS.");
    const withoutContinue = injectPrompts(staticPrompts, vars, [{ role: "user", content: "earlier" }]);
    expect(result.summary.totalSystemChars).toBe(withoutContinue.summary.totalSystemChars);
  });

  it("does not inject a continue message when the prompt is undefined", () => {
    const messages: AgentMessage[] = [{ role: "user", content: "hi" }];
    const { messages: out, summary } = injectPrompts(staticPrompts, vars, messages);
    expect(out.some((m) => m.role === "user" && typeof m.content === "string" && m.content.includes("Continue"))).toBe(false);
    expect(summary.hasContinue).toBe(false);
  });
});
