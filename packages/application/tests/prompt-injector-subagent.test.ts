import { describe, expect, it } from "vitest";
import { injectPrompts, type PromptVars } from "../src/agent/services/prompt-injector.js";
import type { AgentPrompt } from "../src/agent/ports/prompt-loader.port.js";
import type { AgentMessage } from "../src/agent/ports/agent-provider.port.js";

function inject(...args: Parameters<typeof injectPrompts>): AgentMessage[] {
  return injectPrompts(...args).messages;
}

describe("injectPrompts — subagent prompt", () => {
  const vars: PromptVars = {
    currentDate: "2026-08-03",
    environment: "test",
    runtimeOs: "linux (ubuntu)",
    availableTools: "mcp_list, subagent",
    workspace: "/tmp",
  };

  const staticPrompts: AgentPrompt[] = [
    { name: "system", content: "System prompt", isTemplate: false },
    { name: "mcp-tools", content: "MCP tools prompt", isTemplate: false },
  ];

  it("injects subagent prompt after static prompts when provided", () => {
    const messages: AgentMessage[] = [{ role: "user", content: "Hello" }];
    const result = inject(
      staticPrompts,
      vars,
      messages,
      undefined,
      undefined,
      "Subagent delegation guide",
    );
    const systemContents = result.filter((m) => m.role === "system").map((m) => m.content as string);
    expect(systemContents).toContain("Subagent delegation guide");
    const subagentIdx = systemContents.indexOf("Subagent delegation guide");
    expect(subagentIdx).toBeGreaterThan(-1);
  });

  it("does not inject subagent prompt when undefined", () => {
    const messages: AgentMessage[] = [{ role: "user", content: "Hello" }];
    const result = inject(staticPrompts, vars, messages);
    const systemContents = result.filter((m) => m.role === "system").map((m) => m.content as string);
    expect(systemContents).not.toContain("Subagent delegation guide");
  });

  it("injects subagent prompt before user prompt", () => {
    const messages: AgentMessage[] = [{ role: "user", content: "Hello" }];
    const result = inject(
      staticPrompts,
      vars,
      messages,
      "User custom prompt",
      undefined,
      "Subagent guide",
    );
    const systemContents = result.filter((m) => m.role === "system").map((m) => m.content as string);
    const subagentIdx = systemContents.indexOf("Subagent guide");
    const userIdx = systemContents.indexOf("User custom prompt");
    expect(subagentIdx).toBeGreaterThan(-1);
    expect(userIdx).toBeGreaterThan(subagentIdx);
  });
});
