import { describe, expect, it } from "vitest";
import {
  buildAgentContext,
  mergeCompactionCheckpoint,
  searchConversations,
  renderAssistantMarkdown,
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

  it("renders GFM tables while keeping raw HTML as text", () => {
    expect(renderAssistantMarkdown("## Tools\n\n| Tool | Function |\n| --- | --- |\n| **createNote** | Create a note |\n\n<script>alert(1)</script>")).toContain("<table>");
    expect(renderAssistantMarkdown("<script>alert(1)</script>")).toContain("&lt;script&gt;");
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
});
