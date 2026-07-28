import { describe, expect, it } from "vitest";
import {
  AgentTurnRunner,
  type AgentProvider,
  type AgentProviderRequest,
  type AgentProviderResult,
  type AgentToolGateway,
} from "../src/index.js";

class ScriptedProvider implements AgentProvider {
  readonly id = "scripted";
  readonly requests: AgentProviderRequest[] = [];

  constructor(private readonly responses: readonly AgentProviderResult[]) {}

  async complete(request: AgentProviderRequest): Promise<AgentProviderResult> {
    this.requests.push(request);
    const response = this.responses[this.requests.length - 1];
    if (!response) throw new Error("No scripted provider response");
    return response;
  }
}

class FakeToolGateway implements AgentToolGateway {
  readonly calls: Array<{ name: string; args: Readonly<Record<string, unknown>> }> = [];
  readonly begunTurns: string[] = [];
  readonly endedTurns: string[] = [];
  readonly cancelledTurns: string[] = [];

  beginTurn(turnId: string) {
    this.begunTurns.push(turnId);
  }

  endTurn(turnId: string) {
    this.endedTurns.push(turnId);
  }

  cancelTurn(turnId: string) {
    this.cancelledTurns.push(turnId);
  }

  async listTools() {
    return [{
      name: "notes.create",
      description: "Create a note",
      inputSchema: { type: "object", properties: { title: { type: "string" } } },
    }];
  }

  async execute(name: string, args: Readonly<Record<string, unknown>>) {
    this.calls.push({ name, args });
    if (name === "notes.create") return { id: "note-1" };
    throw new Error(`Unexpected tool ${name}`);
  }
}

