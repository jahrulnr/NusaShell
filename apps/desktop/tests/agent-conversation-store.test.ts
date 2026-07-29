import { mkdtemp, readFile, writeFile } from "node:fs/promises";
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
    await first.appendMessage(created.id, { role: "assistant", content: "I found the issue." });
    await first.saveCheckpoint(created.id, {
      summary: "The user asked to investigate MCP logs.",
      compactedMessageCount: 2,
      via: "provider",
    });

    const second = new AgentConversationStore(path);
    const loaded = await second.get(created.id);
    expect(loaded?.title).toBe("Investigate MCP logs");
    expect(loaded?.messages).toHaveLength(2);
    expect(loaded?.checkpoint).toEqual({
      summary: "The user asked to investigate MCP logs.",
      compactedMessageCount: 2,
      via: "provider",
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
});
