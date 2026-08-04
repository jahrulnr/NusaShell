import { describe, expect, it } from "vitest";
import { injectPrompts, type PromptVars } from "../src/agent/services/prompt-injector.js";
import type { AgentPrompt } from "../src/agent/ports/prompt-loader.port.js";
import type { AgentMessage } from "../src/agent/ports/agent-provider.port.js";

const baseVars: PromptVars = {
  currentDate: "2026-01-01",
  environment: "test",
  runtimeOs: "linux",
  availableTools: "mcp_list",
};

const prompts: AgentPrompt[] = [
  { name: "system", content: "You are the agent.", isTemplate: false },
  { name: "mcp-tools", content: "Use tool_list.", isTemplate: false },
  { name: "developer", content: "Date: {{current_date}}", isTemplate: true },
];

describe("injectPrompts summary (structural)", () => {
  it("reports zero system messages for empty prompts + empty messages", () => {
    const { summary } = injectPrompts([], baseVars, []);
    expect(summary.totalSystemMessages).toBe(0);
    expect(summary.totalSystemChars).toBe(0);
    expect(summary.hasSubagentPrompt).toBe(false);
    expect(summary.hasMemory).toBe(false);
    expect(summary.hasUserPrompt).toBe(false);
    expect(summary.subagentVars).toEqual({ availableSubagents: false, defaultSubagent: false });
  });

  it("counts static + developer system messages and chars", () => {
    const { summary } = injectPrompts(prompts, baseVars, []);
    expect(summary.totalSystemMessages).toBe(3);
    expect(summary.totalSystemChars).toBe("You are the agent.".length + "Use tool_list.".length + "Date: 2026-01-01".length);
  });

  it("detects subagent prompt from structural flag, not string sniffing", () => {
    const { summary } = injectPrompts(prompts, baseVars, [], undefined, undefined, "Subagent rules.");
    expect(summary.hasSubagentPrompt).toBe(true);
  });

  it("detects subagent vars from PromptVars, not output text", () => {
    const vars: PromptVars = {
      ...baseVars,
      availableSubagents: "cursor, gemini",
      defaultSubagent: "gemini",
    };
    const { summary } = injectPrompts(prompts, vars, [], undefined, undefined, "Subagent rules.");
    expect(summary.subagentVars.availableSubagents).toBe(true);
    expect(summary.subagentVars.defaultSubagent).toBe(true);
  });

  it("reports subagentVars false when vars are empty/undefined", () => {
    const { summary } = injectPrompts(prompts, baseVars, [], undefined, undefined, "Subagent rules.");
    expect(summary.subagentVars.availableSubagents).toBe(false);
    expect(summary.subagentVars.defaultSubagent).toBe(false);
  });

  it("detects memory from structural flag", () => {
    const { summary } = injectPrompts(prompts, baseVars, [], undefined, "MEMORY (notes)\nbe concise");
    expect(summary.hasMemory).toBe(true);
  });

  it("detects user prompt from structural flag", () => {
    const { summary } = injectPrompts(prompts, baseVars, [], "Be concise.");
    expect(summary.hasUserPrompt).toBe(true);
  });

  it("formats a debug line with all fields", () => {
    const vars: PromptVars = {
      ...baseVars,
      availableSubagents: "gemini",
      defaultSubagent: "gemini",
    };
    const { summary } = injectPrompts(prompts, vars, [], "Be concise.", "MEMORY (notes)", "Subagent rules.");
    const line = summary.toDebugLine("trace-abc");
    expect(line).toContain("traceId=trace-abc");
    expect(line).toContain("hasSubagent=true");
    expect(line).toContain("hasMemory=true");
    expect(line).toContain("hasUserPrompt=true");
    expect(line).toContain("subagentVars.available=true");
    expect(line).toContain("subagentVars.default=true");
  });
});
