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

  it("appends an ephemeral hydration transcript after the compacted summary instead of sealing runtime text into it", async () => {
    const messages: AgentMessage[] = [
      user("Deploy the release, then run the QA checks"),
      assistant("Starting deployment"),
      tool("mcp_ops_deploy", "x".repeat(10_000)),
    ];
    const provider = new ScriptedProvider([
      { text: "Deployment completed. QA remains unfinished." },
    ]);
    const compactor = new ContextCompactor(provider, {
      compactionEnabled: true,
      maxInputTokens: 500,
      reserveTokens: 50,
      recentTurns: 1,
      summaryMaxChars: 2000,
    }, undefined);

    const result = await compactor.compact(
      {
        messages,
        pluginIds: [],
        model: "test",
        buildHydrationTranscript: async () => [
          {
            role: "assistant",
            content: "",
            toolCalls: [{ id: "hydrate:n:0", name: "mcp_list", args: {} }],
          },
          { role: "tool", toolCallId: "hydrate:n:0", name: "mcp_list", content: "{\"running\":[\"ops\"]}" },
        ],
      },
      "trace-runtime-context",
    );

    // Summary stays pure (no runtime text folded in).
    expect(result.checkpoint?.summary).not.toContain("[NUSASHELL RUNTIME CONTEXT]");
    // The hydration transcript is appended AFTER the compacted history.
    const messagesList = result.messages;
    const last = messagesList[messagesList.length - 1];
    expect(last?.role).toBe("tool");
    expect(result.messages.some((m) =>
      m.role === "assistant" && Array.isArray(m.toolCalls) && m.toolCalls.length > 0,
    )).toBe(true);
  });

  it("builds a fresh hydration transcript at the compaction boundary so an MCP change mid-turn is not sealed stale", async () => {
    const provider = new ScriptedProvider([
      { text: "The agent enabled QA and should run the remaining check." },
    ]);
    const compactor = new ContextCompactor(provider, {
      compactionEnabled: true,
      maxInputTokens: 500,
      reserveTokens: 50,
      recentTurns: 1,
      summaryMaxChars: 2000,
    }, undefined);
    let refreshed = 0;

    const result = await compactor.compact({
      messages: [user("enable QA"), tool("mcp_enable", "x".repeat(10_000))],
      pluginIds: [],
      model: "test",
      buildHydrationTranscript: async () => {
        refreshed += 1;
        return [
          {
            role: "assistant",
            content: "",
            toolCalls: [{ id: "hydrate:n:0", name: "tool_list", args: {} }],
          },
          { role: "tool", toolCallId: "hydrate:n:0", name: "tool_list", content: "{\"running\":[\"ops\",\"qa\"]}" },
        ];
      },
    }, "trace-runtime-refresh");

    expect(refreshed).toBe(1);
    // The fresh hydration transcript (with the new MCP state) sits after summary.
    const last = result.messages[result.messages.length - 1];
    expect(last?.role).toBe("tool");
    expect(result.messages.some((m) =>
      m.role === "tool" && String(m.content).includes("\"qa\""),
    )).toBe(true);
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

  it("seals TODO into the same user message as the compaction summary, before the hydration tool transcript", async () => {
    const messages: AgentMessage[] = [
      user("Deploy the release, then run the QA checks"),
      assistant("Setting up deployment"),
      tool("mcp_ops_deploy", "x".repeat(10_000)),
      tool("todo", "{ ok: true, items: [] }"),
    ];
    const provider = new ScriptedProvider([
      { text: "Deployment started. QA still pending because the queue is empty." },
    ]);
    const compactor = new ContextCompactor(provider, {
      compactionEnabled: true,
      maxInputTokens: 500,
      reserveTokens: 50,
      recentTurns: 1,
      summaryMaxChars: 2000,
    }, undefined);

    const result = await compactor.compact({
      messages,
      pluginIds: [],
      model: "test",
      todoPromptForCompaction: () =>
        "CURRENT TASKS (agent-owned checklist — user may delete items)\n[~] run QA after deployment",
      buildHydrationTranscript: async () => [
        {
          role: "assistant",
          content: "",
          toolCalls: [{ id: "hydrate:n:0", name: "mcp_list", args: {} }],
        },
        { role: "tool", toolCallId: "hydrate:n:0", name: "mcp_list", content: "{}" },
      ],
    }, "trace-todo-compact");

    // The summary user message contains BOTH the summary prefix and the TODO.
    const summaryMsg = result.messages.find((m) =>
      m.role === "user" && String(m.content).startsWith(SUMMARY_PREFIX),
    );
    expect(summaryMsg).toBeDefined();
    expect(String(summaryMsg?.content)).toContain("CURRENT TASKS");
    // Order: summary (with todo) -> hydration assistant -> hydration tool result.
    const summaryIdx = result.messages.findIndex((m) => m.role === "user" && String(m.content).startsWith(SUMMARY_PREFIX));
    expect(result.messages[summaryIdx + 1]?.role).toBe("assistant");
    expect((result.messages[summaryIdx + 1] as { toolCalls?: unknown }).toolCalls).toBeDefined();
    expect(result.messages[summaryIdx + 2]?.role).toBe("tool");
    // Summary checkpoint also carries the TODO.
    expect(result.checkpoint?.summary).toContain("CURRENT TASKS");
  });
});