describe("AgentTurnRunner", () => {
  it("cleans up the per-turn tool allowlist when a provider fails", async () => {
    const tools = new FakeToolGateway();
    const runner = new AgentTurnRunner({ provider: new ScriptedProvider([]), toolGateway: tools });

    await expect(runner.run({
      traceId: "trace-cleanup",
      messages: [{ role: "user", content: "Fail this turn" }],
      pluginIds: [],
    })).rejects.toThrow("AI provider request failed");

    expect(tools.begunTurns).toEqual(["trace-cleanup"]);
    expect(tools.endedTurns).toEqual(["trace-cleanup"]);
  });

  it("surfaces cancellation distinctly and asks the gateway to cancel active MCP calls", async () => {
    const controller = new AbortController();
    const tools = new FakeToolGateway();
    controller.abort();
    const runner = new AgentTurnRunner({
      provider: new ScriptedProvider([{ text: "too late" }]),
      toolGateway: tools,
    });

    await expect(runner.run({
      traceId: "trace-cancelled",
      signal: controller.signal,
      messages: [{ role: "user", content: "Stop" }],
      pluginIds: [],
    })).rejects.toMatchObject({ code: "AGENT_TURN_CANCELLED" });
    expect(tools.cancelledTurns).toEqual(["trace-cancelled"]);
    expect(tools.endedTurns).toEqual(["trace-cancelled"]);
  });

  it("returns a text-only result in one provider round", async () => {
    const provider = new ScriptedProvider([{ text: "Hello from the agent" }]);
    const runner = new AgentTurnRunner({ provider, toolGateway: new FakeToolGateway() });

    const result = await runner.run({
      messages: [{ role: "user", content: "Say hello" }],
      pluginIds: [],
    });

    expect(result.text).toBe("Hello from the agent");
    expect(result.rounds).toBe(1);
    expect(result.toolCalls).toEqual([]);
  });

  it("nudges one empty provider response before returning user-facing text", async () => {
    const provider = new ScriptedProvider([{ text: "   " }, { text: "Recovered answer" }]);
    const runner = new AgentTurnRunner({ provider, toolGateway: new FakeToolGateway(), defaultMaxToolRounds: 3 });

    const result = await runner.run({
      messages: [{ role: "user", content: "Answer me" }],
      pluginIds: [],
    });

    expect(result.text).toBe("Recovered answer");
    expect(result.rounds).toBe(2);
    expect(provider.requests[1]?.messages.at(-1)).toMatchObject({
      role: "system",
      content: expect.stringContaining("no user-facing answer"),
    });
  });

  it("returns a bounded runtime answer when the provider stays empty", async () => {
    const provider = new ScriptedProvider([{ text: "" }, { text: "" }]);
    const runner = new AgentTurnRunner({ provider, toolGateway: new FakeToolGateway(), defaultMaxToolRounds: 2 });

    const result = await runner.run({
      messages: [{ role: "user", content: "Answer me" }],
      pluginIds: [],
    });

    expect(result.text).toBe("(empty model response)");
    expect(result.rounds).toBe(2);
  });

  it("executes only an exposed MCP tool and returns its result to the next model round", async () => {
    const provider = new ScriptedProvider([
      { toolCalls: [{ id: "call-1", name: "notes.create", args: { title: "Roadmap" } }] },
      { text: "The note is ready." },
    ]);
    const tools = new FakeToolGateway();
    const runner = new AgentTurnRunner({ provider, toolGateway: tools });

    const result = await runner.run({
      messages: [{ role: "user", content: "Create a roadmap note" }],
      pluginIds: ["notes"],
    });

    expect(result.text).toBe("The note is ready.");
    expect(tools.calls).toEqual([{ name: "notes.create", args: { title: "Roadmap" } }]);
    expect(provider.requests[1]?.messages.at(-1)).toEqual({
      role: "tool",
      toolCallId: "call-1",
      name: "notes.create",
      content: JSON.stringify({ ok: true, result: { id: "note-1" } }),
    });
  });

  it("does not execute a tool that is outside the MCP allowlist", async () => {
    const provider = new ScriptedProvider([
      { toolCalls: [{ id: "call-1", name: "filesystem.delete", args: { path: "/tmp/a" } }] },
    ]);
    const tools = new FakeToolGateway();
    const runner = new AgentTurnRunner({ provider, toolGateway: tools });

    await expect(runner.run({
      messages: [{ role: "user", content: "Delete a file" }],
      pluginIds: ["notes"],
    })).rejects.toMatchObject({ code: "AGENT_TOOL_NOT_ALLOWED" });
    expect(tools.calls).toEqual([]);
  });

  it("returns a bounded runtime answer when the provider exceeds the tool-round limit", async () => {
    const provider = new ScriptedProvider([
      { toolCalls: [{ id: "call-1", name: "notes.create", args: { title: "One" } }] },
      { toolCalls: [{ id: "call-2", name: "notes.create", args: { title: "Two" } }] },
    ]);
    const runner = new AgentTurnRunner({ provider, toolGateway: new FakeToolGateway() });

    const result = await runner.run({
      messages: [{ role: "user", content: "Keep creating notes" }],
      pluginIds: ["notes"],
      maxToolRounds: 1,
    });

    expect(result.text).toContain("maximum tool rounds");
    expect(result.rounds).toBe(1);
  });

  it("compacts old turns while preserving recent turns and returns a durable checkpoint", async () => {
    const old = "old context ".repeat(1200);
    const provider = new ScriptedProvider([
      { text: "A concise checkpoint" },
      { text: "Final answer" },
    ]);
    const runner = new AgentTurnRunner({
      provider,
      toolGateway: new FakeToolGateway(),
      context: {
        compactionEnabled: true,
        maxInputTokens: 1200,
        reserveTokens: 200,
        recentTurns: 1,
        summaryMaxChars: 2000,
      },
    });

    const result = await runner.run({
      messages: [
        { role: "user", content: old },
        { role: "assistant", content: "old answer" },
        { role: "user", content: "latest question" },
      ],
      pluginIds: [],
      model: "test-model",
    });

    expect(provider.requests).toHaveLength(2);
    expect(provider.requests[0]?.tools).toEqual([]);
    expect(provider.requests[1]?.messages).toEqual([
      { role: "system", content: "Conversation summary:\nA concise checkpoint" },
      { role: "user", content: "latest question" },
    ]);
    expect(result.compaction).toMatchObject({
      summary: "A concise checkpoint",
      compactedMessageCount: 2,
      via: "provider",
    });
  });

  it("uses an extractive checkpoint when compaction provider request fails", async () => {
    class FailingSummaryProvider extends ScriptedProvider {
      override async complete(request: AgentProviderRequest): Promise<AgentProviderResult> {
        this.requests.push(request);
        if (this.requests.length === 1) throw new Error("summary unavailable");
        return { text: "Final answer" };
      }
    }
    const provider = new FailingSummaryProvider([]);
    const runner = new AgentTurnRunner({
      provider,
      toolGateway: new FakeToolGateway(),
      context: {
        compactionEnabled: true,
        maxInputTokens: 1200,
        reserveTokens: 200,
        recentTurns: 1,
        summaryMaxChars: 500,
      },
    });

    const result = await runner.run({
      messages: [
        { role: "user", content: "important old instruction ".repeat(1000) },
        { role: "assistant", content: "old answer" },
        { role: "user", content: "latest" },
      ],
      pluginIds: [],
    });

    expect(result.compaction?.via).toBe("extractive");
    expect(result.compaction?.summary).toContain("User:");
    expect(result.text).toBe("Final answer");
  });

  it("uses the selected model context window and output reserve for compaction", async () => {
    const provider = new ScriptedProvider([
      { text: "Model-aware checkpoint" },
      { text: "Final answer" },
    ]);
    const runner = new AgentTurnRunner({
      provider,
      toolGateway: new FakeToolGateway(),
      context: {
        compactionEnabled: true,
        maxInputTokens: 20_000,
        reserveTokens: 500,
        recentTurns: 1,
        summaryMaxChars: 2000,
      },
    });

    await runner.run({
      messages: [
        { role: "user", content: "old ".repeat(1700) },
        { role: "assistant", content: "old answer" },
        { role: "user", content: "latest" },
      ],
      pluginIds: [],
      model: "small-context-model",
      modelCapabilities: { contextWindow: 2000, maxOutput: 500 },
    });

    expect(provider.requests).toHaveLength(2);
    expect(provider.requests[0]?.round).toBe(0);
  });

  it("nudges a repeated identical tool call once and stops it on the third occurrence", async () => {
    const repeated = (id: string): AgentProviderResult => ({
      toolCalls: [{ id, name: "notes.create", args: { title: "Same" } }],
    });
    const provider = new ScriptedProvider([repeated("call-1"), repeated("call-2"), repeated("call-3")]);
    const tools = new FakeToolGateway();
    const runner = new AgentTurnRunner({ provider, toolGateway: tools, defaultMaxToolRounds: 4 });

    const result = await runner.run({
      messages: [{ role: "user", content: "Create one note" }],
      pluginIds: ["notes"],
    });

    expect(tools.calls).toEqual([{ name: "notes.create", args: { title: "Same" } }]);
    expect(provider.requests[2]?.messages.at(-1)).toMatchObject({
      role: "system",
      content: expect.stringContaining("repeating the same tool call"),
    });
    expect(result.text).toContain("repeated the same tool call");
  });

  it("nudges a reasoning-only response and returns usage metadata", async () => {
    const provider = new ScriptedProvider([
      {
        reasoning: "I should answer clearly.",
        usage: {
          inputTokens: 10,
          outputTokens: 5,
          cachedInputTokens: 2,
          cacheWriteTokens: 0,
          reasoningOutputTokens: 5,
        },
      },
      {
        text: "Here is the answer.",
        providerId: "provider-a",
        api: "responses",
        usage: {
          inputTokens: 12,
          outputTokens: 7,
          cachedInputTokens: 0,
          cacheWriteTokens: 0,
          reasoningOutputTokens: 0,
        },
      },
    ]);
    const runner = new AgentTurnRunner({ provider, toolGateway: new FakeToolGateway() });

    const result = await runner.run({
      messages: [{ role: "user", content: "Answer" }],
      pluginIds: [],
    });

    expect(provider.requests[1]?.messages.at(-1)).toMatchObject({
      role: "system",
      content: expect.stringContaining("reasoning but no user-facing answer"),
    });
    expect(result).toMatchObject({
      text: "Here is the answer.",
      providerId: "provider-a",
      api: "responses",
      usage: {
        inputTokens: 22,
        outputTokens: 12,
        cachedInputTokens: 2,
        reasoningOutputTokens: 5,
      },
    });
  });
});
