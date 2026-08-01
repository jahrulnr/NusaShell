import { describe, expect, it } from "vitest";
import { injectPrompts, applyVars, type PromptVars } from "../src/index.js";
import type { AgentPrompt } from "../src/index.js";
import type { AgentMessage } from "../src/index.js";

const vars: PromptVars = {
  currentDate: "2026-07-29",
  environment: "development",
  runtimeOs: "linux (ubuntu)",
  availableTools: "mcp_list, tool_list, tool_search, tool_schema",
};

const varsWithWorkspace: PromptVars = {
  ...vars,
  workspace: "/home/user/projects/myapp",
};

const prompts: AgentPrompt[] = [
  { name: "system", content: "You are the NusaShell agent.", isTemplate: false },
  { name: "mcp-tools", content: "Use tool_list to discover tools.", isTemplate: false },
  { name: "developer", content: "Date: {{current_date}} Env: {{environment}} OS: {{runtime_os}} Tools: {{available_tools}}", isTemplate: true },
];

describe("applyVars", () => {
  it("replaces all template variables", () => {
    const result = applyVars("{{current_date}} {{environment}} {{runtime_os}} {{available_tools}}", vars);
    expect(result).toBe("2026-07-29 development linux (ubuntu) mcp_list, tool_list, tool_search, tool_schema");
  });

  it("leaves unknown variables as-is", () => {
    const result = applyVars("{{unknown_var}} stays", vars);
    expect(result).toBe("{{unknown_var}} stays");
  });

  it("substitutes {{workspace}} when provided", () => {
    const result = applyVars("Workspace: {{workspace}}", varsWithWorkspace);
    expect(result).toBe("Workspace: /home/user/projects/myapp");
  });

  it("falls back to home directory when workspace is not provided", () => {
    const result = applyVars("Workspace: {{workspace}}", vars);
    expect(result).toBe("Workspace: the user's home directory");
  });
});

describe("injectPrompts", () => {
  it("prepends static and developer prompts before conversation messages", () => {
    const messages: AgentMessage[] = [
      { role: "user", content: "hello" },
    ];
    const result = injectPrompts(prompts, vars, messages);
    expect(result).toHaveLength(4);
    expect(result[0]).toEqual({ role: "system", content: "You are the NusaShell agent." });
    expect(result[1]).toEqual({ role: "system", content: "Use tool_list to discover tools." });
    expect(result[2]).toEqual({ role: "system", content: "Date: 2026-07-29 Env: development OS: linux (ubuntu) Tools: mcp_list, tool_list, tool_search, tool_schema" });
    expect(result[3]).toEqual({ role: "user", content: "hello" });
  });

  it("applies template substitution only to developer prompt", () => {
    const messages: AgentMessage[] = [{ role: "user", content: "hi" }];
    const result = injectPrompts(prompts, vars, messages);
    const systemContent = result[0] as { content: string };
    expect(systemContent.content).not.toContain("2026-07-29");
    const devContent = result[2] as { content: string };
    expect(devContent.content).toContain("2026-07-29");
    expect(devContent.content).not.toContain("{{current_date}}");
  });

  it("preserves compaction summary messages", () => {
    const messages: AgentMessage[] = [
      { role: "system", content: "Conversation summary:\nPrior context here" },
      { role: "user", content: "continue" },
    ];
    const result = injectPrompts(prompts, vars, messages);
    expect(result).toHaveLength(5);
    const summary = result[3] as { role: string; content: string };
    expect(summary.role).toBe("system");
    expect(summary.content).toContain("Conversation summary:");
    expect(result[4]).toEqual({ role: "user", content: "continue" });
  });

  it("drops non-summary system messages from conversation", () => {
    const messages: AgentMessage[] = [
      { role: "system", content: "random system message" },
      { role: "user", content: "hello" },
    ];
    const result = injectPrompts(prompts, vars, messages);
    expect(result).toHaveLength(4);
    expect(result[3]).toEqual({ role: "user", content: "hello" });
  });

  it("passes through assistant and tool messages", () => {
    const messages: AgentMessage[] = [
      { role: "user", content: "do something" },
      { role: "assistant", content: "ok", toolCalls: [{ id: "1", name: "tool_list", args: { pluginId: "x" } }] },
      { role: "tool", toolCallId: "1", name: "tool_list", content: '{"tools":[]}' },
    ];
    const result = injectPrompts(prompts, vars, messages);
    expect(result).toHaveLength(6);
    expect(result[3]).toEqual({ role: "user", content: "do something" });
    expect(result[4]).toMatchObject({ role: "assistant" });
    expect(result[5]).toMatchObject({ role: "tool" });
  });

  it("works with empty conversation messages", () => {
    const result = injectPrompts(prompts, vars, []);
    expect(result).toHaveLength(3);
    expect(result[0]).toMatchObject({ role: "system" });
    expect(result[2]).toMatchObject({ role: "system" });
  });

  it("works with empty prompts", () => {
    const messages: AgentMessage[] = [{ role: "user", content: "hi" }];
    const result = injectPrompts([], vars, messages);
    expect(result).toEqual(messages);
  });

  it("injects user prompt after static prompts and before developer prompt", () => {
    const messages: AgentMessage[] = [{ role: "user", content: "hi" }];
    const result = injectPrompts(prompts, vars, messages, "Be concise.");
    expect(result).toHaveLength(5);
    expect(result[0]).toEqual({ role: "system", content: "You are the NusaShell agent." });
    expect(result[1]).toEqual({ role: "system", content: "Use tool_list to discover tools." });
    expect(result[2]).toEqual({ role: "system", content: "Be concise." });
    expect(result[3]).toMatchObject({ role: "system" });
    expect((result[3] as { content: string }).content).toContain("2026-07-29");
    expect(result[4]).toEqual({ role: "user", content: "hi" });
  });

  it("skips user prompt when it is empty or undefined", () => {
    const messages: AgentMessage[] = [{ role: "user", content: "hi" }];
    const withUndefined = injectPrompts(prompts, vars, messages);
    const withEmpty = injectPrompts(prompts, vars, messages, "");
    expect(withUndefined).toEqual(withEmpty);
    expect(withEmpty).toHaveLength(4);
  });

  it("injects memory block after developer prompt and before conversation messages", () => {
    const messages: AgentMessage[] = [{ role: "user", content: "hi" }];
    const result = injectPrompts(prompts, vars, messages, undefined, "MEMORY (personal notes) [10% — 220/2200 chars]\nremember to be concise");
    expect(result).toHaveLength(5);
    expect(result[0]).toEqual({ role: "system", content: "You are the NusaShell agent." });
    expect(result[1]).toEqual({ role: "system", content: "Use tool_list to discover tools." });
    expect(result[2]).toMatchObject({ role: "system" });
    expect((result[2] as { content: string }).content).toContain("2026-07-29");
    expect(result[3]).toEqual({ role: "system", content: "MEMORY (personal notes) [10% — 220/2200 chars]\nremember to be concise" });
    expect(result[4]).toEqual({ role: "user", content: "hi" });
  });

  it("skips memory block when memoryPrompt is undefined or empty", () => {
    const messages: AgentMessage[] = [{ role: "user", content: "hi" }];
    const without = injectPrompts(prompts, vars, messages);
    const withEmpty = injectPrompts(prompts, vars, messages, undefined, "");
    expect(without).toEqual(withEmpty);
    expect(withEmpty).toHaveLength(4);
  });
});
