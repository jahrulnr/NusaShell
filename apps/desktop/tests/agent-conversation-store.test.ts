import { mkdtemp, readdir, readFile, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { describe, expect, it } from "vitest";
import { AgentConversationStore } from "../src/main/agent-conversation-store.js";

describe("AgentConversationStore", () => {
  it("persists conversations, messages, and compaction checkpoints across store instances", async () => {
    const root = await mkdtemp(join(tmpdir(), "nusashell-conversations-"));
    const path = join(root, "agent-conversations.json");
    const first = new AgentConversationStore(path, () => new Date("2026-07-28T10:00:00.000Z"), () => "conv-1");

    const created = await first.create();
    await first.appendMessage(created.id, { role: "user", content: "Investigate MCP logs" });
    await first.appendMessage(created.id, {
      role: "assistant",
      content: "I found the issue.",
      reasoning: "I inspected the renderer and traced the empty state.",
    });
    await first.saveCheckpoint(created.id, {
      summary: "The user asked to investigate MCP logs.",
      compactedMessageCount: 2,
      via: "provider",
      compactionCount: 2,
    });

    const second = new AgentConversationStore(path);
    const loaded = await second.get(created.id);
    expect(loaded?.title).toBe("Investigate MCP logs");
    expect(loaded?.messages).toHaveLength(2);
    expect(loaded?.messages[1]).toMatchObject({
      role: "assistant",
      reasoning: "I inspected the renderer and traced the empty state.",
    });
    expect(loaded?.checkpoint).toEqual({
      summary: "The user asked to investigate MCP logs.",
      compactedMessageCount: 2,
      via: "provider",
      compactionCount: 2,
    });
  });

  it("lists newest conversations first and permanently deletes the selected conversation", async () => {
    let tick = 0;
    const root = await mkdtemp(join(tmpdir(), "nusashell-conversations-"));
    const store = new AgentConversationStore(
      join(root, "agent-conversations.json"),
      () => new Date(1_800_000_000_000 + tick++ * 1000),
      (() => { let id = 0; return () => `conv-${++id}`; })(),
    );
    const first = await store.create();
    const second = await store.create();

    expect((await store.list()).map((item) => item.id)).toEqual([second.id, first.id]);
    await store.delete(second.id);
    expect(await store.get(second.id)).toBeNull();
    expect((await store.list()).map((item) => item.id)).toEqual([first.id]);
  });

  it("does not silently replace a corrupt conversation file", async () => {
    const root = await mkdtemp(join(tmpdir(), "nusashell-conversations-"));
    const path = join(root, "agent-conversations.json");
    await writeFile(path, "{not-json", "utf8");

    const store = new AgentConversationStore(path);
    await expect(store.list()).rejects.toThrow("Could not load conversations");
    expect(await readFile(path, "utf8")).toBe("{not-json");
  });

  it("persists a content-inspected text attachment", async () => {
    const root = await mkdtemp(join(tmpdir(), "nusashell-conversations-"));
    const path = join(root, "agent-conversations.json");
    const first = new AgentConversationStore(path, () => new Date("2026-07-29T10:00:00.000Z"), () => "conv-text");
    const conversation = await first.create();
    await first.appendMessage(conversation.id, {
      role: "user",
      content: "Review this",
      attachments: [{ type: "text", mediaType: "text/plain", name: "layout.css", content: ".shell { display: grid; }" }],
    });

    await expect(new AgentConversationStore(path).get(conversation.id)).resolves.toMatchObject({
      messages: [{ attachments: [{ type: "text", name: "layout.css", content: ".shell { display: grid; }" }] }],
    });
  });

  it("persists assistant steps (chronological reasoning, tool calls, text)", async () => {
    const root = await mkdtemp(join(tmpdir(), "nusashell-conversations-"));
    const path = join(root, "agent-conversations.json");
    const first = new AgentConversationStore(path, () => new Date("2026-07-30T10:00:00.000Z"), () => "conv-steps");
    const conversation = await first.create();
    await first.appendMessage(conversation.id, { role: "user", content: "Check tools" });
    await first.appendMessage(conversation.id, {
      role: "assistant",
      content: "There are 2 plugins.",
      reasoning: "I should check what plugins are available.",
      toolCalls: [{ id: "call-1", name: "mcp_list", ok: true, args: { q: "plugins" }, output: '{"count":2}' }],
      steps: [
        { type: "reasoning", content: "I should check what plugins are available." },
        { type: "tool_calls", calls: [{ id: "call-1", name: "mcp_list", ok: true, args: { q: "plugins" }, output: '{"count":2}' }] },
        { type: "reasoning", content: "There are 2 plugins: Mail and Notes." },
        { type: "text", content: "There are 2 plugins." },
      ],
    });

    const loaded = await new AgentConversationStore(path).get(conversation.id);
    expect(loaded?.messages[1]?.steps).toEqual([
      { type: "reasoning", content: "I should check what plugins are available." },
      { type: "tool_calls", calls: [{ id: "call-1", name: "mcp_list", ok: true, args: { q: "plugins" }, output: '{"count":2}' }] },
      { type: "reasoning", content: "There are 2 plugins: Mail and Notes." },
      { type: "text", content: "There are 2 plugins." },
    ]);
  });

  it("persists workspace across store instances", async () => {
    const root = await mkdtemp(join(tmpdir(), "nusashell-conversations-"));
    const path = join(root, "agent-conversations.json");
    const first = new AgentConversationStore(path, () => new Date("2026-07-30T12:00:00.000Z"), () => "conv-ws");
    const created = await first.create();
    await first.setWorkspace(created.id, "/home/user/projects/myapp");

    const loaded = await new AgentConversationStore(path).get(created.id);
    expect(loaded?.workspace).toBe("/home/user/projects/myapp");
  });

  it("clears workspace when set to empty string", async () => {
    const root = await mkdtemp(join(tmpdir(), "nusashell-conversations-"));
    const path = join(root, "agent-conversations.json");
    const first = new AgentConversationStore(path, () => new Date("2026-07-30T12:00:00.000Z"), () => "conv-ws2");
    const created = await first.create();
    await first.setWorkspace(created.id, "/home/user/projects/myapp");
    await first.setWorkspace(created.id, "");

    const loaded = await new AgentConversationStore(path).get(created.id);
    expect(loaded?.workspace).toBeUndefined();
  });

  it("persists a per-conversation model binding across store instances", async () => {
    const root = await mkdtemp(join(tmpdir(), "nusashell-conversations-"));
    const path = join(root, "agent-conversations.json");
    const first = new AgentConversationStore(path, () => new Date("2026-08-01T12:00:00.000Z"), () => "conv-model");
    const created = await first.create();
    await first.setModel(created.id, { modelKey: "openai/gpt-5", effort: "high" });

    const loaded = await new AgentConversationStore(path).get(created.id);
    expect(loaded?.model).toEqual({ modelKey: "openai/gpt-5", effort: "high" });
    // Switching rooms must not leak the model onto another conversation.
    const other = await first.create();
    expect((await first.get(other.id))?.model).toBeUndefined();
  });

  it("clears the per-conversation model binding when model is set to null", async () => {
    const root = await mkdtemp(join(tmpdir(), "nusashell-conversations-"));
    const path = join(root, "agent-conversations.json");
    const first = new AgentConversationStore(path, () => new Date("2026-08-01T12:00:00.000Z"), () => "conv-model2");
    const created = await first.create();
    await first.setModel(created.id, { modelKey: "anthropic/claude", effort: "auto" });
    await first.setModel(created.id, null);

    const loaded = await new AgentConversationStore(path).get(created.id);
    expect(loaded?.model).toBeUndefined();
  });

  it("round-trips assistant messages with boundary-length truncated tool output", async () => {
    // Regression: a clamped output of 12_002 chars ("...\n…" past the cap) used
    // to fail validation on load and silently drop the whole assistant message.
    const root = await mkdtemp(join(tmpdir(), "nusashell-conversations-"));
    const path = join(root, "agent-conversations.json");
    const first = new AgentConversationStore(path, () => new Date("2026-07-31T10:00:00.000Z"), () => "conv-boundary");
    const conversation = await first.create();
    await first.appendMessage(conversation.id, { role: "user", content: "Run a long command" });
    await first.appendMessage(conversation.id, {
      role: "assistant",
      content: "Done.",
      steps: [
        { type: "tool_calls", calls: [{ id: "call-1", name: "read", ok: true, output: `${"y".repeat(11_998)}\n…` }] },
        { type: "text", content: "Done." },
      ],
    });

    const loaded = await new AgentConversationStore(path).get(conversation.id);
    expect(loaded?.messages).toHaveLength(2);
    expect(loaded?.messages[1]?.steps?.[0]).toMatchObject({ type: "tool_calls" });
  });

  it("repairs legacy over-cap tool output on load instead of dropping the message", async () => {
    const root = await mkdtemp(join(tmpdir(), "nusashell-conversations-"));
    const path = join(root, "agent-conversations.json");
    const legacyOutput = `${"y".repeat(12_000)}\n…`; // 12_002 chars, written by the old clamp
    await writeFile(path, JSON.stringify({
      version: 1,
      conversations: [{
        id: "conv-legacy",
        title: "Legacy chat",
        createdAt: "2026-07-31T08:00:00.000Z",
        updatedAt: "2026-07-31T08:05:00.000Z",
        messages: [
          { role: "user", content: "hai" },
          {
            role: "assistant",
            content: "Done.",
            steps: [
              { type: "tool_calls", calls: [{ id: "call-9", name: "read", ok: true, output: legacyOutput }] },
              { type: "text", content: "Done." },
            ],
          },
        ],
      }],
    }), "utf8");

    const loaded = await new AgentConversationStore(path).get("conv-legacy");
    expect(loaded?.messages).toHaveLength(2);
    const step = loaded?.messages[1]?.steps?.[0];
    expect(step?.type).toBe("tool_calls");
    if (step?.type === "tool_calls") expect(step.calls[0]?.output).toHaveLength(12_000);
  });

  it("persists an interrupted assistant message with status and resumeMessages", async () => {
    const root = await mkdtemp(join(tmpdir(), "nusashell-conversations-"));
    const path = join(root, "agent-conversations.json");
    const store = new AgentConversationStore(path, () => new Date("2026-08-01T10:00:00.000Z"), () => "conv-int");
    const conversation = await store.create();
    await store.appendMessage(conversation.id, { role: "user", content: "Create a note" });
    await store.appendMessage(conversation.id, {
      role: "assistant",
      content: "Turn interrupted after 1 tool round.",
      status: "interrupted",
      traceId: "trace-1",
      rounds: 1,
      steps: [{ type: "tool_calls", calls: [{ id: "call-1", name: "notes.create", ok: true }] }],
      resumeMessages: [
        { role: "user", content: "Create a note" },
        { role: "assistant", toolCalls: [{ id: "call-1", name: "notes.create", args: { title: "X" } }] },
        { role: "tool", toolCallId: "call-1", name: "notes.create", content: '{"ok":true}' },
      ],
    });

    const loaded = await new AgentConversationStore(path).get(conversation.id);
    expect(loaded?.messages[1]).toMatchObject({ status: "interrupted", rounds: 1 });
    expect(loaded?.messages[1]?.resumeMessages).toHaveLength(3);
  });

  it("replaces the last interrupted assistant message with replaceLastInterrupted", async () => {
    const root = await mkdtemp(join(tmpdir(), "nusashell-conversations-"));
    const path = join(root, "agent-conversations.json");
    const store = new AgentConversationStore(path, () => new Date("2026-08-01T10:00:00.000Z"), () => "conv-replace");
    const conversation = await store.create();
    await store.appendMessage(conversation.id, { role: "user", content: "Create a note" });
    await store.appendMessage(conversation.id, {
      role: "assistant",
      content: "Turn interrupted after 1 tool round.",
      status: "interrupted",
      resumeMessages: [{ role: "user", content: "Create a note" }],
    });

    const updated = await store.replaceLastInterrupted(conversation.id, {
      role: "assistant",
      content: "The note is ready.",
      traceId: "trace-1",
      rounds: 2,
    });

    expect(updated.messages).toHaveLength(2);
    expect(updated.messages[1]).toMatchObject({ content: "The note is ready.", rounds: 2 });
    expect(updated.messages[1]?.status).toBeUndefined();
    expect(updated.messages[1]?.resumeMessages).toBeUndefined();
  });

  it("rejects replaceLastInterrupted when the last message is not interrupted", async () => {
    const root = await mkdtemp(join(tmpdir(), "nusashell-conversations-"));
    const path = join(root, "agent-conversations.json");
    const store = new AgentConversationStore(path, () => new Date("2026-08-01T10:00:00.000Z"), () => "conv-reject");
    const conversation = await store.create();
    await store.appendMessage(conversation.id, { role: "user", content: "Hello" });
    await store.appendMessage(conversation.id, { role: "assistant", content: "Hi there." });

    await expect(store.replaceLastInterrupted(conversation.id, {
      role: "assistant",
      content: "Replacement",
    })).rejects.toThrow("not an interrupted assistant message");
  });

  it("drops resumeMessages when the serialized message exceeds the budget", async () => {
    const root = await mkdtemp(join(tmpdir(), "nusashell-conversations-"));
    const path = join(root, "agent-conversations.json");
    const store = new AgentConversationStore(path, () => new Date("2026-08-01T10:00:00.000Z"), () => "conv-large");
    const conversation = await store.create();
    await store.appendMessage(conversation.id, { role: "user", content: "Go" });
    const hugeResume = [{ role: "system", content: "x".repeat(600_000) }];
    await store.appendMessage(conversation.id, {
      role: "assistant",
      content: "Turn interrupted.",
      status: "interrupted",
      resumeMessages: hugeResume,
    });

    const loaded = await new AgentConversationStore(path).get(conversation.id);
    expect(loaded?.messages[1]?.status).toBe("interrupted");
    expect(loaded?.messages[1]?.resumeMessages).toBeUndefined();
  });

  it("normalizes a legacy version-1 document without canvas artifacts and fills empty", async () => {
    const root = await mkdtemp(join(tmpdir(), "nusashell-conversations-"));
    const path = join(root, "agent-conversations.json");
    await writeFile(path, JSON.stringify({
      version: 1,
      conversations: [{
        id: "conv-legacy-v1",
        title: "Legacy v1",
        createdAt: "2026-08-02T08:00:00.000Z",
        updatedAt: "2026-08-02T08:05:00.000Z",
        messages: [{ role: "user", content: "hi" }],
      }],
    }), "utf8");

    const loaded = await new AgentConversationStore(path).get("conv-legacy-v1");
    expect(loaded?.messages).toHaveLength(1);
    expect(loaded?.canvasArtifacts).toBeUndefined();
    expect(loaded?.activeCanvasArtifactId).toBeUndefined();
  });

  it("upserts, activates, and round-trips canvas artifacts", async () => {
    const root = await mkdtemp(join(tmpdir(), "nusashell-conversations-"));
    const path = join(root, "agent-conversations.json");
    const store = new AgentConversationStore(path, () => new Date("2026-08-03T10:00:00.000Z"), () => "conv-canvas");
    const conversation = await store.create();

    const artifact = {
      id: "conv-canvas:0:0",
      conversationId: conversation.id,
      sourceMessageId: "0",
      fenceIndex: 0,
      kind: "svg" as const,
      title: "Diagram",
      source: "<svg></svg>",
      createdAt: "2026-08-03T10:00:00.000Z",
      updatedAt: "2026-08-03T10:00:00.000Z",
    };
    await store.upsertCanvasArtifact(conversation.id, artifact);
    const activated = await store.setActiveCanvasArtifact(conversation.id, artifact.id);

    expect(activated.canvasArtifacts).toEqual([artifact]);
    expect(activated.activeCanvasArtifactId).toBe(artifact.id);

    const loaded = await new AgentConversationStore(path).get(conversation.id);
    expect(loaded?.canvasArtifacts?.[0]).toMatchObject({ id: artifact.id, kind: "svg" });
    expect(loaded?.activeCanvasArtifactId).toBe(artifact.id);
  });

  it("replaces an existing artifact on upsert by id and clears active when set to null", async () => {
    const root = await mkdtemp(join(tmpdir(), "nusashell-conversations-"));
    const path = join(root, "agent-conversations.json");
    const store = new AgentConversationStore(path, () => new Date("2026-08-03T10:00:00.000Z"), () => "conv-canvas-up");
    const conversation = await store.create();
    const base = {
      id: "conv-canvas-up:0:0",
      conversationId: conversation.id,
      sourceMessageId: "0",
      fenceIndex: 0,
      kind: "mermaid" as const,
      title: "Flow",
      source: "flowchart LR\n  A-->B",
      createdAt: "2026-08-03T10:00:00.000Z",
      updatedAt: "2026-08-03T10:00:00.000Z",
    };
    await store.upsertCanvasArtifact(conversation.id, base);
    const updated = await store.upsertCanvasArtifact(conversation.id, { ...base, source: "flowchart LR\n  A-->C", title: "Flow v2" });
    expect(updated.canvasArtifacts).toHaveLength(1);
    expect(updated.canvasArtifacts?.[0]?.source).toBe("flowchart LR\n  A-->C");
    expect(updated.canvasArtifacts?.[0]?.title).toBe("Flow v2");

    const cleared = await store.setActiveCanvasArtifact(conversation.id, null);
    expect(cleared.activeCanvasArtifactId).toBeUndefined();
  });

  it("evicts oldest non-active artifacts past the count cap", async () => {
    const root = await mkdtemp(join(tmpdir(), "nusashell-conversations-"));
    const path = join(root, "agent-conversations.json");
    let tick = 0;
    const store = new AgentConversationStore(
      path,
      () => new Date(1_900_000_000_000 + tick++ * 1000),
      () => "conv-canvas-evict",
    );
    const conversation = await store.create();
    // Activate the very first artifact before the count cap is reached, so the
    // eviction policy must keep it even though it is the oldest by createdAt.
    await store.upsertCanvasArtifact(conversation.id, {
      id: "conv-canvas-evict:0:0",
      conversationId: conversation.id,
      sourceMessageId: "0",
      fenceIndex: 0,
      kind: "svg",
      title: "Art 0",
      source: "<svg></svg>",
      createdAt: new Date(1_900_000_000_000).toISOString(),
      updatedAt: new Date(1_900_000_000_000).toISOString(),
    });
    await store.setActiveCanvasArtifact(conversation.id, "conv-canvas-evict:0:0");
    for (let index = 1; index < 22; index++) {
      await store.upsertCanvasArtifact(conversation.id, {
        id: `conv-canvas-evict:0:${index}`,
        conversationId: conversation.id,
        sourceMessageId: "0",
        fenceIndex: index,
        kind: "svg",
        title: `Art ${index}`,
        source: "<svg></svg>",
        createdAt: new Date(1_900_000_000_000 + index * 1000).toISOString(),
        updatedAt: new Date(1_900_000_000_000 + index * 1000).toISOString(),
      });
    }
    const loaded = await store.get(conversation.id);
    expect(loaded?.canvasArtifacts).toHaveLength(20);
    expect(loaded?.canvasArtifacts?.some((item) => item.id === "conv-canvas-evict:0:0")).toBe(true);
    expect(loaded?.canvasArtifacts?.some((item) => item.id === "conv-canvas-evict:0:1")).toBe(false);
  });

  it("rejects an artifact whose source exceeds the per-artifact byte cap", async () => {
    const root = await mkdtemp(join(tmpdir(), "nusashell-conversations-"));
    const path = join(root, "agent-conversations.json");
    const store = new AgentConversationStore(path, () => new Date("2026-08-03T10:00:00.000Z"), () => "conv-canvas-big");
    const conversation = await store.create();
    await expect(store.upsertCanvasArtifact(conversation.id, {
      id: "conv-canvas-big:0:0",
      conversationId: conversation.id,
      sourceMessageId: "0",
      fenceIndex: 0,
      kind: "html",
      title: "Big",
      source: "x".repeat(513 * 1024),
      createdAt: "2026-08-03T10:00:00.000Z",
      updatedAt: "2026-08-03T10:00:00.000Z",
    })).rejects.toThrow("exceeds the");
  });

  it("drops canvas artifacts whose conversationId does not match on normalize", async () => {
    const root = await mkdtemp(join(tmpdir(), "nusashell-conversations-"));
    const path = join(root, "agent-conversations.json");
    await writeFile(path, JSON.stringify({
      version: 2,
      conversations: [{
        id: "conv-mismatch",
        title: "Mismatch",
        createdAt: "2026-08-03T08:00:00.000Z",
        updatedAt: "2026-08-03T08:05:00.000Z",
        messages: [],
        canvasArtifacts: [{
          id: "conv-mismatch:0:0",
          conversationId: "other-conv",
          sourceMessageId: "0",
          fenceIndex: 0,
          kind: "svg",
          title: "Stray",
          source: "<svg></svg>",
          createdAt: "2026-08-03T08:00:00.000Z",
          updatedAt: "2026-08-03T08:00:00.000Z",
        }],
        activeCanvasArtifactId: "conv-mismatch:0:0",
      }],
    }), "utf8");

    const loaded = await new AgentConversationStore(path).get("conv-mismatch");
    expect(loaded?.canvasArtifacts).toBeUndefined();
    expect(loaded?.activeCanvasArtifactId).toBeUndefined();
  });
});

describe("AgentConversationStore I/O audit (race / lost-update)", () => {
  it("serializes concurrent appends so no delta is lost", async () => {
    const root = await mkdtemp(join(tmpdir(), "nusashell-conv-race-"));
    const path = join(root, "agent-conversations.json");
    const first = new AgentConversationStore(path, () => new Date("2026-08-02T10:00:00.000Z"), () => "conv-race");
    const created = await first.create();

    // Fire many appends concurrently (no await between them).
    const count = 50;
    const pending = Array.from({ length: count }, (_, i) =>
      first.appendMessage(created.id, { role: "user", content: `msg-${i}` }),
    );
    await Promise.all(pending);

    const loaded = await new AgentConversationStore(path).get(created.id);
    expect(loaded?.messages).toHaveLength(count);

    // Every message must be present — no lost update from interleaving.
    const contents = new Set((loaded?.messages ?? []).map((m) => m.content));
    for (let i = 0; i < count; i++) {
      expect(contents.has(`msg-${i}`)).toBe(true);
    }
  });
});

describe("AgentConversationStore Codex-style 2-file layout", () => {
  it("persists message history order across store instances via JSONL", async () => {
    const root = await mkdtemp(join(tmpdir(), "nusashell-conv-jsonl-"));
    const path = join(root, "agent-conversations.json");
    const first = new AgentConversationStore(path, () => new Date("2026-08-05T10:00:00.000Z"), () => "conv-hist");
    const created = await first.create();
    await first.appendMessage(created.id, { role: "user", content: "first" });
    await first.appendMessage(created.id, { role: "assistant", content: "second" });
    await first.appendMessage(created.id, { role: "user", content: "third" });

    const loaded = await new AgentConversationStore(path).get(created.id);
    expect(loaded?.messages.map((m) => m.content)).toEqual(["first", "second", "third"]);
    const jsonl = await readFile(join(root, "conversations", "conv-hist.jsonl"), "utf8");
    expect(jsonl.trim().split("\n")).toHaveLength(3);
  });

  it("persists metadata (workspace, model, checkpoint, title) across instances", async () => {
    const root = await mkdtemp(join(tmpdir(), "nusashell-conv-meta-"));
    const path = join(root, "agent-conversations.json");
    const first = new AgentConversationStore(path, () => new Date("2026-08-05T11:00:00.000Z"), () => "conv-meta");
    const created = await first.create();
    await first.appendMessage(created.id, { role: "user", content: "Room title from user" });
    await first.setWorkspace(created.id, "/tmp/ws");
    await first.setModel(created.id, { modelKey: "openai/gpt-5", effort: "medium" });
    await first.saveCheckpoint(created.id, {
      summary: "Summarized so far",
      compactedMessageCount: 1,
      via: "extractive",
      compactionCount: 1,
    });

    const loaded = await new AgentConversationStore(path).get(created.id);
    expect(loaded?.title).toBe("Room title from user");
    expect(loaded?.workspace).toBe("/tmp/ws");
    expect(loaded?.model).toEqual({ modelKey: "openai/gpt-5", effort: "medium" });
    expect(loaded?.checkpoint).toEqual({
      summary: "Summarized so far",
      compactedMessageCount: 1,
      via: "extractive",
      compactionCount: 1,
    });
    const metaRaw = await readFile(join(root, "conversations", "conv-meta.meta.json"), "utf8");
    expect(JSON.parse(metaRaw).messageCount).toBe(1);
  });

  it("serializes ~50 concurrent appends on the same conversation", async () => {
    const root = await mkdtemp(join(tmpdir(), "nusashell-conv-same-"));
    const path = join(root, "agent-conversations.json");
    const store = new AgentConversationStore(path, () => new Date("2026-08-05T12:00:00.000Z"), () => "conv-same");
    const created = await store.create();
    const count = 50;
    await Promise.all(
      Array.from({ length: count }, (_, i) =>
        store.appendMessage(created.id, { role: "user", content: `same-${i}` }),
      ),
    );
    const loaded = await store.get(created.id);
    expect(loaded?.messages).toHaveLength(count);
    const contents = new Set((loaded?.messages ?? []).map((m) => m.content));
    for (let i = 0; i < count; i++) expect(contents.has(`same-${i}`)).toBe(true);
  });

  it("does not block concurrent ops on different conversations", async () => {
    const root = await mkdtemp(join(tmpdir(), "nusashell-conv-diff-"));
    const path = join(root, "agent-conversations.json");
    let id = 0;
    const store = new AgentConversationStore(
      path,
      () => new Date("2026-08-05T13:00:00.000Z"),
      () => `conv-diff-${++id}`,
    );
    const a = await store.create();
    const b = await store.create();
    await Promise.all([
      ...Array.from({ length: 20 }, (_, i) => store.appendMessage(a.id, { role: "user", content: `a-${i}` })),
      ...Array.from({ length: 20 }, (_, i) => store.appendMessage(b.id, { role: "user", content: `b-${i}` })),
    ]);
    const loadedA = await store.get(a.id);
    const loadedB = await store.get(b.id);
    expect(loadedA?.messages).toHaveLength(20);
    expect(loadedB?.messages).toHaveLength(20);
    expect(loadedA?.messages.every((m) => m.content.startsWith("a-"))).toBe(true);
    expect(loadedB?.messages.every((m) => m.content.startsWith("b-"))).toBe(true);
  });

  it("get() rebuilds a long conversation with all messages in order", async () => {
    const root = await mkdtemp(join(tmpdir(), "nusashell-conv-long-"));
    const path = join(root, "agent-conversations.json");
    const store = new AgentConversationStore(path, () => new Date("2026-08-05T14:00:00.000Z"), () => "conv-long");
    const created = await store.create();
    const total = 80;
    for (let i = 0; i < total; i++) {
      await store.appendMessage(created.id, {
        role: i % 2 === 0 ? "user" : "assistant",
        content: `turn-${i}`,
      });
    }
    const loaded = await new AgentConversationStore(path).get(created.id);
    expect(loaded?.messages).toHaveLength(total);
    expect(loaded?.messages.map((m) => m.content)).toEqual(
      Array.from({ length: total }, (_, i) => `turn-${i}`),
    );
  });

  it("trims oldest messages when JSONL exceeds max_bytes soft cap", async () => {
    const root = await mkdtemp(join(tmpdir(), "nusashell-conv-trim-"));
    const path = join(root, "agent-conversations.json");
    // Tiny cap so a handful of large messages force drop of the oldest.
    const store = new AgentConversationStore(
      path,
      () => new Date("2026-08-05T15:00:00.000Z"),
      () => "conv-trim",
      2_000,
    );
    const created = await store.create();
    const payload = "x".repeat(400);
    for (let i = 0; i < 20; i++) {
      await store.appendMessage(created.id, { role: "user", content: `m${i}-${payload}` });
    }
    const loaded = await store.get(created.id);
    expect(loaded).not.toBeNull();
    expect(loaded!.messages.length).toBeGreaterThan(0);
    expect(loaded!.messages.length).toBeLessThan(20);
    // Newest survive; earliest bulk should be gone.
    expect(loaded!.messages.at(-1)?.content.startsWith("m19-")).toBe(true);
    expect(loaded!.messages.some((m) => m.content.startsWith("m0-"))).toBe(false);
  });

  it("delete() removes all per-conversation files", async () => {
    const root = await mkdtemp(join(tmpdir(), "nusashell-conv-del-"));
    const path = join(root, "agent-conversations.json");
    const store = new AgentConversationStore(path, () => new Date("2026-08-05T16:00:00.000Z"), () => "conv-del");
    const created = await store.create();
    await store.appendMessage(created.id, { role: "user", content: "bye" });
    await store.setWorkspace(created.id, "/tmp/x");
    await store.upsertCanvasArtifact(created.id, {
      id: "conv-del:0:0",
      conversationId: created.id,
      sourceMessageId: "0",
      fenceIndex: 0,
      kind: "svg",
      title: "Art",
      source: "<svg></svg>",
      createdAt: "2026-08-05T16:00:00.000Z",
      updatedAt: "2026-08-05T16:00:00.000Z",
    });
    const dir = join(root, "conversations");
    expect((await readdir(dir)).length).toBeGreaterThan(0);
    await store.delete(created.id);
    expect(await store.get(created.id)).toBeNull();
    expect(await readdir(dir)).toEqual([]);
  });

  it("replaceLastInterrupted rewrites the JSONL tail (length and last content correct)", async () => {
    const root = await mkdtemp(join(tmpdir(), "nusashell-conv-tail-"));
    const path = join(root, "agent-conversations.json");
    const store = new AgentConversationStore(path, () => new Date("2026-08-05T17:00:00.000Z"), () => "conv-tail");
    const created = await store.create();
    await store.appendMessage(created.id, { role: "user", content: "A" });
    await store.appendMessage(created.id, {
      role: "assistant",
      content: "B interrupted",
      status: "interrupted",
      resumeMessages: [{ role: "user", content: "A" }],
    });
    const updated = await store.replaceLastInterrupted(created.id, {
      role: "assistant",
      content: "B complete",
      rounds: 2,
    });
    expect(updated.messages).toHaveLength(2);
    expect(updated.messages[1]).toMatchObject({ content: "B complete", rounds: 2 });
    expect(updated.messages[1]?.status).toBeUndefined();

    const reloaded = await new AgentConversationStore(path).get(created.id);
    expect(reloaded?.messages).toHaveLength(2);
    expect(reloaded?.messages[1]?.content).toBe("B complete");
    const lines = (await readFile(join(root, "conversations", "conv-tail.jsonl"), "utf8"))
      .trim()
      .split("\n");
    expect(lines).toHaveLength(2);
    expect(JSON.parse(lines[1]!).content).toBe("B complete");
  });
});

