import { describe, expect, it } from "vitest";
import {
  buildAgentContext,
  clampToolText,
  composerTextareaSize,
  describeToolActivity,
  formatMessageTimestamp,
  formatToolOutput,
  formatToolTerminalInput,
  mergeCompactionCheckpoint,
  searchConversations,
  renderAssistantMarkdown,
  renderReasoningMarkdown,
  renderToolCodeHtml,
  summarizeToolArgs,
  toConversationToolCall,
} from "../src/renderer/agent-conversation-ui.js";

describe("agent conversation UI helpers", () => {
  it("rebuilds provider context from the saved compaction checkpoint", () => {
    const messages = [
      { role: "user" as const, content: "old question" },
      { role: "assistant" as const, content: "old answer" },
      { role: "user" as const, content: "recent question" },
    ];

    expect(buildAgentContext({
      messages,
      checkpoint: { summary: "Earlier work was completed.", compactedMessageCount: 2, via: "provider" },
    })).toEqual([
      { role: "system", content: "Conversation summary:\nEarlier work was completed." },
      { role: "user", content: "recent question" },
    ]);
  });

  it("translates a runner checkpoint into an absolute durable checkpoint", () => {
    expect(mergeCompactionCheckpoint(
      { summary: "Previous", compactedMessageCount: 4, via: "provider" },
      { summary: "Updated", compactedMessageCount: 3, via: "extractive" },
      10,
    )).toEqual({
      summary: "Updated",
      compactedMessageCount: 6,
      via: "extractive",
    });
  });

  it("searches conversation titles without changing newest-first order", () => {
    const conversations = [
      { id: "2", title: "Deploy notes", createdAt: "", updatedAt: "2", messageCount: 1 },
      { id: "1", title: "Investigate MCP", createdAt: "", updatedAt: "1", messageCount: 2 },
    ];

    expect(searchConversations(conversations, "mcp").map((item) => item.id)).toEqual(["1"]);
  });

  it("renders GFM tables and sanitizes dangerous HTML", () => {
    expect(renderAssistantMarkdown("## Tools\n\n| Tool | Function |\n| --- | --- |\n| **createNote** | Create a note |\n\n<script>alert(1)</script>")).toContain("<table>");
    expect(renderAssistantMarkdown("<script>alert(1)</script>")).not.toContain("<script>");
  });

  it("renders safe HTML tags like br and kbd", () => {
    expect(renderAssistantMarkdown("Line 1<br>Line 2")).toContain("<br>");
    expect(renderAssistantMarkdown("Press <kbd>Ctrl+C</kbd> to copy")).toContain("<kbd>");
  });

  it("renders model reasoning as safe markdown", () => {
    expect(renderReasoningMarkdown("I should **inspect the logs** first.")).toContain("<strong>inspect the logs</strong>");
    expect(renderReasoningMarkdown("<img src=x onerror=alert(1)>")).not.toContain("onerror");
  });

  it("summarizes tool args and formats terminal input/output previews", () => {
    expect(summarizeToolArgs({ path: "resources/agent/docs/ui/plugins.md" })).toBe("\"resources/agent/docs/ui/plugins.md\"");
    expect(summarizeToolArgs({ a: 1, b: 2 })).toBe("2 args");
    expect(formatToolTerminalInput("docs_search", { query: "dokumentasi" })).toBe("docs_search(\"dokumentasi\")");
    expect(formatToolTerminalInput("docs_read", { path: "ui/agent.md", max_chars: 200 })).toBe("docs_read(path=\"ui/agent.md\", max_chars=200)");
    expect(formatToolTerminalInput("docs_list", {})).toBe("docs_list()");
    expect(formatToolOutput({ ok: true, items: ["a"] })).toContain('"ok": true');
    expect(renderToolCodeHtml('docs_search("dokumentasi")')).toContain('class="tok-cmd"');
    expect(renderToolCodeHtml('docs_search("dokumentasi")')).toContain('class="tok-str"');
    expect(toConversationToolCall({
      id: "call-1",
      name: "docs_list",
      ok: true,
      args: { limit: 20 },
      result: { docs: ["plugins.md"] },
    })).toEqual({
      id: "call-1",
      name: "docs_list",
      ok: true,
      args: { limit: 20 },
      output: "{\n  \"docs\": [\n    \"plugins.md\"\n  ]\n}",
    });
  });

  it("does not auto-link .md filenames in reasoning as blue Moldova URLs", () => {
    const html = renderReasoningMarkdown('Read "mcp-tools.md" or "plugins.md" next.');
    expect(html).not.toContain("<a ");
    expect(html).toContain("mcp-tools.md");
    expect(html).toContain("plugins.md");
  });

  it("keeps clamped tool output and args within the persistence validator caps", () => {
    expect(clampToolText("x".repeat(20_000))).toHaveLength(12_000);
    expect(clampToolText("x".repeat(100), 50)).toHaveLength(50);
    expect(clampToolText("short")).toBe("short");

    const hugeOutput = toConversationToolCall({ id: "c1", name: "terminal_read", ok: true, output: "y".repeat(50_000) });
    expect(hugeOutput.output).toHaveLength(12_000);

    const hugeArgs = toConversationToolCall({
      id: "c2",
      name: "files_write",
      ok: true,
      args: { path: "/a.txt", content: "z".repeat(20_000) },
    });
    expect(JSON.stringify(hugeArgs.args).length).toBeLessThanOrEqual(8_000);
  });

  it("formats persisted message timestamps as compact local metadata", () => {
    expect(formatMessageTimestamp("2026-07-29T10:05:00.000Z", "en-US", "UTC")).toBe("Jul 29, 10:05 AM");
    expect(formatMessageTimestamp("not-a-date", "en-US", "UTC")).toBe("");
  });

  it("summarizes completed tool activity without implying live progress", () => {
    expect(describeToolActivity([
      { id: "1", name: "notes.list", ok: true },
      { id: "2", name: "notes.create", ok: false, error: "Permission denied" },
    ])).toEqual({
      label: "2 tool calls",
      succeeded: 1,
      failed: 1,
    });
  });

  it("grows the composer through ten rows before enabling internal scroll", () => {
    expect(composerTextareaSize({
      scrollHeight: 76,
      lineHeight: 20,
      paddingTop: 7,
      paddingBottom: 7,
    })).toEqual({ height: 76, overflowY: "hidden" });
    expect(composerTextareaSize({
      scrollHeight: 260,
      lineHeight: 20,
      paddingTop: 7,
      paddingBottom: 7,
    })).toEqual({ height: 214, overflowY: "auto" });
  });

  it("restores persisted image and document attachments as provider content parts", () => {
    expect(buildAgentContext({
      messages: [{
        role: "user",
        content: "Review these",
        attachments: [
          { type: "image", dataUrl: "data:image/png;base64,YQ==", mediaType: "image/png", name: "shot.png" },
          { type: "file", dataUrl: "data:application/pdf;base64,YQ==", mediaType: "application/pdf", name: "brief.pdf" },
        ],
      }],
    })).toEqual([{
      role: "user",
      content: [
        { type: "text", text: "Review these" },
        { type: "image", dataUrl: "data:image/png;base64,YQ==", name: "shot.png" },
        { type: "file", dataUrl: "data:application/pdf;base64,YQ==", mediaType: "application/pdf", name: "brief.pdf" },
      ],
    }]);
  });

  it("restores text attachments as named text context for any chat model", () => {
    expect(buildAgentContext({
      messages: [{
        role: "user",
        content: "Review this source",
        attachments: [{ type: "text", content: "body { color: red; }", mediaType: "text/plain", name: "theme.css" }],
      }],
    })).toEqual([{
      role: "user",
      content: [
        { type: "text", text: "Review this source" },
        { type: "text", text: "Attached text file: theme.css\n\nbody { color: red; }" },
      ],
    }]);
  });
});
