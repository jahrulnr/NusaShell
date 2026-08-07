import { describe, expect, it, vi } from "vitest";
import {
  AgentTurnRunner,
  resolveModelContextDefaults,
  DEFAULT_UNKNOWN_CONTEXT_WINDOW,
  SUMMARY_PREFIX,
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

class FlakyProvider implements AgentProvider {
  readonly id = "flaky";
  readonly requests: AgentProviderRequest[] = [];
  private queue: (AgentProviderResult | Error)[];

  constructor(
    responses: readonly (AgentProviderResult | Error)[],
    private readonly onCall?: (index: number) => void,
  ) {
    this.queue = [...responses];
  }

  async complete(request: AgentProviderRequest): Promise<AgentProviderResult> {
    const index = this.requests.length;
    this.requests.push(request);
    this.onCall?.(index);
    const next = this.queue.shift();
    if (next instanceof Error) throw next;
    if (!next) throw new Error("No scripted provider response");
    return next;
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

  async execute(name: string, args: Readonly<Record<string, unknown>>): Promise<unknown> {
    this.calls.push({ name, args });
    if (name === "notes.create") return { id: "note-1" };
    throw new Error(`Unexpected tool ${name}`);
  }
}

/**
 * Gateway that defers each tool call, allowing tests to observe overlap and
 * control completion order. Records the start order and the completion order.
 */
class DeferredToolGateway implements AgentToolGateway {
  readonly startOrder: string[] = [];
  readonly endOrder: string[] = [];
  private readonly pending = new Map<string, { resolve: (value: unknown) => void; reject: (error: Error) => void }>();
  private readonly toolNames: readonly string[];

  constructor(toolNames: readonly string[] = ["tool_a", "tool_b", "tool_pre", "tool_post", "ask_question"]) {
    this.toolNames = toolNames;
  }

  beginTurn() {}
  endTurn() {}
  cancelTurn() {
    for (const [callId, entry] of this.pending) {
      this.pending.delete(callId);
      this.endOrder.push(callId);
      entry.reject(new Error("Tool call cancelled"));
    }
  }

  async listTools() {
    return this.toolNames.map((name) => ({
      name,
      description: `Test tool ${name}`,
      inputSchema: { type: "object", properties: {} },
    }));
  }

  async execute(_name: string, _args: Readonly<Record<string, unknown>>, _requestId: string, _turnId: string, callId: string): Promise<unknown> {
    this.startOrder.push(callId);
    return new Promise((resolve, reject) => {
      this.pending.set(callId, { resolve, reject });
    });
  }

  complete(callId: string, result: unknown): void {
    const entry = this.pending.get(callId);
    if (entry) {
      this.pending.delete(callId);
      this.endOrder.push(callId);
      entry.resolve(result);
    }
  }

  fail(callId: string, error: Error): void {
    const entry = this.pending.get(callId);
    if (entry) {
      this.pending.delete(callId);
      this.endOrder.push(callId);
      entry.reject(error);
    }
  }

  isStarted(callId: string): boolean {
    return this.startOrder.includes(callId);
  }

  isPending(callId: string): boolean {
    return this.pending.has(callId);
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
      content: JSON.stringify({ ok: true, data: { id: "note-1" }, meta: { truncated: false } }),
    });
  });

  it("records steps in provider order: reasoning → text → tools", async () => {
    const provider = new ScriptedProvider([
      {
        reasoning: "I should create the note first.",
        text: "Creating the roadmap note now.",
        toolCalls: [{ id: "call-1", name: "notes.create", args: { title: "Roadmap" } }],
      },
      {
        reasoning: "The tool succeeded.",
        text: "The note is ready.",
      },
    ]);
    const runner = new AgentTurnRunner({ provider, toolGateway: new FakeToolGateway() });

    const result = await runner.run({
      messages: [{ role: "user", content: "Create a roadmap note" }],
      pluginIds: ["notes"],
    });

    expect(result.steps).toEqual([
      { type: "reasoning", content: "I should create the note first." },
      { type: "text", content: "Creating the roadmap note now." },
      {
        type: "tool_calls",
        calls: [expect.objectContaining({
          id: "call-1",
          name: "notes.create",
          ok: true,
          args: { title: "Roadmap" },
          result: { id: "note-1" },
        })],
      },
      { type: "reasoning", content: "The tool succeeded." },
      { type: "text", content: "The note is ready." },
    ]);
  });

  it("soft-rejects a tool outside the MCP allowlist and continues the turn", async () => {
    const provider = new ScriptedProvider([
      { toolCalls: [{ id: "call-1", name: "filesystem.delete", args: { path: "/tmp/a" } }] },
      { text: "Done." },
    ]);
    const tools = new FakeToolGateway();
    const runner = new AgentTurnRunner({ provider, toolGateway: tools });

    const result = await runner.run({
      messages: [{ role: "user", content: "Delete a file" }],
      pluginIds: ["notes"],
    });

    expect(result.text).toBe("Done.");
    expect(result.toolCalls).toHaveLength(1);
    expect(result.toolCalls[0]).toMatchObject({ id: "call-1", name: "filesystem.delete", ok: false });
    expect(result.toolCalls[0]?.error).toContain("filesystem.delete");
    expect(result.toolCalls[0]?.error).toContain("not");
    expect(tools.calls).toEqual([]);
  });

  it("soft-rejects an unknown tool after prior progress and continues", async () => {
    const provider = new ScriptedProvider([
      { toolCalls: [{ id: "call-1", name: "notes.create", args: { title: "Roadmap" } }] },
      { toolCalls: [{ id: "call-2", name: "filesystem.delete", args: { path: "/tmp/a" } }] },
      { text: "Finished." },
    ]);
    const tools = new FakeToolGateway();
    const runner = new AgentTurnRunner({ provider, toolGateway: tools });

    const result = await runner.run({
      messages: [{ role: "user", content: "Create then delete" }],
      pluginIds: ["notes"],
    });

    expect(result.text).toBe("Finished.");
    expect(result.toolCalls).toHaveLength(2);
    expect(result.toolCalls[0]).toMatchObject({ id: "call-1", name: "notes.create", ok: true });
    expect(result.toolCalls[1]).toMatchObject({ id: "call-2", name: "filesystem.delete", ok: false });
    expect(tools.calls).toEqual([{ name: "notes.create", args: { title: "Roadmap" } }]);
  });

  it("soft-rejects unknown tools in a mixed batch while running valid ones", async () => {
    const provider = new ScriptedProvider([
      { toolCalls: [
        { id: "call-1", name: "ReadFile", args: { path: "/tmp/a" } },
        { id: "call-2", name: "notes.create", args: { title: "Mixed" } },
      ] },
      { text: "Mixed done." },
    ]);
    const tools = new FakeToolGateway();
    const runner = new AgentTurnRunner({ provider, toolGateway: tools });

    const result = await runner.run({
      messages: [{ role: "user", content: "Read and create" }],
      pluginIds: ["notes"],
    });

    expect(result.text).toBe("Mixed done.");
    expect(result.toolCalls).toHaveLength(2);
    // Provider order preserved
    expect(result.toolCalls[0]).toMatchObject({ id: "call-1", name: "ReadFile", ok: false });
    expect(result.toolCalls[1]).toMatchObject({ id: "call-2", name: "notes.create", ok: true });
    // Only the known tool was dispatched to the gateway
    expect(tools.calls).toEqual([{ name: "notes.create", args: { title: "Mixed" } }]);
  });

  it("forwards an unadvertised mcp_* plugin tool name to the gateway for lazy resolve", async () => {
    const provider = new ScriptedProvider([
      { toolCalls: [{ id: "call-1", name: "mcp_nusashell_createNote", args: { text: "hi" } }] },
      { text: "Done." },
    ]);
    const tools = new FakeToolGateway();
    tools.execute = async (name, args): Promise<unknown> => {
      tools.calls.push({ name, args });
      if (name === "mcp_nusashell_createNote") return { ok: true };
      throw new Error(`Unexpected tool ${name}`);
    };
    const runner = new AgentTurnRunner({ provider, toolGateway: tools });

    const result = await runner.run({
      messages: [{ role: "user", content: "Create a note" }],
      pluginIds: ["notes"],
    });

    expect(result.text).toBe("Done.");
    expect(result.toolCalls).toHaveLength(1);
    expect(result.toolCalls[0]).toMatchObject({ id: "call-1", name: "mcp_nusashell_createNote", ok: true });
    expect(tools.calls).toEqual([{ name: "mcp_nusashell_createNote", args: { text: "hi" } }]);
  });

  it("returns a bounded runtime answer when the provider exceeds the tool-round limit", async () => {
    const provider = new ScriptedProvider([
      { toolCalls: [{ id: "call-1", name: "notes.create", args: { title: "One" } }] },
      { toolCalls: [{ id: "call-2", name: "notes.create", args: { title: "Two" } }] },
    ]);
    const runner = new AgentTurnRunner({ provider, toolGateway: new FakeToolGateway() });

    let error: unknown;
    try {
      await runner.run({
        messages: [{ role: "user", content: "Keep creating notes" }],
        pluginIds: ["notes"],
        maxToolRounds: 1,
      });
    } catch (e) {
      error = e;
    }
    expect(error).toMatchObject({
      code: "AGENT_MAX_TOOL_ROUNDS",
      details: {
        limit: 1,
        partial: expect.objectContaining({
          rounds: 1,
          toolCalls: expect.any(Array),
          messages: expect.any(Array),
        }),
      },
    });
    const partial = (error as { details: { partial: { toolCalls: unknown[] } } }).details.partial;
    expect(partial.toolCalls.length).toBeGreaterThan(0);
  });

  it("does not throw AGENT_MAX_TOOL_ROUNDS when maxToolRounds is 0 (unlimited)", async () => {
    // Provider does 3 tool rounds then a final answer. With maxToolRounds=0
    // the loop must not hit a ceiling — it runs until the final answer.
    const provider = new ScriptedProvider([
      { toolCalls: [{ id: "call-1", name: "notes.create", args: { title: "One" } }] },
      { toolCalls: [{ id: "call-2", name: "notes.create", args: { title: "Two" } }] },
      { toolCalls: [{ id: "call-3", name: "notes.create", args: { title: "Three" } }] },
      { text: "Done creating notes" },
    ]);
    const runner = new AgentTurnRunner({ provider, toolGateway: new FakeToolGateway() });
    const result = await runner.run({
      messages: [{ role: "user", content: "Keep creating notes" }],
      pluginIds: ["notes"],
      maxToolRounds: 0,
    });
    expect(result.text).toBe("Done creating notes");
    expect(result.rounds).toBe(4);
  });

  it("compacts old turns while preserving recent turns and returns a durable checkpoint", async () => {
    const old = "old context ".repeat(1200);
    const provider = new ScriptedProvider([
      { text: "A concise checkpoint summarizing the prior work and decisions for the next model." },
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
    // Codex-aligned: replacement history is retained user messages + one
    // summary user message (SUMMARY_PREFIX + body), not a system summary.
    const summaryPrefix = SUMMARY_PREFIX;
    expect(result.compaction?.summary).toBe(`${summaryPrefix}\nA concise checkpoint summarizing the prior work and decisions for the next model.`);
    expect(result.compaction?.via).toBe("provider");
    // The second request (the actual turn) must contain the retained user
    // message and the summary user message.
    const turnMessages = provider.requests[1]?.messages ?? [];
    const userContents = turnMessages.filter((m) => m.role === "user").map((m) => String(m.content));
    expect(userContents).toContain("latest question");
    expect(userContents.some((c) => c.startsWith(summaryPrefix))).toBe(true);
    expect(userContents.some((c) => c.includes("A concise checkpoint summarizing the prior work"))).toBe(true);
    // No system "Conversation summary:" marker anymore.
    expect(turnMessages.some((m) => m.role === "system" && String(m.content).startsWith("Conversation summary:"))).toBe(false);
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

  it("preserves tool args, reasoning, and a slice of tool outcomes in the extractive compaction summary", async () => {
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
        summaryMaxChars: 20_000,
      },
    });

    const oldToolOutput = "wrote 2 bytes to /a.txt";
    const result = await runner.run({
      messages: [
        { role: "user", content: "write a scratch file. ".repeat(400) },
        {
          role: "assistant",
          content: "Done.",
          reasoning: "I chose /a.txt because the user asked for a scratch file.",
          toolCalls: [{ id: "call-1", name: "write", args: { path: "/a.txt", content: "hi" } }],
        },
        { role: "tool", toolCallId: "call-1", name: "write", content: oldToolOutput },
        { role: "user", content: "latest question" },
      ],
      pluginIds: [],
    });

    expect(result.compaction?.via).toBe("extractive");
    const summary = result.compaction?.summary ?? "";
    expect(summary).toContain("write(");
    expect(summary).toContain("/a.txt");
    expect(summary).toContain("Reasoning:");
    expect(summary).toContain("scratch file");
    expect(summary).toContain(oldToolOutput);
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
    const runner = new AgentTurnRunner({ provider, toolGateway: tools, defaultMaxToolRounds: 4, defaultMaxRepeatedToolCalls: 3 });

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

  it("throws without details.partial when the provider fails on round 1 with no tool progress", async () => {
    const provider = new FlakyProvider([new Error("boom")]);
    const runner = new AgentTurnRunner({ provider, toolGateway: new FakeToolGateway(), softRecoverAttempts: 1 });

    const error = await runner.run({
      messages: [{ role: "user", content: "Fail immediately" }],
      pluginIds: [],
    }).catch((e) => e);

    expect(error).toMatchObject({ code: "AGENT_PROVIDER_FAILED" });
    expect(error.details?.partial).toBeUndefined();
  });

  it("soft-recovers after a tool round succeeds and the next provider call fails", async () => {
    const provider = new FlakyProvider([
      { toolCalls: [{ id: "call-1", name: "notes.create", args: { title: "Roadmap" } }] },
      new Error("transient boom"),
      { text: "Recovered after soft retry" },
    ]);
    const tools = new FakeToolGateway();
    const runner = new AgentTurnRunner({ provider, toolGateway: tools, softRecoverAttempts: 1 });

    const result = await runner.run({
      messages: [{ role: "user", content: "Create a note then answer" }],
      pluginIds: ["notes"],
    });

    expect(result.text).toBe("Recovered after soft retry");
    expect(result.rounds).toBe(2);
    expect(provider.requests).toHaveLength(3);
    expect(tools.calls).toEqual([{ name: "notes.create", args: { title: "Roadmap" } }]);
    expect(provider.requests[2]?.messages).toEqual([
      { role: "user", content: "Create a note then answer" },
      { role: "assistant", toolCalls: [{ id: "call-1", name: "notes.create", args: { title: "Roadmap" } }] },
      {
        role: "tool",
        toolCallId: "call-1",
        name: "notes.create",
        content: expect.stringContaining("note-1"),
      },
    ]);
  });

  it("throws with details.partial containing tool messages when soft recover is exhausted", async () => {
    const provider = new FlakyProvider([
      { toolCalls: [{ id: "call-1", name: "notes.create", args: { title: "Roadmap" } }] },
      new Error("fail once"),
      new Error("fail again"),
    ]);
    const tools = new FakeToolGateway();
    const runner = new AgentTurnRunner({ provider, toolGateway: tools, softRecoverAttempts: 1 });

    const error = await runner.run({
      messages: [{ role: "user", content: "Create a note then answer" }],
      pluginIds: ["notes"],
    }).catch((e) => e);

    expect(error).toMatchObject({ code: "AGENT_PROVIDER_FAILED" });
    expect(error.details?.partial).toBeDefined();
    expect(error.details.partial.rounds).toBe(1);
    expect(error.details.partial.toolCalls).toHaveLength(1);
    expect(error.details.partial.messages).toEqual(expect.arrayContaining([
      expect.objectContaining({ role: "tool", toolCallId: "call-1" }),
    ]));
  });

  it("attaches details.partial when cancelled after prior tool progress", async () => {
    const controller = new AbortController();
    const provider = new FlakyProvider([
      { toolCalls: [{ id: "call-1", name: "notes.create", args: { title: "Roadmap" } }] },
      new Error("fail once"),
      new Error("fail again"),
    ], (index) => {
      if (index === 2) controller.abort();
    });
    const tools = new FakeToolGateway();
    const runner = new AgentTurnRunner({ provider, toolGateway: tools, softRecoverAttempts: 1 });

    const error = await runner.run({
      messages: [{ role: "user", content: "Create a note" }],
      pluginIds: ["notes"],
      signal: controller.signal,
    }).catch((e) => e);

    expect(error).toMatchObject({ code: "AGENT_TURN_CANCELLED" });
    expect(error.details?.partial).toBeDefined();
    expect(error.details.partial.rounds).toBe(1);
    expect(error.details.partial.toolCalls).toHaveLength(1);
    expect(error.details.partial.messages).toEqual(expect.arrayContaining([
      expect.objectContaining({ role: "tool", toolCallId: "call-1" }),
    ]));
  });

  it("cancels without partial when aborted before any tool progress", async () => {
    const controller = new AbortController();
    const provider = new FlakyProvider([
      new Error("boom"),
    ], () => {
      controller.abort();
    });
    const runner = new AgentTurnRunner({
      provider,
      toolGateway: new FakeToolGateway(),
      softRecoverAttempts: 0,
    });

    const error = await runner.run({
      messages: [{ role: "user", content: "Fail immediately" }],
      pluginIds: [],
      signal: controller.signal,
    }).catch((e) => e);

    expect(error).toMatchObject({ code: "AGENT_TURN_CANCELLED" });
    expect(error.details?.partial).toBeUndefined();
  });

  it("executes two tool calls in a round concurrently (overlap)", async () => {
    const tools = new DeferredToolGateway();
    const provider = new ScriptedProvider([
      { toolCalls: [
        { id: "call-a", name: "tool_a", args: {} },
        { id: "call-b", name: "tool_b", args: {} },
      ] },
      { text: "Done" },
    ]);
    const runner = new AgentTurnRunner({ provider, toolGateway: tools, maxConcurrentToolCalls: 8 });

    const turnPromise = runner.run({
      messages: [{ role: "user", content: "Run both" }],
      pluginIds: [],
    });

    // Wait for both to start (concurrent dispatch).
    await vi.waitFor(() => {
      expect(tools.isStarted("call-a")).toBe(true);
      expect(tools.isStarted("call-b")).toBe(true);
    });

    // Complete in reverse order to prove order is preserved in results.
    tools.complete("call-b", { b: 1 });
    tools.complete("call-a", { a: 1 });

    const result = await turnPromise;
    expect(result.text).toBe("Done");
    expect(result.toolCalls.map((tc) => tc.id)).toEqual(["call-a", "call-b"]);
    // Tool messages in messages array preserve call order.
    const toolMessages = result.messages!.filter((m) => m.role === "tool");
    expect(toolMessages.map((m) => m.toolCallId)).toEqual(["call-a", "call-b"]);
  });

  it("preserves call order in steps despite reverse completion", async () => {
    const tools = new DeferredToolGateway(["tool_first", "tool_second"]);
    const provider = new ScriptedProvider([
      { toolCalls: [
        { id: "first", name: "tool_first", args: {} },
        { id: "second", name: "tool_second", args: {} },
      ] },
      { text: "Done" },
    ]);
    const runner = new AgentTurnRunner({ provider, toolGateway: tools, maxConcurrentToolCalls: 8 });

    const turnPromise = runner.run({
      messages: [{ role: "user", content: "Run both" }],
      pluginIds: [],
    });

    await vi.waitFor(() => expect(tools.isStarted("first")).toBe(true));
    tools.complete("second", "s");
    tools.complete("first", "f");

    const result = await turnPromise;
    const toolStep = result.steps!.find((s) => s.type === "tool_calls");
    expect(toolStep?.type).toBe("tool_calls");
    if (toolStep?.type === "tool_calls") {
      expect(toolStep.calls.map((c) => c.id)).toEqual(["first", "second"]);
    }
  });

  it("does not overlap a barrier tool (ask_question) with neighbors", async () => {
    const tools = new DeferredToolGateway();
    const provider = new ScriptedProvider([
      { toolCalls: [
        { id: "pre", name: "tool_pre", args: {} },
        { id: "ask", name: "ask_question", args: { question: "q", options: [{ id: "y", label: "Y" }] } },
        { id: "post", name: "tool_post", args: {} },
      ] },
      { text: "Done" },
    ]);
    const runner = new AgentTurnRunner({ provider, toolGateway: tools, maxConcurrentToolCalls: 8 });

    const turnPromise = runner.run({
      messages: [{ role: "user", content: "Run all three" }],
      pluginIds: [],
    });

    // "pre" starts first (parallel segment of 1); "ask" must NOT start until "pre" completes.
    await vi.waitFor(() => expect(tools.isStarted("pre")).toBe(true));
    expect(tools.isStarted("ask")).toBe(false);
    tools.complete("pre", "p");

    // Now "ask" (barrier) starts; "post" must NOT start until "ask" completes.
    await vi.waitFor(() => expect(tools.isStarted("ask")).toBe(true));
    expect(tools.isStarted("post")).toBe(false);
    tools.complete("ask", { answer: "Y" });

    // Now "post" starts.
    await vi.waitFor(() => expect(tools.isStarted("post")).toBe(true));
    tools.complete("post", "d");

    const result = await turnPromise;
    expect(result.toolCalls.map((tc) => tc.id)).toEqual(["pre", "ask", "post"]);
  });

  it("fills cancelled stubs for all calls when aborted mid-batch", async () => {
    const controller = new AbortController();
    const tools = new DeferredToolGateway();
    const provider = new ScriptedProvider([
      { toolCalls: [
        { id: "call-a", name: "tool_a", args: {} },
        { id: "call-b", name: "tool_b", args: {} },
      ] },
      { text: "Done" },
    ]);
    const runner = new AgentTurnRunner({ provider, toolGateway: tools, maxConcurrentToolCalls: 8 });

    const turnPromise = runner.run({
      messages: [{ role: "user", content: "Run both" }],
      pluginIds: [],
      signal: controller.signal,
    });

    await vi.waitFor(() => {
      expect(tools.isStarted("call-a")).toBe(true);
      expect(tools.isStarted("call-b")).toBe(true);
    });

    controller.abort();
    // The turn should reject with AGENT_TURN_CANCELLED.
    const error = await turnPromise.catch((e) => e);
    expect(error).toMatchObject({ code: "AGENT_TURN_CANCELLED" });
  });

  it("runs sequentially when maxConcurrentToolCalls is 1", async () => {
    const tools = new DeferredToolGateway();
    const provider = new ScriptedProvider([
      { toolCalls: [
        { id: "call-a", name: "tool_a", args: {} },
        { id: "call-b", name: "tool_b", args: {} },
      ] },
      { text: "Done" },
    ]);
    const runner = new AgentTurnRunner({ provider, toolGateway: tools, maxConcurrentToolCalls: 1 });

    const turnPromise = runner.run({
      messages: [{ role: "user", content: "Run both" }],
      pluginIds: [],
    });

    // "call-a" starts; "call-b" must NOT start until "call-a" completes.
    await vi.waitFor(() => expect(tools.isStarted("call-a")).toBe(true));
    expect(tools.isStarted("call-b")).toBe(false);
    tools.complete("call-a", "a");

    await vi.waitFor(() => expect(tools.isStarted("call-b")).toBe(true));
    tools.complete("call-b", "b");

    const result = await turnPromise;
    expect(result.toolCalls.map((tc) => tc.id)).toEqual(["call-a", "call-b"]);
  });

  // --- Token-first context budget (Codex-aligned) ---

  it("compacts fat few-user-turns conversations despite recentTurns veto (anti-veto)", async () => {
    const fat = "x".repeat(5000);
    const provider = new ScriptedProvider([
      { text: "Anti-veto checkpoint summarizing the fat conversation for the next model to continue." },
      { text: "Final answer" },
    ]);
    const runner = new AgentTurnRunner({
      provider,
      toolGateway: new FakeToolGateway(),
      context: {
        compactionEnabled: true,
        maxInputTokens: 1200,
        reserveTokens: 200,
        recentTurns: 4,
        summaryMaxChars: 2000,
      },
    });

    const result = await runner.run({
      messages: [
        { role: "user", content: fat },
        { role: "assistant", content: "a" },
        { role: "user", content: fat },
        { role: "assistant", content: "b" },
        { role: "user", content: fat },
        { role: "assistant", content: "c" },
        { role: "user", content: "latest" },
      ],
      pluginIds: [],
    });

    expect(result.compaction).toBeDefined();
    expect(result.compaction?.summary).toBe(`${SUMMARY_PREFIX}\nAnti-veto checkpoint summarizing the fat conversation for the next model to continue.`);
    expect(provider.requests).toHaveLength(2);
    expect(provider.requests[0]?.tools).toEqual([]);
  });

  it("uses 90% window threshold with 10k free floor (32k window → 22k soft)", async () => {
    const fat = "x".repeat(90000);
    const provider = new ScriptedProvider([
      { text: "90% checkpoint summarizing the context window threshold test for the next model." },
      { text: "Final answer" },
    ]);
    const runner = new AgentTurnRunner({
      provider,
      toolGateway: new FakeToolGateway(),
      context: {
        compactionEnabled: true,
        maxInputTokens: 32000,
        reserveTokens: 1000,
        recentTurns: 1,
        summaryMaxChars: 2000,
      },
    });

    const result = await runner.run({
      messages: [
        { role: "user", content: fat },
        { role: "assistant", content: "old" },
        { role: "user", content: "latest" },
      ],
      pluginIds: [],
      modelCapabilities: { contextWindow: 32000, maxOutput: 4000 },
    });

    expect(result.compaction).toBeDefined();
    expect(result.compaction?.summary).toBe(`${SUMMARY_PREFIX}\n90% checkpoint summarizing the context window threshold test for the next model.`);
  });

  it("forces compaction at full window even with tiny context", async () => {
    const fat = "x".repeat(8000);
    const provider = new ScriptedProvider([
      { text: "Hard force checkpoint" },
      { text: "Final answer" },
    ]);
    const runner = new AgentTurnRunner({
      provider,
      toolGateway: new FakeToolGateway(),
      context: {
        compactionEnabled: true,
        maxInputTokens: 1000,
        reserveTokens: 0,
        recentTurns: 1,
        summaryMaxChars: 500,
      },
    });

    const result = await runner.run({
      messages: [
        { role: "user", content: fat },
        { role: "assistant", content: "old" },
        { role: "user", content: "latest" },
      ],
      pluginIds: [],
      modelCapabilities: { contextWindow: 1000, maxOutput: 0 },
    });

    expect(result.compaction).toBeDefined();
  });

  it("mid-turn memento drops tool graph before the next provider complete", async () => {
    // Codex post-tool roll-over: after a fat tool result, history is replaced
    // with users + SUMMARY_PREFIX summary; tool_call ids from the prior pair
    // are not re-sent (invalid by design for the next sample).
    const hugeOutput = "x".repeat(50000);
    const provider = new ScriptedProvider([
      { toolCalls: [{ id: "call-1", name: "notes.create", args: { title: "Big" } }] },
      {
        text: "Created a large note via notes.create. The durable outcome is that note content exists in the notes store and the user asked to create a note.",
      },
      { text: "Done" },
    ]);
    const tools = new FakeToolGateway();
    tools.execute = async () => ({ id: "note-big", output: hugeOutput });
    const runner = new AgentTurnRunner({
      provider,
      toolGateway: tools,
      context: {
        compactionEnabled: true,
        maxInputTokens: 2000,
        reserveTokens: 200,
        recentTurns: 4,
        summaryMaxChars: 1000,
      },
    });

    const result = await runner.run({
      messages: [{ role: "user", content: "Create a note" }],
      pluginIds: ["notes"],
    });

    expect(result.text).toBe("Done");
    expect(result.compaction?.summary.length).toBeGreaterThan(40);
    // Second provider.complete for the turn is the post-memento final answer
    // (request index: 0=tools, 1=compact summarizer, 2=final).
    const postMemento = provider.requests[2];
    expect(postMemento).toBeDefined();
    expect(postMemento?.messages.some((m) => m.role === "tool")).toBe(false);
    const summaryUser = postMemento?.messages.find(
      (m) => m.role === "user" && typeof m.content === "string" && m.content.includes("Another language model"),
    );
    expect(summaryUser).toBeDefined();
    expect(postMemento?.messages.some((m) => m.role === "user" && m.content === "Create a note")).toBe(true);
  });

  it("mid-turn memento after mcp_ fat result does not keep untrusted tool envelopes for next sample", async () => {
    const hugeOutput = { entries: Array.from({ length: 80 }, (_, i) => ({ path: `docs/item-${i}.json`, blob: "z".repeat(200) })) };
    const provider = new ScriptedProvider([
      { toolCalls: [{ id: "call-1", name: "mcp_nusashell_files_search", args: { query: "curl" } }] },
      {
        text: "Searched files for curl helpers via MCP search. Found many matching paths under docs/; next answer the user with a short path list only.",
      },
      { text: "Found curl helpers" },
    ]);
    const tools = new FakeToolGateway();
    tools.execute = async (): Promise<unknown> => hugeOutput;
    const runner = new AgentTurnRunner({
      provider,
      toolGateway: tools,
      context: {
        compactionEnabled: true,
        maxInputTokens: 2000,
        reserveTokens: 200,
        recentTurns: 4,
        summaryMaxChars: 1000,
      },
    });

    await runner.run({
      messages: [{ role: "user", content: "Find curl-related files" }],
      pluginIds: ["nusashell.files"],
    });

    const postMemento = provider.requests[2];
    expect(postMemento).toBeDefined();
    expect(postMemento?.messages.some((m) => m.role === "tool")).toBe(false);
    expect(
      postMemento?.messages.some(
        (m) => m.role === "user" && typeof m.content === "string" && m.content.includes("</untrusted_tool_result>"),
      ),
    ).toBe(false);
  });

  // --- Dual-space: full transcript for UI, compact only for model send ---

  it("does not mutate input messages after compaction (dual-space: store stays full)", async () => {
    const fat = "x".repeat(5000);
    const originalMessages = [
      { role: "user" as const, content: fat },
      { role: "assistant" as const, content: "a" },
      { role: "user" as const, content: fat },
      { role: "assistant" as const, content: "b" },
      { role: "user" as const, content: fat },
      { role: "assistant" as const, content: "c" },
      { role: "user" as const, content: "latest" },
    ];
    const originalSnapshot = originalMessages.map((m) => ({ ...m }));
    const provider = new ScriptedProvider([
      { text: "Dual-space checkpoint" },
      { text: "Final answer" },
    ]);
    const runner = new AgentTurnRunner({
      provider,
      toolGateway: new FakeToolGateway(),
      context: {
        compactionEnabled: true,
        maxInputTokens: 1200,
        reserveTokens: 200,
        recentTurns: 4,
        summaryMaxChars: 2000,
      },
    });

    await runner.run({
      messages: originalMessages,
      pluginIds: [],
    });

    // Input messages array must be unchanged — the store transcript is SoT
    // and must never be stripped by compaction (dual-space contract).
    expect(originalMessages).toEqual(originalSnapshot);
    expect(originalMessages.length).toBe(7);
  });

  // --- Family heuristic: default window when API hides context limits ---

  it("resolveModelContextDefaults returns family-specific windows for known model ids", () => {
    expect(resolveModelContextDefaults("deepseek/deepseek-chat").contextWindow).toBe(163_840);
    expect(resolveModelContextDefaults("deepseek/deepseek-v4-flash").contextWindow).toBe(1_048_576);
    expect(resolveModelContextDefaults("z-ai/glm-4.7-flash").contextWindow).toBe(200_000);
    expect(resolveModelContextDefaults("minimax/m2.5").contextWindow).toBe(204_800);
    expect(resolveModelContextDefaults("xiaomi/mimo-7b").contextWindow).toBe(1_000_000);
    expect(resolveModelContextDefaults("moonshotai/kimi-k3").contextWindow).toBe(262_144);
    expect(resolveModelContextDefaults("openai/gpt-5-nano").contextWindow).toBe(400_000);
    expect(resolveModelContextDefaults("anthropic/claude-haiku-4").contextWindow).toBe(200_000);
    expect(resolveModelContextDefaults("anthropic/claude-sonnet-4").contextWindow).toBe(1_000_000);
    expect(resolveModelContextDefaults("google/gemini-2.5-pro").contextWindow).toBe(1_000_000);
  });

  it("resolveModelContextDefaults falls back to 200k for unknown model ids", () => {
    expect(resolveModelContextDefaults("some-unknown-vendor/model-x").contextWindow).toBe(DEFAULT_UNKNOWN_CONTEXT_WINDOW);
    expect(resolveModelContextDefaults(undefined).contextWindow).toBe(DEFAULT_UNKNOWN_CONTEXT_WINDOW);
    expect(resolveModelContextDefaults("").contextWindow).toBe(DEFAULT_UNKNOWN_CONTEXT_WINDOW);
  });

  it("compacts using 200k fallback window when model contextWindow is unknown", async () => {
    // Without the 200k fallback, maxInputTokens=12000 would be the window
    // and a 15k-token conversation would compact. With 200k fallback,
    // 15k < soft(180k) so NO compaction should trigger.
    const fat = "x".repeat(50000); // ~12.5k tokens — under 12000 maxInputTokens ceiling
    const provider = new ScriptedProvider([
      { text: "Should not compact — under 200k window" },
    ]);
    const runner = new AgentTurnRunner({
      provider,
      toolGateway: new FakeToolGateway(),
      context: {
        compactionEnabled: true,
        maxInputTokens: 200000,
        reserveTokens: 1000,
        recentTurns: 1,
        summaryMaxChars: 2000,
      },
    });

    const result = await runner.run({
      messages: [
        { role: "user", content: fat },
        { role: "assistant", content: "ok" },
        { role: "user", content: "latest" },
      ],
      pluginIds: [],
      model: "unknown-vendor/unknown-model",
      // No modelCapabilities — should use 200k family fallback
    });

    expect(result.compaction).toBeUndefined();
    expect(result.text).toBe("Should not compact — under 200k window");
  });

  // --- Stream soft-recover fix (Cycle 2) ---

  it("does not soft-recover on first sample when history has prior tool messages but no in-turn tools", async () => {
    // Regression: history with role:tool from an earlier turn must NOT trigger
    // soft-recover on the first provider call of a new turn.
    const provider = new FlakyProvider([
      new Error("transient boom"),
      { text: "should not reach" },
    ]);
    const runner = new AgentTurnRunner({ provider, toolGateway: new FakeToolGateway(), softRecoverAttempts: 1 });

    const error = await runner.run({
      messages: [
        { role: "user", content: "do something" },
        { role: "assistant", content: "", toolCalls: [{ id: "c1", name: "notes.create", args: {} }] },
        { role: "tool", toolCallId: "c1", name: "notes.create", content: "ok" },
        { role: "user", content: "now answer" },
      ],
      pluginIds: [],
    }).catch((e) => e);

    // No soft-recover: only 1 provider request, then AGENT_PROVIDER_FAILED.
    expect(provider.requests).toHaveLength(1);
    expect(error).toMatchObject({ code: "AGENT_PROVIDER_FAILED" });
  });

  it("captures streamed text deltas into partial.text when provider fails mid-stream", async () => {
    // Provider invokes onTextDelta with partial paragraphs, then throws.
    // The partial must contain the already-streamed text.
    const tools = new FakeToolGateway();
    // Need in-turn tool progress so partial is attached.
    // Use a two-response provider: first gives a tool call, second streams then fails.
    const providerWithTools = new FlakyProvider([
      { toolCalls: [{ id: "call-1", name: "notes.create", args: { title: "Test" } }] },
    ]);
    // Override second call to stream then fail
    const originalComplete = providerWithTools.complete.bind(providerWithTools);
    providerWithTools.complete = async (request: AgentProviderRequest) => {
      const index = providerWithTools.requests.length;
      if (index === 1) {
        providerWithTools.requests.push(request);
        request.onTextDelta?.("Hello ");
        request.onTextDelta?.("world, this is a ");
        request.onTextDelta?.("partial essay that ");
        throw new Error("stream cut");
      }
      return originalComplete(request);
    };

    const runner = new AgentTurnRunner({ provider: providerWithTools, toolGateway: tools, softRecoverAttempts: 0 });

    const error = await runner.run({
      messages: [{ role: "user", content: "Create a note then write an essay" }],
      pluginIds: ["notes"],
    }).catch((e) => e);

    expect(error).toMatchObject({ code: "AGENT_PROVIDER_FAILED" });
    expect(error.details?.partial).toBeDefined();
    expect(error.details.partial.text).toContain("Hello");
    expect(error.details.partial.text).toContain("partial essay");
  });

  // --- Continue incomplete stream (Cycle 1) ---

  it("attaches partial.text on cancel mid pure-text stream (no tools)", async () => {
    // Provider streams text via onTextDelta, then abort fires mid-stream.
    // The cancel error must carry partial.text with the streamed chars even
    // when no in-turn tools ran — so the UI can persist "interrupted" content.
    const controller = new AbortController();
    const provider = new FlakyProvider([]);
    provider.complete = async (request: AgentProviderRequest) => {
      provider.requests.push(request);
      request.onTextDelta?.("Halfway through ");
      request.onTextDelta?.("the essay and ");
      controller.abort();
      throw new Error("aborted");
    };
    const runner = new AgentTurnRunner({ provider, toolGateway: new FakeToolGateway(), softRecoverAttempts: 0 });

    const error = await runner.run({
      messages: [{ role: "user", content: "Write an essay" }],
      pluginIds: [],
      signal: controller.signal,
    }).catch((e) => e);

    expect(error).toMatchObject({ code: "AGENT_TURN_CANCELLED" });
    expect(error.details?.partial).toBeDefined();
    expect(error.details.partial.text).toContain("Halfway through");
    expect(error.details.partial.text).toContain("the essay");
    expect(error.details.partial.toolCalls).toHaveLength(0);
  });

  it("pure-text provider fail with softRecoverAttempts=1 and history tools → no soft recover, partial has live text", async () => {
    // History has role:tool from a prior turn, but no in-turn tools.
    // Provider streams text then fails. Soft-recover must NOT fire (no
    // in-turn tools). One request only. Partial.text has streamed chars.
    const providerWithTools = new FlakyProvider([]);
    providerWithTools.complete = async (request: AgentProviderRequest) => {
      providerWithTools.requests.push(request);
      request.onTextDelta?.("Partial answer ");
      request.onTextDelta?.("that got cut");
      throw new Error("stream cut");
    };
    const runner = new AgentTurnRunner({ provider: providerWithTools, toolGateway: new FakeToolGateway(), softRecoverAttempts: 1 });

    const error = await runner.run({
      messages: [
        { role: "user", content: "do something" },
        { role: "assistant", content: "", toolCalls: [{ id: "c1", name: "notes.create", args: {} }] },
        { role: "tool", toolCallId: "c1", name: "notes.create", content: "ok" },
        { role: "user", content: "now write an essay" },
      ],
      pluginIds: [],
    }).catch((e) => e);

    // No soft-recover: only 1 provider request.
    expect(providerWithTools.requests).toHaveLength(1);
    expect(error).toMatchObject({ code: "AGENT_PROVIDER_FAILED" });
    expect(error.details?.partial).toBeDefined();
    expect(error.details.partial.text).toContain("Partial answer");
    expect(error.details.partial.text).toContain("that got cut");
  });

  // --- Soft recover vs live stream (tool-round rewrite gap) ---

  it("does not soft-recover when in-turn tools exist but provider already streamed live text", async () => {
    // After a successful tool round, the next sample paints prose via
    // onTextDelta then times out. Soft recover must NOT re-sample that
    // round (rewrite loop); throw with partial so UI can Continue/Resume.
    const provider = new FlakyProvider([
      { toolCalls: [{ id: "call-1", name: "notes.create", args: { title: "Roadmap" } }] },
    ]);
    provider.complete = async (request: AgentProviderRequest) => {
      const index = provider.requests.length;
      provider.requests.push(request);
      if (index === 0) {
        return {
          toolCalls: [{ id: "call-1", name: "notes.create", args: { title: "Roadmap" } }],
        };
      }
      request.onTextDelta?.("Long answer that was already ");
      request.onTextDelta?.("shown to the user before the cut");
      throw new Error("Provider request timed out");
    };
    const tools = new FakeToolGateway();
    const runner = new AgentTurnRunner({ provider, toolGateway: tools, softRecoverAttempts: 1 });

    const error = await runner.run({
      messages: [{ role: "user", content: "Create a note then write a long answer" }],
      pluginIds: ["notes"],
    }).catch((e) => e);

    // Round 1 tool + one failed text sample only — no soft-recover third call.
    expect(provider.requests).toHaveLength(2);
    expect(error).toMatchObject({ code: "AGENT_PROVIDER_FAILED" });
    expect(error.details?.partial).toBeDefined();
    expect(error.details.partial.text).toContain("Long answer that was already");
    expect(error.details.partial.text).toContain("shown to the user before the cut");
    expect(error.details.partial.toolCalls).toHaveLength(1);
    expect(error.details.partial.messages).toEqual(expect.arrayContaining([
      expect.objectContaining({ role: "tool", toolCallId: "call-1" }),
    ]));
  });

  it("does not soft-recover when in-turn tools exist but provider already streamed reasoning deltas", async () => {
    const provider = new FlakyProvider([
      { toolCalls: [{ id: "call-1", name: "notes.create", args: { title: "Roadmap" } }] },
    ]);
    provider.complete = async (request: AgentProviderRequest) => {
      const index = provider.requests.length;
      provider.requests.push(request);
      if (index === 0) {
        return {
          toolCalls: [{ id: "call-1", name: "notes.create", args: { title: "Roadmap" } }],
        };
      }
      request.onReasoningDelta?.("Thinking about the next step…");
      throw new Error("SSE read failed: Provider request timed out");
    };
    const tools = new FakeToolGateway();
    const runner = new AgentTurnRunner({ provider, toolGateway: tools, softRecoverAttempts: 1 });

    const error = await runner.run({
      messages: [{ role: "user", content: "Create a note then reason" }],
      pluginIds: ["notes"],
    }).catch((e) => e);

    expect(provider.requests).toHaveLength(2);
    expect(error).toMatchObject({ code: "AGENT_PROVIDER_FAILED" });
    expect(error.details?.partial?.reasoning).toContain("Thinking about the next step");
  });
});
