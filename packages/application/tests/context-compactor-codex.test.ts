import { describe, expect, it } from "vitest";
import type { AgentMessage, AgentProvider, AgentProviderRequest, AgentProviderResult } from "../src/index.js";
import { ContextCompactor } from "../src/agent/services/agent-context-compaction.js";
import { SUMMARY_PREFIX, isSummaryMessage } from "../src/agent/services/compact-history.js";

class ScriptedProvider implements AgentProvider {
  readonly id = "scripted";
  readonly requests: AgentProviderRequest[] = [];
  constructor(private readonly responses: readonly AgentProviderResult[]) {}

  async complete(request: AgentProviderRequest): Promise<AgentProviderResult> {
    this.requests.push(request);
    const response = this.responses[this.requests.length - 1];
    if (!response) throw new Error("no more scripted responses");
    return response;
  }
}

const user = (content: string): AgentMessage => ({ role: "user", content });
const assistant = (content: string): AgentMessage => ({ role: "assistant", content });
const tool = (name: string, content: string): AgentMessage => ({ role: "tool", toolCallId: "1", name, content });

describe("ContextCompactor (Codex-aligned memento)", () => {
  it("retains the original user goal wording after a fat tool turn + follow-up", async () => {
    // Reproduces the 00b549a5 bug class: a 70+ tool-round first turn, then a
    // follow-up question. The compacted context must still contain the
    // original user goal text.
    const originalGoal = "Fix the cookie handling in curl.go so that updateUrlFromParams preserves session cookies across redirects";
    const fatToolResult = "x".repeat(20_000);
    const messages: AgentMessage[] = [
      user(originalGoal),
      assistant("Starting investigation"),
      tool("read", fatToolResult),
      tool("read", fatToolResult),
      tool("read", fatToolResult),
      assistant("Found the issue in curl.go"),
      user("lanjut investigasi — cek updateUrlFromParams"),
    ];

    const provider = new ScriptedProvider([
      { text: "Investigated curl.go cookie handling. Found that updateUrlFromParams drops session cookies on redirect. Next: fix the cookie jar persistence." },
    ]);
    const compactor = new ContextCompactor(provider, {
      compactionEnabled: true,
      maxInputTokens: 1000,
      reserveTokens: 100,
      recentTurns: 1,
      summaryMaxChars: 2000,
    }, undefined);

    const result = await compactor.compact(
      { messages, pluginIds: [], model: "test" },
      "trace-1",
    );

    // The compacted messages must include the original user goal text.
    const userContents = result.messages
      .filter((m) => m.role === "user")
      .map((m) => String(m.content));
    expect(userContents.some((c) => c.includes("Fix the cookie handling in curl.go"))).toBe(true);

    // The summary must be a user message with the SUMMARY_PREFIX marker.
    expect(result.checkpoint?.summary.startsWith(SUMMARY_PREFIX)).toBe(true);
    expect(result.checkpoint?.via).toBe("provider");

    // The retainedUserMessages must include the original goal.
    expect(result.checkpoint?.retainedUserMessages).toContain(originalGoal);
  });

  it("falls back to extractive excerpt when the provider body is too short", async () => {
    const messages: AgentMessage[] = [
      user("Do something important with files"),
      assistant("ok"),
      tool("read", "x".repeat(10_000)),
      user("follow up"),
    ];

    const provider = new ScriptedProvider([
      { text: "short" }, // below MIN_SUMMARY_CHARS (80)
    ]);
    const compactor = new ContextCompactor(provider, {
      compactionEnabled: true,
      maxInputTokens: 500,
      reserveTokens: 50,
      recentTurns: 1,
      summaryMaxChars: 1000,
    }, undefined);

    const result = await compactor.compact(
      { messages, pluginIds: [], model: "test" },
      "trace-2",
    );

    expect(result.checkpoint?.via).toBe("extractive");
    // The summary must still contain evidence (not a solitary one-line ghost).
    expect(result.checkpoint?.summary.length).toBeGreaterThan(80);
    expect(result.checkpoint?.summary.startsWith(SUMMARY_PREFIX)).toBe(true);
  });

  it("does not stack summaries: collectUserMessages skips prior SUMMARY_PREFIX markers", async () => {
    const priorSummary = `${SUMMARY_PREFIX}\nPrior checkpoint body that is long enough to pass the quality gate for testing purposes.`;
    const messages: AgentMessage[] = [
      user(priorSummary), // prior compaction summary
      user("new question after compaction"),
      assistant("answer"),
      tool("read", "x".repeat(10_000)),
    ];

    const provider = new ScriptedProvider([
      { text: "New checkpoint summarizing the work after the prior compaction for the next model to continue." },
    ]);
    const compactor = new ContextCompactor(provider, {
      compactionEnabled: true,
      maxInputTokens: 500,
      reserveTokens: 50,
      recentTurns: 1,
      summaryMaxChars: 2000,
    }, undefined);

    const result = await compactor.compact(
      { messages, pluginIds: [], model: "test" },
      "trace-3",
    );

    // The retained user messages must NOT include the prior summary.
    const retained = result.checkpoint?.retainedUserMessages ?? [];
    expect(retained.some((m) => isSummaryMessage(m))).toBe(false);
    expect(retained).toContain("new question after compaction");
  });

  it("preserves leading system injects at the head of the replacement", async () => {
    const messages: AgentMessage[] = [
      { role: "system", content: "system.md" },
      { role: "system", content: "mcp-tools.md" },
      user("important goal"),
      assistant("work"),
      tool("read", "x".repeat(10_000)),
      user("follow up"),
    ];

    const provider = new ScriptedProvider([
      { text: "Checkpoint summarizing the work for the next model to continue the task." },
    ]);
    const compactor = new ContextCompactor(provider, {
      compactionEnabled: true,
      maxInputTokens: 500,
      reserveTokens: 50,
      recentTurns: 1,
      summaryMaxChars: 2000,
    }, undefined);

    const result = await compactor.compact(
      { messages, pluginIds: [], model: "test" },
      "trace-4",
    );

    // Leading system messages must be preserved at the head.
    expect(result.messages[0]).toMatchObject({ role: "system", content: "system.md" });
    expect(result.messages[1]).toMatchObject({ role: "system", content: "mcp-tools.md" });
  });

  it("returns input unchanged when under threshold", async () => {
    const messages: AgentMessage[] = [
      user("hi"),
      assistant("hello"),
    ];

    const provider = new ScriptedProvider([]);
    const compactor = new ContextCompactor(provider, {
      compactionEnabled: true,
      maxInputTokens: 10_000,
      reserveTokens: 1000,
      recentTurns: 1,
      summaryMaxChars: 2000,
    }, undefined);

    const result = await compactor.compact(
      { messages, pluginIds: [], model: "test" },
      "trace-5",
    );

    expect(result.messages).toBe(messages);
    expect(result.checkpoint).toBeUndefined();
    expect(provider.requests).toHaveLength(0);
  });
});
