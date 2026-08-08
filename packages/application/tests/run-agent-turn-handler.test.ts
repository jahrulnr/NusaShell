import { describe, expect, it } from "vitest";
import {
  AgentTurnCoordinator,
  RunAgentTurnHandler,
  type AgentProvider,
  type AgentProviderRequest,
  type AgentProviderResult,
  type AgentToolGateway,
  type McpLiveSnapshot,
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
  readonly begunTurns: string[] = [];
  readonly endedTurns: string[] = [];
  beginTurn(turnId: string) { this.begunTurns.push(turnId); }
  endTurn(turnId: string) { this.endedTurns.push(turnId); }
  cancelTurn() {}
  async listTools() { return []; }
  async execute() { return { ok: true }; }
  async getMcpLiveSnapshot(_turnId: string): Promise<McpLiveSnapshot> {
    return { running: [], tools: [] };
  }
}

function makeRegistry(provider: AgentProvider) {
  return {
    get: (id: string) => (id === provider.id ? provider : undefined),
    list: () => [provider],
  };
}

const RUNTIME = {
  strategy: "failover" as const,
  totalAttemptBudget: 1,
  maxToolRounds: 1,
  maxRepeatedToolCalls: 1,
  softRecoverAttempts: 0,
  maxConcurrentToolCalls: 1,
};

