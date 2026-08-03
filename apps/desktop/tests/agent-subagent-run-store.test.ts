import { mkdtemp } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { describe, expect, it } from "vitest";
import { AgentConversationStore } from "../src/main/agent-conversation-store.js";
import type { AgentSubagentRun } from "../src/shared/agent-conversation-contract.js";

describe("AgentConversationStore — subagent runs", () => {
  it("upserts a subagent run and sets it active", async () => {
    const root = await mkdtemp(join(tmpdir(), "nusashell-subagent-"));
    const store = new AgentConversationStore(join(root, "conversations.json"));
    const conv = await store.create();

    const run: AgentSubagentRun = {
      id: "run-1",
      conversationId: conv.id,
      sourceMessageId: "0",
      runId: "trace-1",
      providerId: "cursor",
      title: "Refactor auth",
      prompt: "Refactor the auth module",
      status: "running",
      createdAt: new Date().toISOString(),
      updatedAt: new Date().toISOString(),
    };

    const after = await store.upsertSubagentRun(conv.id, run);
    expect(after.subagentRuns).toHaveLength(1);
    expect(after.subagentRuns?.[0]).toMatchObject({ runId: "trace-1", status: "running" });

    const active = await store.setActiveSubagentRun(conv.id, "trace-1");
    expect(active.activeSubagentRunId).toBe("trace-1");
  });

  it("updates subagent run status", async () => {
    const root = await mkdtemp(join(tmpdir(), "nusashell-subagent-"));
    const store = new AgentConversationStore(join(root, "conversations.json"));
    const conv = await store.create();

    const run: AgentSubagentRun = {
      id: "run-2",
      conversationId: conv.id,
      sourceMessageId: "0",
      runId: "trace-2",
      providerId: "codex",
      prompt: "Fix the bug",
      status: "running",
      createdAt: new Date().toISOString(),
      updatedAt: new Date().toISOString(),
    };

    await store.upsertSubagentRun(conv.id, run);
    const updated = await store.updateSubagentRunStatus(conv.id, "trace-2", "ok", { summary: "Done" });
    expect(updated.subagentRuns?.[0]).toMatchObject({ status: "ok", summary: "Done" });
  });

  it("clears active subagent run id", async () => {
    const root = await mkdtemp(join(tmpdir(), "nusashell-subagent-"));
    const store = new AgentConversationStore(join(root, "conversations.json"));
    const conv = await store.create();

    const run: AgentSubagentRun = {
      id: "run-3",
      conversationId: conv.id,
      sourceMessageId: "0",
      runId: "trace-3",
      providerId: "cursor",
      prompt: "Test",
      status: "running",
      createdAt: new Date().toISOString(),
      updatedAt: new Date().toISOString(),
    };

    await store.upsertSubagentRun(conv.id, run);
    await store.setActiveSubagentRun(conv.id, "trace-3");
    const cleared = await store.setActiveSubagentRun(conv.id, null);
    expect(cleared.activeSubagentRunId).toBeUndefined();
  });

  it("persists subagent runs across store instances", async () => {
    const root = await mkdtemp(join(tmpdir(), "nusashell-subagent-"));
    const path = join(root, "conversations.json");
    const first = new AgentConversationStore(path);
    const conv = await first.create();

    const run: AgentSubagentRun = {
      id: "run-4",
      conversationId: conv.id,
      sourceMessageId: "0",
      runId: "trace-4",
      providerId: "gemini",
      title: "Write tests",
      prompt: "Write unit tests",
      status: "ok",
      summary: "All tests written",
      createdAt: new Date().toISOString(),
      updatedAt: new Date().toISOString(),
    };

    await first.upsertSubagentRun(conv.id, run);
    await first.setActiveSubagentRun(conv.id, "trace-4");

    const second = new AgentConversationStore(path);
    const loaded = await second.get(conv.id);
    expect(loaded?.subagentRuns).toHaveLength(1);
    expect(loaded?.subagentRuns?.[0]).toMatchObject({ runId: "trace-4", status: "ok", summary: "All tests written" });
    expect(loaded?.activeSubagentRunId).toBe("trace-4");
  });

  it("persists subagent stream steps for review replay", async () => {
    const root = await mkdtemp(join(tmpdir(), "nusashell-subagent-"));
    const path = join(root, "conversations.json");
    const first = new AgentConversationStore(path);
    const conv = await first.create();

    const run: AgentSubagentRun = {
      id: "run-6",
      conversationId: conv.id,
      sourceMessageId: "0",
      runId: "trace-6",
      providerId: "cursor",
      prompt: "Build profile",
      status: "running",
      createdAt: new Date().toISOString(),
      updatedAt: new Date().toISOString(),
    };

    await first.upsertSubagentRun(conv.id, run);
    await first.updateSubagentRunStatus(conv.id, "trace-6", "ok", {
      summary: "Done",
      steps: [
        { type: "text", content: "**File** `index.html` ready" },
        { type: "tool_calls", calls: [{ id: "c1", name: "Edit", ok: true, output: "{}" }] },
        { type: "plan", steps: [{ text: "Write HTML", done: true }, { text: "Open browser" }] },
      ],
    });

    const second = new AgentConversationStore(path);
    const loaded = await second.get(conv.id);
    expect(loaded?.subagentRuns?.[0]).toMatchObject({
      status: "ok",
      summary: "Done",
      steps: [
        { type: "text", content: "**File** `index.html` ready" },
        { type: "tool_calls", calls: [{ id: "c1", name: "Edit", ok: true, output: "{}" }] },
        { type: "plan", steps: [{ text: "Write HTML", done: true }, { text: "Open browser" }] },
      ],
    });
  });

  it("rejects subagent run with mismatched conversationId", async () => {
    const root = await mkdtemp(join(tmpdir(), "nusashell-subagent-"));
    const store = new AgentConversationStore(join(root, "conversations.json"));
    const conv = await store.create();

    const run: AgentSubagentRun = {
      id: "run-5",
      conversationId: "wrong-id",
      sourceMessageId: "0",
      runId: "trace-5",
      providerId: "cursor",
      prompt: "Test",
      status: "running",
      createdAt: new Date().toISOString(),
      updatedAt: new Date().toISOString(),
    };

    await expect(store.upsertSubagentRun(conv.id, run)).rejects.toThrow();
  });
});
