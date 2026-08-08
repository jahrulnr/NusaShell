import { describe, expect, it } from "vitest";
import {
  injectPrompts,
  applyVars,
  machineCurrentTime,
  stableCurrentDate,
  type PromptVars,
} from "../src/index.js";
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
];

describe("applyVars", () => {
  it("replaces all template variables", () => {
    const result = applyVars("{{current_date}} {{environment}} {{runtime_os}} {{available_tools}}", vars);
    expect(result).toBe("2026-07-29 development linux (ubuntu) mcp_list, tool_list, tool_search, tool_schema");
  });

  it("substitutes the local machine time and timezone when supplied", () => {
    const result = applyVars("{{current_date}} {{current_time}} {{time_zone}}", {
      ...vars,
      currentTime: "09:30:15",
      timeZone: "Asia/Jakarta",
    });
    expect(result).toBe("2026-07-29 09:30:15 Asia/Jakarta");
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
  it("does not inject a legacy developer prompt into the default request", () => {
    const messages: AgentMessage[] = [{ role: "user", content: "hello" }];
    const result = injectPrompts([
      { name: "system", content: "Stable system", isTemplate: false },
      { name: "mcp-tools", content: "Stable MCP workflow", isTemplate: false },
      { name: "developer", content: "Legacy dynamic prompt: {{current_date}}", isTemplate: true },
    ], vars, messages);

    expect(result.messages).toEqual([
      { role: "system", content: "Stable system" },
      { role: "system", content: "Stable MCP workflow" },
      { role: "user", content: "hello" },
    ]);
    expect(result.promptCache).toEqual({ mode: "auto", stableSystemMessages: 2 });
  });

  it("prepends only the stable prompt pair before conversation messages", () => {
    const messages: AgentMessage[] = [
      { role: "user", content: "hello" },
    ];
    const result = inject(prompts, vars, messages);
    expect(result).toHaveLength(3);
    expect(result[0]).toEqual({ role: "system", content: "You are the NusaShell agent." });
    expect(result[1]).toEqual({ role: "system", content: "Use tool_list to discover tools." });
    expect(result[2]).toEqual({ role: "user", content: "hello" });
  });

  it("preserves compaction summary messages", () => {
    const messages: AgentMessage[] = [
      { role: "system", content: "Conversation summary:\nPrior context here" },
      { role: "user", content: "continue" },
    ];
    const result = inject(prompts, vars, messages);
    expect(result).toHaveLength(4);
    const summary = result[2] as { role: string; content: string };
    expect(summary.role).toBe("system");
    expect(summary.content).toContain("Conversation summary:");
    expect(result[3]).toEqual({ role: "user", content: "continue" });
  });

  it("drops non-summary system messages from conversation", () => {
    const messages: AgentMessage[] = [
      { role: "system", content: "random system message" },
      { role: "user", content: "hello" },
    ];
    const result = inject(prompts, vars, messages);
    expect(result).toHaveLength(3);
    expect(result[2]).toEqual({ role: "user", content: "hello" });
  });

  it("passes through assistant and tool messages", () => {
    const messages: AgentMessage[] = [
      { role: "user", content: "do something" },
      { role: "assistant", content: "ok", toolCalls: [{ id: "1", name: "tool_list", args: { pluginId: "x" } }] },
      { role: "tool", toolCallId: "1", name: "tool_list", content: '{"tools":[]}' },
    ];
    const result = inject(prompts, vars, messages);
    expect(result).toHaveLength(5);
    expect(result[2]).toEqual({ role: "user", content: "do something" });
    expect(result[3]).toMatchObject({ role: "assistant" });
    expect(result[4]).toMatchObject({ role: "tool" });
  });

  it("works with empty conversation messages", () => {
    const result = inject(prompts, vars, []);
    expect(result).toHaveLength(2);
    expect(result[0]).toMatchObject({ role: "system" });
    expect(result[1]).toMatchObject({ role: "system" });
  });

  it("works with empty prompts", () => {
    const messages: AgentMessage[] = [{ role: "user", content: "hi" }];
    const result = inject([], vars, messages);
    expect(result).toEqual(messages);
  });

  it("injects user prompt after the stable prefix", () => {
    const messages: AgentMessage[] = [{ role: "user", content: "hi" }];
    const result = inject(prompts, vars, messages, "Be concise.");
    expect(result).toHaveLength(4);
    expect(result[0]).toEqual({ role: "system", content: "You are the NusaShell agent." });
    expect(result[1]).toEqual({ role: "system", content: "Use tool_list to discover tools." });
    expect(result[2]).toEqual({ role: "system", content: "Be concise." });
    expect(result[3]).toEqual({ role: "user", content: "hi" });
  });

  it("skips user prompt when it is empty or undefined", () => {
    const messages: AgentMessage[] = [{ role: "user", content: "hi" }];
    const withUndefined = inject(prompts, vars, messages);
    const withEmpty = inject(prompts, vars, messages, "");
    expect(withUndefined).toEqual(withEmpty);
    expect(withEmpty).toHaveLength(3);
  });

  it("does not inject memory into the system prompt (memory moves to hydration tool per REV2)", () => {
    const messages: AgentMessage[] = [{ role: "user", content: "hi" }];
    const result = inject(prompts, vars, messages, undefined, "MEMORY (personal notes) [10% — 220/2200 chars]\nremember to be concise");
    expect(result).toHaveLength(3);
    expect(result[0]).toEqual({ role: "system", content: "You are the NusaShell agent." });
    expect(result[1]).toEqual({ role: "system", content: "Use tool_list to discover tools." });
    // Memory content no longer appears in a system message — hydration carries it.
    expect(result.some((m) => m.role === "system" && String(m.content).includes("MEMORY (personal notes)"))).toBe(false);
    expect(result[2]).toEqual({ role: "user", content: "hi" });
  });

  it("skips memory block when memoryPrompt is undefined or empty (memory not rendered anyway)", () => {
    const messages: AgentMessage[] = [{ role: "user", content: "hi" }];
    const without = inject(prompts, vars, messages);
    const withEmpty = inject(prompts, vars, messages, undefined, "");
    expect(without).toEqual(withEmpty);
    expect(withEmpty).toHaveLength(3);
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
    expect(result).toHaveLength(4);
    expect(result[0]).toEqual({ role: "system", content: "You are the NusaShell agent." });
    expect(result[1]).toEqual({ role: "system", content: "Use tool_list to discover tools." });
    expect(result[2]).toEqual({ role: "system", content: "Subagent delegation rules." });
  });

  it("does not inject TODO into any user message (todo stays a normal injected flag per REV2)", () => {
    const messages: AgentMessage[] = [{ role: "user", content: "hi" }];
    const memoryPrompt = "MEMORY (personal notes)\nremember to be concise";
    const todoPrompt = "CURRENT TASKS (agent-owned checklist)\n[ ] do the thing";
    const result = injectPrompts(prompts, vars, messages, undefined, memoryPrompt, undefined, todoPrompt).messages;
    // Memory and TODO are no longer rendered into the prompt (hydration carries them).
    expect(result).toHaveLength(3);
    expect(result[0]).toEqual({ role: "system", content: "You are the NusaShell agent." });
    expect(result[1]).toEqual({ role: "system", content: "Use tool_list to discover tools." });
    expect(result[2]).toEqual({ role: "user", content: "hi" });
    expect(result.some((m) => m.role === "system" && String(m.content).includes("MEMORY"))).toBe(false);
    expect(result.some((m) => m.role === "user" && String(m.content).includes("CURRENT TASKS"))).toBe(false);
    expect(result.some((m) => m.role === "user" && String(m.content).includes("[NUSASHELL RUNTIME CONTEXT]"))).toBe(false);
  });

  it("skips todo block when todoPrompt is undefined or empty", () => {
    const messages: AgentMessage[] = [{ role: "user", content: "hi" }];
    const without = injectPrompts(prompts, vars, messages, undefined, undefined, undefined, undefined).messages;
    const withEmpty = injectPrompts(prompts, vars, messages, undefined, undefined, undefined, "").messages;
    expect(without).toEqual(withEmpty);
    expect(withEmpty).toHaveLength(3);
  });

  it("reports hasTodo in the injection summary", () => {
    const messages: AgentMessage[] = [{ role: "user", content: "hi" }];
    const withTodo = injectPrompts(prompts, vars, messages, undefined, undefined, undefined, "CURRENT TASKS\n[ ] x");
    expect(withTodo.summary.hasTodo).toBe(true);
    const without = injectPrompts(prompts, vars, messages, undefined, undefined, undefined, undefined);
    expect(without.summary.hasTodo).toBe(false);
  });

  it("does not render skills or TODO into any system or user message (REV2)", () => {
    const messages: AgentMessage[] = [{ role: "user", content: "hi" }];
    const skills = "## Skills catalog\n- skill.x";
    const todo = "CURRENT TASKS\n[ ] finish the migration";
    const result = injectPrompts(
      prompts, vars, messages,
      undefined, undefined, undefined, todo,
      skills,
    );
    const allContents = result.messages.map((m) => String(m.content));
    expect(allContents).not.toContain(skills);
    expect(allContents).not.toContain(todo);
    expect(result.messages.some((m) => m.role === "user" && String(m.content).startsWith("[NUSASHELL RUNTIME CONTEXT]"))).toBe(false);
  });

  it("reports only the static protocol prompts as cacheable (no runtime user checkpoint), stable prefix stays 2", () => {
    const result = injectPrompts(
      prompts,
      vars,
      [{ role: "user", content: "hello" }],
      "user-specific instructions",
      "turn memory",
      "subagent rules",
      "todo",
      "skills",
      undefined,
    );

    expect(result.promptCache).toEqual({ mode: "auto", stableSystemMessages: 2 });
    // Memory/todo/skills/mcp-live are no longer rendered; only static + subagent +
    // user-instructions + developer.
    expect(result.messages.slice(0, 4).map((message) => message.content)).toEqual([
      "You are the NusaShell agent.",
      "Use tool_list to discover tools.",
      "subagent rules",
      "user-specific instructions",
    ]);
  });

  it("keeps the leading stable system prefix byte-identical across turns", () => {
    const richPrompts: AgentPrompt[] = [
      ...prompts,
      { name: "live", content: "LIVE: {{current_date}}", isTemplate: false },
    ];
    const turnA = injectPrompts(
      richPrompts, vars, [{ role: "user", content: "msg A" }],
      "instructions", "memory", "subagent", "todo", "skills",
    );
    const turnB = injectPrompts(
      richPrompts, vars, [{ role: "user", content: "msg B" }],
      "instructions", "memory", "subagent", "todo", "skills",
    );
    const stableCount = turnA.promptCache.stableSystemMessages ?? 0;
    const stableA = turnA.messages.slice(0, stableCount);
    const stableB = turnB.messages.slice(0, stableCount);
    expect(stableA).toEqual(stableB);
    expect(stableA.map((m) => m.content)).toEqual([
      "You are the NusaShell agent.",
      "Use tool_list to discover tools.",
    ]);
    expect(turnA.messages.slice(stableCount)[0]).toMatchObject({ role: "system", content: "subagent" });
  });

  it("changing a volatile var does not touch the stable prefix", () => {
    const turnA = injectPrompts(prompts, vars, [{ role: "user", content: "hi" }]);
    const varsB: PromptVars = { ...vars, availableTools: "completely different tool set" };
    const turnB = injectPrompts(prompts, varsB, [{ role: "user", content: "hi" }]);
    const stableCount = turnA.promptCache.stableSystemMessages ?? 0;
    expect(turnA.messages.slice(0, stableCount)).toEqual(turnB.messages.slice(0, stableCount));
  });

  it("uses the local machine calendar date for the supplied instant", () => {
    const originalTimeZone = process.env.TZ;
    process.env.TZ = "Asia/Jakarta";
    try {
      expect(stableCurrentDate(new Date("2026-01-01T20:00:00.000Z"))).toBe("2026-01-02");
      expect(machineCurrentTime(new Date("2026-01-01T20:00:00.000Z"))).toBe("03:00:00");
    } finally {
      if (originalTimeZone === undefined) delete process.env.TZ;
      else process.env.TZ = originalTimeZone;
    }
  });
});