describe("RunAgentTurnHandler lifecycle callbacks", () => {
  it("emits onTurnStarted before the run and onTurnEnd(completed) on success", async () => {
    const provider = new ScriptedProvider([{ text: "hi" }]);
    const tools = new FakeToolGateway();
    const started: string[] = [];
    const ended: Array<{ traceId: string; reason: string }> = [];
    const handler = new RunAgentTurnHandler(
      makeRegistry(provider),    // 1 providers
      tools,                     // 2 toolGateway
      "scripted",                // 3 defaultProviderId
      RUNTIME,                   // 4 runtime
      undefined,                 // 5 logger
      undefined,                 // 6 coordinator
      undefined,                 // 7 onTextDelta
      undefined,                 // 8 onReasoningDelta
      undefined,                 // 9 onToolCallStart
      undefined,                 // 10 onToolCallEnd
      undefined,                 // 11 onContextUpdate
      undefined,                 // 12 promptLoader
      undefined,                 // 13 userPrompt
      undefined,                 // 14 memoryStore
      (_result) => {},           // 15 onTurnComplete
      (traceId, reason) => { ended.push({ traceId, reason }); }, // 16 onTurnEnd
      (traceId) => { started.push(traceId); },                   // 17 onTurnStarted
    );
    const result = await handler.handle({
      kind: "run-agent-turn",
      traceId: "trace-ok",
      messages: [{ role: "user", content: "hi" }],
      pluginIds: [],
    });
    expect(result.text).toBe("hi");
    expect(started).toEqual(["trace-ok"]);
    expect(ended).toEqual([{ traceId: "trace-ok", reason: "completed" }]);
  });

  it("emits onTurnEnd(cancelled) when the turn is cancelled", async () => {
    // Provider that blocks until the abort signal fires, then throws.
    const provider: AgentProvider = {
      id: "blocking",
      async complete(request: AgentProviderRequest): Promise<AgentProviderResult> {
        return new Promise((_resolve, reject) => {
          const onAbort = () => reject(new Error("aborted by test"));
          if (request.signal?.aborted) { onAbort(); return; }
          request.signal?.addEventListener("abort", onAbort, { once: true });
        });
      },
    };
    const tools = new FakeToolGateway();
    const coordinator = new AgentTurnCoordinator();
    const ended: Array<{ traceId: string; reason: string }> = [];
    const handler = new RunAgentTurnHandler(
      makeRegistry(provider),
      tools,
      "blocking",
      RUNTIME,
      undefined,
      coordinator,
      undefined,
      undefined,
      undefined,
      undefined,
      undefined,
      undefined,
      undefined,
      undefined,
      undefined,
      (traceId, reason) => { ended.push({ traceId, reason }); },
    );
    const handlePromise = handler.handle({
      kind: "run-agent-turn",
      traceId: "trace-cancel",
      messages: [{ role: "user", content: "go" }],
      pluginIds: [],
    });
    // Cancel after the run has started (coordinator registers the controller).
    await new Promise((r) => setTimeout(r, 10));
    coordinator.cancel("trace-cancel");
    await expect(handlePromise).rejects.toMatchObject({ code: "AGENT_TURN_CANCELLED" });
    expect(ended).toEqual([{ traceId: "trace-cancel", reason: "cancelled" }]);
  });

  it("emits onTurnEnd(failed) when the provider throws", async () => {
    const provider = new ScriptedProvider([]);
    const tools = new FakeToolGateway();
    const ended: Array<{ traceId: string; reason: string }> = [];
    const handler = new RunAgentTurnHandler(
      makeRegistry(provider),
      tools,
      "scripted",
      RUNTIME,
      undefined,
      undefined,
      undefined,
      undefined,
      undefined,
      undefined,
      undefined,
      undefined,
      undefined,
      undefined,
      undefined,
      (traceId, reason) => { ended.push({ traceId, reason }); },
    );
    await expect(handler.handle({
      kind: "run-agent-turn",
      traceId: "trace-fail",
      messages: [{ role: "user", content: "go" }],
      pluginIds: [],
    })).rejects.toThrow("AI provider request failed");
    expect(ended).toEqual([{ traceId: "trace-fail", reason: "failed" }]);
  });

  it("seals interrupted via onTurnInterrupted then strips messages from wire partial", async () => {
    // Tool round succeeds then provider fails → partial has messages/toolCalls.
    // Handler must durable-seal before rethrow and slim messages for IPC.
    class ToolThenFailProvider implements AgentProvider {
      readonly id = "scripted";
      private n = 0;
      async complete(): Promise<AgentProviderResult> {
        this.n += 1;
        if (this.n === 1) {
          return { toolCalls: [{ id: "c1", name: "notes.create", args: { title: "T" } }] };
        }
        throw new Error("provider boom");
      }
    }
    class Tools implements AgentToolGateway {
      beginTurn() {}
      endTurn() {}
      cancelTurn() {}
      async listTools() {
        return [{ name: "notes.create", description: "c", inputSchema: { type: "object" } }];
      }
      async execute() { return { id: "note-1" }; }
    }
    const seals: Array<{
      conversationId: string;
      resume?: boolean;
      interruptReason: string;
      messagesLen: number;
      rounds: number;
    }> = [];
    const runtime = {
      ...RUNTIME,
      maxToolRounds: 4,
      softRecoverAttempts: 0,
      maxRepeatedToolCalls: 50,
    };
    const handler = new RunAgentTurnHandler(
      makeRegistry(new ToolThenFailProvider()),
      new Tools(),
      "scripted",
      runtime,
      undefined,
      undefined,
      undefined,
      undefined,
      undefined,
      undefined,
      undefined,
      undefined,
      undefined,
      undefined,
      undefined,
      undefined,
      undefined,
      undefined,
      undefined,
      undefined,
      undefined,
      undefined,
      undefined,
      undefined,
      {
        onTurnInterrupted: async (partial, context) => {
          seals.push({
            conversationId: context.conversationId,
            ...(context.resume !== undefined ? { resume: context.resume } : {}),
            interruptReason: context.interruptReason,
            messagesLen: partial.messages.length,
            rounds: partial.rounds,
          });
        },
      },
    );

    const error = await handler.handle({
      kind: "run-agent-turn",
      traceId: "trace-interrupt-seal",
      conversationId: "conv_test",
      messages: [{ role: "user", content: "Create a note" }],
      pluginIds: ["notes"],
      maxToolRounds: 4,
    }).catch((e) => e);

    expect(seals).toHaveLength(1);
    expect(seals[0]).toMatchObject({
      conversationId: "conv_test",
      interruptReason: "provider",
      rounds: 1,
    });
    expect(seals[0]!.messagesLen).toBeGreaterThan(0);
    expect(error).toMatchObject({ code: "AGENT_PROVIDER_FAILED" });
    expect(error.details?.partial).toBeDefined();
    expect(error.details.partial.toolCalls.length).toBeGreaterThan(0);
    // Wire copy omits heavy resume payload (durable store already sealed).
    expect(error.details.partial.messages).toEqual([]);
    expect(error.details.sealedInterrupted).toBe(true);
  });


  it("supersedes a previous traceId and emits onTurnSuperseded", async () => {
    const provider = new ScriptedProvider([{ text: "new" }]);
    const tools = new FakeToolGateway();
    const superseded: Array<{ oldId: string; newId: string }> = [];
    const ended: Array<{ traceId: string; reason: string }> = [];
    const handler = new RunAgentTurnHandler(
      makeRegistry(provider),
      tools,
      "scripted",
      RUNTIME,
      undefined,
      undefined,
      undefined,
      undefined,
      undefined,
      undefined,
      undefined,
      undefined,
      undefined,
      undefined,
      undefined,
      (traceId, reason) => { ended.push({ traceId, reason }); },
      undefined,
      (oldId, newId) => { superseded.push({ oldId, newId }); },
    );
    const result = await handler.handle({
      kind: "run-agent-turn",
      traceId: "trace-new",
      supersedeTraceId: "trace-old",
      messages: [{ role: "user", content: "go" }],
      pluginIds: [],
    });
    expect(result.text).toBe("new");
    expect(superseded).toEqual([{ oldId: "trace-old", newId: "trace-new" }]);
    expect(ended).toEqual([{ traceId: "trace-new", reason: "completed" }]);
  });

  it("projects sealed steps onto ActiveTurnProjection for the conversation", async () => {
    const { InMemoryActiveTurnProjection } = await import("../src/index.js");
    const provider = new ScriptedProvider([{ text: "final answer" }]);
    const tools = new FakeToolGateway();
    const activeTurns = new InMemoryActiveTurnProjection();
    const progress: string[] = [];
    const handler = new RunAgentTurnHandler(
      makeRegistry(provider),
      tools,
      "scripted",
      RUNTIME,
      undefined,
      undefined,
      undefined,
      undefined,
      undefined,
      undefined,
      undefined,
      undefined,
      undefined,
      undefined,
      undefined,
      undefined,
      undefined,
      undefined,
      undefined,
      activeTurns,
      (snap) => { progress.push(`${snap.traceId}:${snap.steps.length}`); },
    );
    const pending = handler.handle({
      kind: "run-agent-turn",
      traceId: "trace-proj",
      conversationId: "conv-1",
      messages: [{ role: "user", content: "hi" }],
      pluginIds: [],
    });
    // Mid-turn snapshot exists before completion finishes clearing it.
    await Promise.resolve();
    const result = await pending;
    expect(result.text).toBe("final answer");
    // Cleared on finally after success.
    expect(activeTurns.get("conv-1")).toBeUndefined();
    expect(progress.some((p) => p.startsWith("trace-proj:"))).toBe(true);
  });

  // --- Live MCP snapshot inject (Cycle 4) ---

  const fakePromptLoader = {
    loadPrompts: async () => [
      { name: "system", content: "You are the NusaShell agent.", isTemplate: false },
      { name: "mcp-tools", content: "Use tool_list to discover tools.", isTemplate: false },
    ],
    loadSubagentPrompt: async () => undefined,
    loadContinuePrompt: async () => undefined,
    loadCompactPrompt: async () => undefined,
    loadReviewPrompt: async (_kind: "memory" | "skill" | "combined") => "",
  };

  it("injects Live MCP catalog in a hidden runtime checkpoint on non-resume turns", async () => {
    const provider = new ScriptedProvider([{ text: "ok" }]);
    class LiveSnapshotGateway extends FakeToolGateway {
      snapshotCalls = 0;
      override async getMcpLiveSnapshot() {
        this.snapshotCalls += 1;
        return {
          running: [{ pluginId: "nusashell.notes" }],
          tools: [{
            providerName: "mcp_nusashell_notes_createNote",
            pluginId: "nusashell.notes",
            toolName: "createNote",
            description: "Create a note",
            inputSchema: { type: "object", properties: { text: { type: "string" } } },
          }],
        };
      }
    }
    const tools = new LiveSnapshotGateway();
    const handler = new RunAgentTurnHandler(
      makeRegistry(provider), tools, "scripted", RUNTIME,
      undefined, undefined, undefined, undefined, undefined, undefined, undefined,
      fakePromptLoader, undefined, undefined, undefined, undefined, undefined, undefined,
    );
    const result = await handler.handle({
      kind: "run-agent-turn",
      traceId: "trace-live",
      messages: [{ role: "user", content: "hi" }],
      pluginIds: [],
    });
    expect(result.text).toBe("ok");
    // REV2: no hidden user runtime checkpoint anymore. The Live MCP catalog is
    // recovered via the ephemeral hydration transcript instead.
    const legacyCheckpoint = provider.requests[0]?.messages
      .filter((m) => m.role === "user")
      .map((m) => String(m.content))
      .find((c) => c.startsWith("[NUSASHELL RUNTIME CONTEXT]"));
    expect(legacyCheckpoint).toBeUndefined();
    // Fresh room still carries the catalog through the synthetic hydration
    // tool results (tool_list result contains the running plugin + schema).
    const toolResults = provider.requests[0]?.messages
      .filter((m) => m.role === "tool")
      .map((m) => String(m.content))
      .join("\n");
    expect(toolResults).toContain("nusashell.notes");
    expect(toolResults).toContain("mcp_nusashell_notes_createNote");
    // Schema is JSON-serialized compactly in the tool_list result.
    expect(toolResults).toContain('"inputSchema"');
    expect(toolResults).toContain('"type":"object"');
    // The synthetic hydration transcript is the sole runtime-context path.
    // Do not rebuild an unused legacy system-prompt catalog first.
    expect(tools.snapshotCalls).toBe(1);
  });

  it("does not inject Live MCP block when snapshot is empty", async () => {
    const provider = new ScriptedProvider([{ text: "ok" }]);
    class EmptySnapshotGateway extends FakeToolGateway {
      override async getMcpLiveSnapshot() {
        return { running: [], tools: [] };
      }
    }
    const tools = new EmptySnapshotGateway();
    const handler = new RunAgentTurnHandler(
      makeRegistry(provider), tools, "scripted", RUNTIME,
      undefined, undefined, undefined, undefined, undefined, undefined, undefined,
      fakePromptLoader, undefined, undefined, undefined, undefined, undefined, undefined,
    );
    await handler.handle({
      kind: "run-agent-turn",
      traceId: "trace-empty-live",
      messages: [{ role: "user", content: "hi" }],
      pluginIds: [],
    });
    const hasLive = provider.requests[0]?.messages
      .some((m) => m.role === "system" && String(m.content).includes("## Live MCP (runtime)"));
    expect(hasLive).toBe(false);
  });

  it("resume refreshes only the hidden runtime checkpoint, not the full system tail", async () => {
    const provider = new ScriptedProvider([{ text: "ok" }]);
    let snapshotCalls = 0;
    class CountingSnapshotGateway extends FakeToolGateway {
      override async getMcpLiveSnapshot() {
        snapshotCalls += 1;
        return { running: [{ pluginId: "x" }], tools: [] };
      }
    }
    const tools = new CountingSnapshotGateway();
    const handler = new RunAgentTurnHandler(
      makeRegistry(provider), tools, "scripted", RUNTIME,
      undefined, undefined, undefined, undefined, undefined, undefined, undefined,
      fakePromptLoader, undefined, undefined, undefined, undefined, undefined, undefined,
    );
    await handler.handle({
      kind: "run-agent-turn",
      traceId: "trace-resume",
      messages: [{ role: "user", content: "hi" }],
      pluginIds: [],
      resume: true,
    });
    // The cost-saving resume path still avoids system/developer re-injection.
    // REV2: resume does NOT rebuild a hidden runtime checkpoint; the resumed
    // loop re-hydrates after compaction only. So no Live MCP block and no
    // `[NUSASHELL RUNTIME CONTEXT]` appear on resume.
    const resumeRequest = provider.requests.at(-1);
    const hasLiveSystem = resumeRequest?.messages
      .some((m) => m.role === "system" && String(m.content).includes("## Live MCP (runtime)"));
    expect(hasLiveSystem).toBe(false);
    const hasLegacyCheckpoint = resumeRequest?.messages.some((m) =>
      m.role === "user"
      && String(m.content).startsWith("[NUSASHELL RUNTIME CONTEXT]"),
    );
    expect(hasLegacyCheckpoint).toBe(false);
    expect(snapshotCalls).toBe(0);
  });

  // --- #36: resume path supplies system prompts to the summarizer ---

  it("injects system prompts into the compactor on a resume turn (summarizer sees session context)", async () => {
    // Provider records the request messages; first call (round 0) is the
    // summarizer, second is the resumed turn itself.
    const provider = new ScriptedProvider([
      { text: "summary body with enough length to pass the quality gate ................" },
      { text: "resumed answer" },
    ]);
    const tools = new FakeToolGateway();
    const runtime = {
      ...RUNTIME,
      // Aggressive budget so the resume messages trigger compaction.
      context: {
        compactionEnabled: true,
        maxInputTokens: 200,
        reserveTokens: 20,
        recentTurns: 3,
        summaryMaxChars: 10_000,
      },
    };
    const handler = new RunAgentTurnHandler(
      makeRegistry(provider), tools, "scripted", runtime,
      undefined, undefined, undefined, undefined, undefined, undefined, undefined,
      fakePromptLoader, undefined, undefined, undefined, undefined, undefined, undefined,
    );
    // Long user history so the token estimate crosses the tiny budget.
    const longUser = "user message ".repeat(200).trim();
    const result = await handler.handle({
      kind: "run-agent-turn",
      traceId: "trace-resume-compact",
      messages: [
        { role: "user", content: longUser },
        { role: "assistant", content: "part 1" },
        { role: "user", content: "Continue. This is the resumed instruction for the next step." },
      ],
      pluginIds: [],
      resume: true,
    });
    expect(result.text).toBe("resumed answer");
    // At least two provider calls: the summarizer (round 0) and the resumed turn.
    expect(provider.requests.length).toBeGreaterThanOrEqual(2);
    // The summarizer request (round 0) MUST contain the injected system prompts
    // (the stable system prefix) — not the raw history only.
    const summarizer = provider.requests[0];
    if (!summarizer) {
      throw new Error("expected a summarizer request on the resume-compact path");
    }
    const systemContents = summarizer.messages
      .filter((m) => m.role === "system")
      .map((m) => String(m.content));
    expect(systemContents.length).toBeGreaterThan(0);
    expect(systemContents.some((c) => c.includes("You are the NusaShell agent."))).toBe(true);
    expect(systemContents.some((c) => c.includes("Use tool_list to discover tools."))).toBe(true);
  });
  });
