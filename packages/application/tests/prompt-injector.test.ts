import { describe, expect, it } from "vitest";
import { injectPrompts, applyVars, type PromptVars } from "../src/index.js";
import type { AgentPrompt } from "../src/index.js";
import type { AgentMessage } from "../src/index.js";

// injectPrompts now returns { messages, summary }; unwrap for legacy tests.
function inject(...args: Parameters<typeof injectPrompts>): AgentMessage[] {
  return injectPrompts(...args).messages;
}

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

  it("substitutes {{available_subagents}} and {{default_subagent}} when provided", () => {
    const varsWithSubagents: PromptVars = {
      ...vars,
      availableSubagents: "cursor, gemini, codex",
      defaultSubagent: "gemini",
    };
    const result = applyVars(
      "Available: {{available_subagents}} Default: {{default_subagent}}",
      varsWithSubagents,
    );
    expect(result).toBe("Available: cursor, gemini, codex Default: gemini");
  });

  it("replaces {{available_subagents}} with empty string when not provided", () => {
    const result = applyVars("Subagents: {{available_subagents}}", vars);
    expect(result).toBe("Subagents: ");
  });
});

describe("injectPrompts", () => {
  it("prepends static and developer prompts before conversation messages", () => {
    const messages: AgentMessage[] = [
      { role: "user", content: "hello" },
    ];
    const result = inject(prompts, vars, messages);
    expect(result).toHaveLength(4);
    expect(result[0]).toEqual({ role: "system", content: "You are the NusaShell agent." });
    expect(result[1]).toEqual({ role: "system", content: "Use tool_list to discover tools." });
    expect(result[2]).toEqual({ role: "system", content: "Date: 2026-07-29 Env: development OS: linux (ubuntu) Tools: mcp_list, tool_list, tool_search, tool_schema" });
    expect(result[3]).toEqual({ role: "user", content: "hello" });
  });

  it("applies template substitution only to developer prompt", () => {
    const messages: AgentMessage[] = [{ role: "user", content: "hi" }];
    const result = inject(prompts, vars, messages);
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
    const result = inject(prompts, vars, messages);
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
    const result = inject(prompts, vars, messages);
    expect(result).toHaveLength(4);
    expect(result[3]).toEqual({ role: "user", content: "hello" });
  });

  it("passes through assistant and tool messages", () => {
    const messages: AgentMessage[] = [
      { role: "user", content: "do something" },
      { role: "assistant", content: "ok", toolCalls: [{ id: "1", name: "tool_list", args: { pluginId: "x" } }] },
      { role: "tool", toolCallId: "1", name: "tool_list", content: '{"tools":[]}' },
    ];
    const result = inject(prompts, vars, messages);
    expect(result).toHaveLength(6);
    expect(result[3]).toEqual({ role: "user", content: "do something" });
    expect(result[4]).toMatchObject({ role: "assistant" });
    expect(result[5]).toMatchObject({ role: "tool" });
  });

  it("works with empty conversation messages", () => {
    const result = inject(prompts, vars, []);
    expect(result).toHaveLength(3);
    expect(result[0]).toMatchObject({ role: "system" });
    expect(result[2]).toMatchObject({ role: "system" });
  });

  it("works with empty prompts", () => {
    const messages: AgentMessage[] = [{ role: "user", content: "hi" }];
    const result = inject([], vars, messages);
    expect(result).toEqual(messages);
  });

  it("injects user prompt after static prompts and before developer prompt", () => {
    const messages: AgentMessage[] = [{ role: "user", content: "hi" }];
    const result = inject(prompts, vars, messages, "Be concise.");
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
    const withUndefined = inject(prompts, vars, messages);
    const withEmpty = inject(prompts, vars, messages, "");
    expect(withUndefined).toEqual(withEmpty);
    expect(withEmpty).toHaveLength(4);
  });

  it("injects memory block after developer prompt and before conversation messages", () => {
    const messages: AgentMessage[] = [{ role: "user", content: "hi" }];
    const result = inject(prompts, vars, messages, undefined, "MEMORY (personal notes) [10% — 220/2200 chars]\nremember to be concise");
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
    const without = inject(prompts, vars, messages);
    const withEmpty = inject(prompts, vars, messages, undefined, "");
    expect(without).toEqual(withEmpty);
    expect(withEmpty).toHaveLength(4);
  });

  it("applies template substitution to the subagent prompt", () => {
    const messages: AgentMessage[] = [{ role: "user", content: "hi" }];
    const varsWithSubagents: PromptVars = {
      ...vars,
      availableSubagents: "cursor, gemini",
      defaultSubagent: "gemini",
    };
    const subagentPrompt = "Available subagents: {{available_subagents}}. Default: {{default_subagent}}.";
    const result = inject(prompts, varsWithSubagents, messages, undefined, undefined, subagentPrompt);
    const subagentMessage = result.find(
      (m) => typeof m.content === "string" && m.content.includes("Available subagents:"),
    ) as { content: string } | undefined;
    expect(subagentMessage).toBeDefined();
    expect(subagentMessage!.content).toBe("Available subagents: cursor, gemini. Default: gemini.");
    expect(subagentMessage!.content).not.toContain("{{");
  });

  it("injects subagent prompt after static prompts and before user prompt", () => {
    const messages: AgentMessage[] = [{ role: "user", content: "hi" }];
    const subagentPrompt = "Subagent delegation rules.";
    const result = inject(prompts, vars, messages, undefined, undefined, subagentPrompt);
    expect(result).toHaveLength(5);
    expect(result[0]).toEqual({ role: "system", content: "You are the NusaShell agent." });
    expect(result[1]).toEqual({ role: "system", content: "Use tool_list to discover tools." });
    expect(result[2]).toEqual({ role: "system", content: "Subagent delegation rules." });
  });

  it("injects todo block after memory block and before conversation messages", () => {
    const messages: AgentMessage[] = [{ role: "user", content: "hi" }];
    const memoryPrompt = "MEMORY (personal notes)\nremember to be concise";
    const todoPrompt = "CURRENT TASKS (agent-owned checklist)\n[ ] do the thing";
    const result = injectPrompts(prompts, vars, messages, undefined, memoryPrompt, undefined, todoPrompt).messages;
    expect(result).toHaveLength(6);
    expect(result[0]).toEqual({ role: "system", content: "You are the NusaShell agent." });
    expect(result[1]).toEqual({ role: "system", content: "Use tool_list to discover tools." });
    const dev = result[2] as { content: string };
    expect(dev.content).toContain("2026-07-29");
    const mem = result[3] as { content: string };
    expect(mem.content).toBe(memoryPrompt);
    const todo = result[4] as { content: string };
    expect(todo.content).toBe(todoPrompt);
    expect(result[5]).toEqual({ role: "user", content: "hi" });
  });

  it("skips todo block when todoPrompt is undefined or empty", () => {
    const messages: AgentMessage[] = [{ role: "user", content: "hi" }];
    const without = injectPrompts(prompts, vars, messages, undefined, undefined, undefined, undefined).messages;
    const withEmpty = injectPrompts(prompts, vars, messages, undefined, undefined, undefined, "").messages;
    expect(without).toEqual(withEmpty);
    expect(withEmpty).toHaveLength(4);
  });

  it("reports hasTodo in the injection summary", () => {
    const messages: AgentMessage[] = [{ role: "user", content: "hi" }];
    const withTodo = injectPrompts(prompts, vars, messages, undefined, undefined, undefined, "CURRENT TASKS\n[ ] x");
    expect(withTodo.summary.hasTodo).toBe(true);
    const without = injectPrompts(prompts, vars, messages, undefined, undefined, undefined, undefined);
    expect(without.summary.hasTodo).toBe(false);
  });

  // --- Live MCP snapshot (Cycle 2) ---

  it("injects mcpLivePrompt after mcp-tools and before skills catalog", () => {
    const messages: AgentMessage[] = [{ role: "user", content: "hi" }];
    const mcpLive = "## Live MCP (runtime)\nRunning: plugin.a\nAdvertised this turn: mcp_a_foo";
    const skills = "## Skills catalog\n- skill.x";
    const result = injectPrompts(
      prompts, vars, messages,
      undefined, undefined, undefined, undefined,
      skills, undefined, mcpLive,
    );
    const systemContents = result.messages
      .filter((m) => m.role === "system")
      .map((m) => String(m.content));
    const mcpToolsIdx = systemContents.findIndex((c) => c === "Use tool_list to discover tools.");
    const liveIdx = systemContents.findIndex((c) => c === mcpLive);
    const skillsIdx = systemContents.findIndex((c) => c === skills);
    expect(mcpToolsIdx).toBeGreaterThanOrEqual(0);
    expect(liveIdx).toBeGreaterThan(mcpToolsIdx);
    expect(skillsIdx).toBeGreaterThan(liveIdx);
  });

  it("reports hasMcpLive in the injection summary and toDebugLine", () => {
    const messages: AgentMessage[] = [{ role: "user", content: "hi" }];
    const withLive = injectPrompts(
      prompts, vars, messages,
      undefined, undefined, undefined, undefined,
      undefined, undefined, "## Live MCP (runtime)\nRunning: plugin.a",
    );
    expect(withLive.summary.hasMcpLive).toBe(true);
    expect(withLive.summary.toDebugLine("trace-x")).toContain("hasMcpLive=true");
    const without = injectPrompts(prompts, vars, messages);
    expect(without.summary.hasMcpLive).toBe(false);
    expect(without.summary.toDebugLine("trace-x")).toContain("hasMcpLive=false");
  });

  it("skips mcpLive block when undefined or empty", () => {
    const messages: AgentMessage[] = [{ role: "user", content: "hi" }];
    const withUndefined = injectPrompts(prompts, vars, messages, undefined, undefined, undefined, undefined, undefined, undefined, undefined).messages;
    const withEmpty = injectPrompts(prompts, vars, messages, undefined, undefined, undefined, undefined, undefined, undefined, "").messages;
    expect(withUndefined).toEqual(withEmpty);
    expect(withEmpty.some((m) => m.role === "system" && String(m.content).includes("Live MCP"))).toBe(false);
  });
});
