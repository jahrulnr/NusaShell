import { describe, expect, it } from "vitest";
import {
  buildAgentContext,
  buildContinueContext,
  classifyTurnError,
  CONTINUE_STEER,
  clampToolText,
  composerTextareaSize,
  describeToolActivity,
  getConversationRoomMetadata,
  formatMessageTimestamp,
  formatToolOutput,
  formatToolTerminalInput,
  hasToolResumeSnapshot,
  mergeCompactionCheckpoint,
  mergeConversationMessages,
  conversationSearchEmptyCopy,
  searchConversations,
  renderAssistantMarkdown,
  renderReasoningMarkdown,
  renderToolCodeHtml,
  summarizeToolArgs,
  toConversationToolCall,
} from "../src/renderer/agent-conversation-ui.js";

describe("agent conversation UI helpers", () => {
  it("does not claim a tool turn is resumable from display history alone", () => {
    expect(hasToolResumeSnapshot({
      role: "assistant",
      status: "interrupted",
      toolCalls: [{ id: "call-1", name: "read", ok: true }],
      steps: [{ type: "tool_calls", calls: [{ id: "call-1", name: "read", ok: true }] }],
    })).toBe(false);
  });

  it("recognizes a durable provider transcript containing settled tools", () => {
    expect(hasToolResumeSnapshot({
      role: "assistant",
      status: "interrupted",
      resumeMessages: [
        { role: "assistant", content: "", toolCalls: [{ id: "call-1", name: "read", args: {} }] },
        { role: "tool", toolCallId: "call-1", name: "read", content: "status=success" },
      ],
    })).toBe(true);
  });

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
      compactionCount: 2,
    });
  });

  it("summarizes durable room diagnostics without double-counting message steps", () => {
    expect(getConversationRoomMetadata({
      id: "conv-room",
      messages: [
        { role: "assistant", toolCalls: [{ id: "a", name: "todo", ok: true }], steps: [{ type: "tool_calls", calls: [{ id: "a", name: "todo", ok: true }] }] },
        { role: "assistant", steps: [{ type: "tool_calls", calls: [{ id: "b", name: "shell", ok: true }, { id: "c", name: "shell", ok: false }] }] },
      ],
      checkpoint: { summary: "old", compactedMessageCount: 1, via: "provider", compactionCount: 3 },
      subagentRuns: [{ steps: [{ type: "tool_calls", calls: [{ id: "d", name: "grep", ok: true }] }] }],
    })).toEqual({ conversationId: "conv-room", compactionCount: 3, toolCallCount: 4 });
  });

  it("searches conversation titles without changing newest-first order", () => {
    const conversations = [
      { id: "2", title: "Deploy notes", createdAt: "", updatedAt: "2", messageCount: 1 },
      { id: "1", title: "Investigate MCP", createdAt: "", updatedAt: "1", messageCount: 2 },
    ];

    expect(searchConversations(conversations, "mcp").map((item) => item.id)).toEqual(["1"]);
  });

  it("does not match phrases that only appear in message content (title-only search scope)", () => {
    // List payloads are AgentConversationSummary: title + counts, no thread body.
    // A phrase from the thread must not invent a hit when only titles are searchable.
    const conversations = [
      {
        id: "content-only",
        title: "Planning session",
        createdAt: "",
        updatedAt: "2",
        messageCount: 3,
        messages: [
          { role: "user", content: "remember the phrase unique-mcp-token-xyz" },
          { role: "assistant", content: "Noted the unique-mcp-token-xyz requirement." },
        ],
      },
      {
        id: "title-hit",
        title: "unique-mcp-token-xyz follow-up",
        createdAt: "",
        updatedAt: "1",
        messageCount: 1,
      },
    ];

    expect(searchConversations(conversations, "unique-mcp-token-xyz").map((item) => item.id)).toEqual(["title-hit"]);
    expect(searchConversations(conversations, "Planning")).toHaveLength(1);
  });

  it("returns no rows for a non-matching title query (empty results, not emptied list)", () => {
    const conversations = [
      { id: "1", title: "Deploy notes", createdAt: "", updatedAt: "1", messageCount: 1 },
    ];
    expect(searchConversations(conversations, "mcp")).toEqual([]);
  });

  it("uses honest empty-state copy for title-scoped search vs no conversations", () => {
    expect(conversationSearchEmptyCopy(true)).toBe("No conversations with this title.");
    expect(conversationSearchEmptyCopy(false)).toBe("No conversations yet.");
  });

  it("renders GFM tables and sanitizes dangerous HTML", () => {
    expect(renderAssistantMarkdown("## Tools\n\n| Tool | Function |\n| --- | --- |\n| **createNote** | Create a note |\n\n<script>alert(1)</script>")).toContain("agent-markdown-table-scroll");
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

    const hugeOutput = toConversationToolCall({ id: "c1", name: "read", ok: true, output: "y".repeat(50_000) });
    expect(hugeOutput.output).toHaveLength(12_000);

    const hugeArgs = toConversationToolCall({
      id: "c2",
      name: "write",
      ok: true,
      args: { path: "/a.txt", content: "z".repeat(20_000) },
    });
    expect(JSON.stringify(hugeArgs.args).length).toBeLessThanOrEqual(8_000);
  });

  it("defaults missing args to {} in toConversationToolCall", () => {
    const call = toConversationToolCall({ id: "c1", name: "mcp_list", ok: true, output: "[]" });
    expect(call.args).toEqual({});
    expect(call.id).toBe("c1");
    expect(call.name).toBe("mcp_list");
  });

  it("uses the nested canonical projection so the rendered card matches the provider result", () => {
    const providerContent = '<untrusted_tool_result source="mcp_files_list" format="terminal">\n' +
      "\nstatus=success\ntruncated=false\n\nentries=[]\n" +
      "</untrusted_tool_result>";
    const call = toConversationToolCall({
      id: "c-terminal",
      name: "mcp_files_list",
      ok: true,
      args: {},
      result: { entries: [] },
      toolResult: { modelOutput: providerContent },
    });
    expect(call.modelOutput).toBe(providerContent);
    expect(call.output).toBe(providerContent);
  });

  it("keeps nested structured content for renderer-side transcript persistence", () => {
    const structuredContent = { ok: true, runId: "run-renderer", summary: "Done" };
    const call = toConversationToolCall({
      id: "subagent:0",
      name: "subagent",
      ok: true,
      args: {},
      toolResult: {
        modelOutput: "status=success\ntruncated=false\n\nrunId=run-renderer",
        status: "success",
        structuredContent,
        metadata: { truncated: false },
      },
    });

    expect(call.structuredContent).toEqual(structuredContent);
    expect(call.status).toBe("success");
    expect(call.truncated).toBe(false);
  });

  it("defaults missing args to {} in toProviderToolCall via buildAgentContext", () => {
    const messages = [
      { role: "user" as const, content: "List plugins" },
      {
        role: "assistant" as const,
        content: "",
        toolCalls: [{ id: "call-1", name: "mcp_list", ok: true, output: "[]" }],
      },
      { role: "tool" as const, toolCallId: "call-1", name: "mcp_list", content: "[]" },
      { role: "user" as const, content: "Continue" },
    ];
    const context = buildAgentContext({ messages });
    const assistant = context.find(
      (m) => m.role === "assistant" && "toolCalls" in m && Array.isArray(m.toolCalls) && m.toolCalls.length > 0,
    );
    expect(assistant).toBeDefined();
    if (assistant && "toolCalls" in assistant && assistant.toolCalls) {
      expect(assistant.toolCalls[0].args).toEqual({});
    }
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

  it("skips interrupted assistant messages when building provider context", () => {
    expect(buildAgentContext({
      messages: [
        { role: "user", content: "Create a note" },
        { role: "assistant", content: "Turn interrupted.", status: "interrupted", resumeMessages: [{ role: "user", content: "Create a note" }] },
      ],
    })).toEqual([
      { role: "user", content: "Create a note" },
    ]);
  });

  it("reconstructs assistant toolCalls into an assistant message plus one role:tool result per call", () => {
    const result = buildAgentContext({
      messages: [
        { role: "user", content: "Write a file then list the dir" },
        {
          role: "assistant",
          content: "Done.",
          toolCalls: [
            { id: "call-1", name: "write", ok: true, args: { path: "/a.txt", content: "hi" }, output: "wrote 2 bytes" },
            { id: "call-2", name: "list", ok: false, args: { path: "/" }, error: "Permission denied" },
          ],
        },
      ],
    });

    expect(result).toEqual([
      { role: "user", content: "Write a file then list the dir" },
      { role: "assistant", content: "", toolCalls: [
        { id: "call-1", name: "write", args: { path: "/a.txt", content: "hi" } },
        { id: "call-2", name: "list", args: { path: "/" } },
      ] },
      { role: "tool", toolCallId: "call-1", name: "write", content: "wrote 2 bytes" },
      { role: "tool", toolCallId: "call-2", name: "list", content: "[TOOL ERROR] Permission denied" },
      { role: "assistant", content: "Done." },
    ]);
  });

  it("rebuilds multi-round steps as interleaved assistant/tool/assistant/tool + final text after tools", () => {
    const result = buildAgentContext({
      messages: [
        { role: "user", content: "Read go.mod then grep for TODO" },
        {
          role: "assistant",
          content: "I read go.mod and found no TODOs.",
          toolCalls: [
            { id: "call-1", name: "read", ok: true, args: { path: "go.mod" }, output: "module example" },
            { id: "call-2", name: "grep", ok: true, args: { pattern: "TODO" }, output: "no matches" },
          ],
          steps: [
            { type: "tool_calls", calls: [
              { id: "call-1", name: "read", ok: true, args: { path: "go.mod" }, output: "module example" },
            ] },
            { type: "tool_calls", calls: [
              { id: "call-2", name: "grep", ok: true, args: { pattern: "TODO" }, output: "no matches" },
            ] },
            { type: "text", content: "I read go.mod and found no TODOs." },
          ],
        },
      ],
    });

    expect(result).toEqual([
      { role: "user", content: "Read go.mod then grep for TODO" },
      { role: "assistant", content: "", toolCalls: [
        { id: "call-1", name: "read", args: { path: "go.mod" } },
      ] },
      { role: "tool", toolCallId: "call-1", name: "read", content: "module example" },
      { role: "assistant", content: "", toolCalls: [
        { id: "call-2", name: "grep", args: { pattern: "TODO" } },
      ] },
      { role: "tool", toolCallId: "call-2", name: "grep", content: "no matches" },
      { role: "assistant", content: "I read go.mod and found no TODOs." },
    ]);
  });

  it("skips reasoning steps when rebuilding from steps (keeps confabulated reasoning out of next prompt)", () => {
    const result = buildAgentContext({
      messages: [
        { role: "user", content: "go" },
        {
          role: "assistant",
          content: "Done.",
          toolCalls: [{ id: "call-1", name: "read", ok: true, args: { path: "/a" }, output: "hi" }],
          steps: [
            { type: "reasoning", content: "I should skip this tool because it failed before" },
            { type: "tool_calls", calls: [
              { id: "call-1", name: "read", ok: true, args: { path: "/a" }, output: "hi" },
            ] },
            { type: "text", content: "Done." },
          ],
        },
      ],
    });

    expect(result).toEqual([
      { role: "user", content: "go" },
      { role: "assistant", content: "", toolCalls: [
        { id: "call-1", name: "read", args: { path: "/a" } },
      ] },
      { role: "tool", toolCallId: "call-1", name: "read", content: "hi" },
      { role: "assistant", content: "Done." },
    ]);
  });

  it("emits mid-turn text steps between tool rounds as standalone assistant messages", () => {
    const result = buildAgentContext({
      messages: [
        { role: "user", content: "go" },
        {
          role: "assistant",
          content: "Mid text\nFinal answer",
          toolCalls: [
            { id: "call-1", name: "read", ok: true, args: { path: "/a" }, output: "a" },
            { id: "call-2", name: "read", ok: true, args: { path: "/b" }, output: "b" },
          ],
          steps: [
            { type: "tool_calls", calls: [
              { id: "call-1", name: "read", ok: true, args: { path: "/a" }, output: "a" },
            ] },
            { type: "text", content: "Mid text" },
            { type: "tool_calls", calls: [
              { id: "call-2", name: "read", ok: true, args: { path: "/b" }, output: "b" },
            ] },
            { type: "text", content: "Final answer" },
          ],
        },
      ],
    });

    expect(result).toEqual([
      { role: "user", content: "go" },
      { role: "assistant", content: "", toolCalls: [
        { id: "call-1", name: "read", args: { path: "/a" } },
      ] },
      { role: "tool", toolCallId: "call-1", name: "read", content: "a" },
      { role: "assistant", content: "Mid text" },
      { role: "assistant", content: "", toolCalls: [
        { id: "call-2", name: "read", args: { path: "/b" } },
      ] },
      { role: "tool", toolCallId: "call-2", name: "read", content: "b" },
      { role: "assistant", content: "Final answer" },
    ]);
  });

  it("wraps mcp_* tool results in the untrusted_tool_result envelope and leaves non-mcp results bare", () => {
    const result = buildAgentContext({
      messages: [
        { role: "user", content: "run it" },
        {
          role: "assistant",
          content: "",
          toolCalls: [
            { id: "call-1", name: "mcp_search", ok: true, args: { query: "inbox" }, output: "Subject: hello\nBody: this is a long enough message body to trigger wrapping" },
            { id: "call-2", name: "read", ok: true, args: { path: "/a.txt" }, output: "plain content body" },
          ],
        },
      ],
    });

    const mcpResult = result.find((m) => m.role === "tool" && m.name === "mcp_search");
    expect(mcpResult?.content).toContain("<untrusted_tool_result source=\"mcp_search\" format=\"terminal\">");
    expect(mcpResult?.content).toContain("Subject: hello");

    const filesResult = result.find((m) => m.role === "tool" && m.name === "read");
    expect(filesResult?.content).toBe("plain content body");
    expect(filesResult?.content).not.toContain("untrusted_tool_result");
  });

  it("emits an empty tool result when a persisted call has neither output nor error", () => {
    const result = buildAgentContext({
      messages: [
        { role: "user", content: "go" },
        { role: "assistant", content: "", toolCalls: [{ id: "call-1", name: "read", ok: true, args: { path: "/a" } }] },
      ],
    });

    expect(result).toEqual([
      { role: "user", content: "go" },
      { role: "assistant", content: "", toolCalls: [{ id: "call-1", name: "read", args: { path: "/a" } }] },
      { role: "tool", toolCallId: "call-1", name: "read", content: "" },
    ]);
  });

  it("skips toolCalls missing id or name but keeps the rest stable in order", () => {
    const result = buildAgentContext({
      messages: [
        { role: "user", content: "go" },
        {
          role: "assistant",
          content: "partial",
          toolCalls: [
            { id: "call-1", name: "write", ok: true, args: { path: "/a" }, output: "ok" },
            { name: "no-id", ok: true, output: "x" },
            { id: "call-3", ok: true, output: "y" },
            { id: "call-4", name: "read", ok: true, args: { path: "/b" }, output: "b" },
          ],
        },
      ],
    });

    expect(result.map((m) => m.role)).toEqual(["user", "assistant", "tool", "tool", "assistant"]);
    expect(result[1]).toMatchObject({ role: "assistant", toolCalls: [
      { id: "call-1", name: "write" },
      { id: "call-4", name: "read" },
    ] });
    expect(result[2]).toMatchObject({ role: "tool", toolCallId: "call-1" });
    expect(result[3]).toMatchObject({ role: "tool", toolCallId: "call-4" });
    expect(result[4]).toMatchObject({ role: "assistant", content: "partial" });
  });

  it("reconstructs tool context on the checkpoint branch alongside the summary system message", () => {
    const result = buildAgentContext({
      messages: [
        { role: "user", content: "old question" },
        {
          role: "assistant",
          content: "old answer",
          toolCalls: [{ id: "call-1", name: "write", ok: true, args: { path: "/a" }, output: "wrote" }],
        },
        { role: "user", content: "follow up" },
      ],
      checkpoint: { summary: "Earlier file was written.", compactedMessageCount: 1, via: "provider" },
    });

    expect(result).toEqual([
      { role: "system", content: "Conversation summary:\nEarlier file was written." },
      { role: "assistant", content: "", toolCalls: [{ id: "call-1", name: "write", args: { path: "/a" } }] },
      { role: "tool", toolCallId: "call-1", name: "write", content: "wrote" },
      { role: "assistant", content: "old answer" },
      { role: "user", content: "follow up" },
    ]);
  });

  // --- Dual-space: full transcript for UI, compact only for model send ---

  it("dual-space: buildAgentContext with checkpoint is shorter than full messages while store stays intact", () => {
    const messages = [
      { role: "user" as const, content: "x".repeat(5000) },
      { role: "assistant" as const, content: "x".repeat(5000) },
      { role: "user" as const, content: "x".repeat(5000) },
      { role: "assistant" as const, content: "x".repeat(5000) },
      { role: "user" as const, content: "latest question" },
    ];
    const checkpoint = { summary: "Old turns summarized.", compactedMessageCount: 4, via: "provider" as const };
    const conversation = { messages, checkpoint };

    const providerContext = buildAgentContext(conversation);

    // Provider context starts with summary system message + only the last user turn.
    expect(providerContext[0]).toEqual({ role: "system", content: "Conversation summary:\nOld turns summarized." });
    expect(providerContext).toHaveLength(2);
    expect(providerContext[1]).toEqual({ role: "user", content: "latest question" });

    // Full transcript is untouched — UI would still render all 5 messages.
    expect(messages.length).toBe(5);
    expect(messages[0]).toEqual({ role: "user", content: "x".repeat(5000) });

    // Provider context is significantly shorter than full thread.
    const providerChars = JSON.stringify(providerContext).length;
    const fullChars = JSON.stringify(messages).length;
    expect(providerChars).toBeLessThan(fullChars / 3);
  });

  // --- buildContinueContext (Continue incomplete stream) ---

  describe("buildContinueContext", () => {
    it("text continue: interrupted with body → context ends with assistant partial + continue steer", () => {
      const conversation = {
        messages: [
          { role: "user", content: "Write an essay" },
          { role: "assistant", content: "Halfway through the essay", status: "interrupted", interruptReason: "provider" },
        ],
      };
      const ctx = buildContinueContext(conversation);
      // Interrupted is filtered from base, then re-added as assistant + steer.
      expect(ctx).toEqual([
        { role: "user", content: "Write an essay" },
        { role: "assistant", content: "Halfway through the essay" },
        { role: "user", content: CONTINUE_STEER },
      ]);
    });

    it("text continue: interrupted is not duplicated from base", () => {
      const conversation = {
        messages: [
          { role: "user", content: "Write an essay" },
          { role: "assistant", content: "Halfway", status: "interrupted" },
        ],
      };
      const ctx = buildContinueContext(conversation);
      // Only one user "Write an essay" (base filters interrupted, then we add
      // assistant + steer — no dup of the interrupted row).
      const userWrites = ctx.filter((m) => m.role === "user" && m.content === "Write an essay");
      expect(userWrites).toHaveLength(1);
    });

    it("tool resume: interrupted with resumeMessages → returns resumeMessages (tool path)", () => {
      const resumeMessages = [
        { role: "user", content: "Create a note" },
        { role: "assistant", toolCalls: [{ id: "c1", name: "notes.create", args: {} }] },
        { role: "tool", toolCallId: "c1", name: "notes.create", content: "ok" },
      ];
      const conversation = {
        messages: [
          { role: "user", content: "Create a note" },
          { role: "assistant", content: "", status: "interrupted", resumeMessages, toolCalls: [{ id: "c1", name: "notes.create", args: {}, ok: true, output: "ok" }] },
        ],
      };
      const ctx = buildContinueContext(conversation);
      // Tool path: returns resumeMessages as-is (caller sets resume: true).
      expect(ctx).toEqual(resumeMessages);
    });

    it("text continue: inject-only resumeMessages (no tools settled) does not take tool path", () => {
      // Pre-tool provider fail seals inject+user as resumeMessages even with no tools.
      // Retry must still text-continue so the model sees the partial bubble.
      const conversation = {
        messages: [
          { role: "user", content: "Hunt bugs in NusaShell" },
          {
            role: "assistant",
            content: "Siap! Task ini cocok banget dengan skill codebase-review.",
            status: "interrupted",
            interruptReason: "provider",
            resumeMessages: [
              { role: "system", content: "You are the NusaShell agent." },
              { role: "user", content: "Hunt bugs in NusaShell" },
            ],
          },
        ],
      };
      const ctx = buildContinueContext(conversation);
      expect(ctx).toEqual([
        { role: "user", content: "Hunt bugs in NusaShell" },
        { role: "assistant", content: "Siap! Task ini cocok banget dengan skill codebase-review." },
        { role: "user", content: CONTINUE_STEER },
      ]);
    });

    it("noop: interrupted with no body and no resumeMessages → same as buildAgentContext", () => {
      const conversation = {
        messages: [
          { role: "user", content: "Hello" },
          { role: "assistant", content: "", status: "interrupted" },
        ],
      };
      const ctx = buildContinueContext(conversation);
      expect(ctx).toEqual([{ role: "user", content: "Hello" }]);
    });

    it("normal buildAgentContext still excludes interrupted", () => {
      const conversation = {
        messages: [
          { role: "user", content: "Hello" },
          { role: "assistant", content: "cut off", status: "interrupted" },
        ],
      };
      expect(buildAgentContext(conversation)).toEqual([{ role: "user", content: "Hello" }]);
    });
  });
});

describe("classifyTurnError (ticket #45)", () => {
  it("classifies 429 rate limit as retryable with a backoff hint", () => {
    const r = classifyTurnError({
      code: "AGENT_PROVIDER_FAILED",
      message: "AI provider request failed: Provider returned HTTP 429: rate limited",
      details: { cause: "Provider returned HTTP 429: rate limited" },
    });
    expect(r.category).toBe("rate_limited");
    expect(r.retryable).toBe(true);
    expect(r.label).toBe("Retry");
  });

  it("classifies 401 as auth, not retryable", () => {
    const r = classifyTurnError({
      code: "AGENT_PROVIDER_FAILED",
      message: "AI provider request failed: Provider returned HTTP 401: invalid key",
      details: { cause: "Provider returned HTTP 401: invalid key" },
    });
    expect(r.category).toBe("auth");
    expect(r.retryable).toBe(false);
  });

  it("classifies 5xx as retryable server error", () => {
    const r = classifyTurnError({
      code: "AGENT_PROVIDER_FAILED",
      message: "AI provider request failed: Provider returned HTTP 503: overloaded",
      details: { cause: "Provider returned HTTP 503: overloaded" },
    });
    expect(r.category).toBe("server_error");
    expect(r.retryable).toBe(true);
  });

  it("maps AGENT_TURN_CANCELLED to cancelled", () => {
    const r = classifyTurnError({ code: "AGENT_TURN_CANCELLED" });
    expect(r.category).toBe("cancelled");
    expect(r.label).toBe("Continue");
  });

  it("detects superseded turns and disables retry", () => {
    const r = classifyTurnError({ code: "AGENT_TURN_SUPERSEDED", message: "superseded by a newer turn" });
    expect(r.category).toBe("superseded");
    expect(r.retryable).toBe(false);
    expect(r.label).toBe("");
  });

  it("falls back to unknown for null / empty", () => {
    expect(classifyTurnError(null).category).toBe("unknown");
    expect(classifyTurnError({}).category).not.toBe("rate_limited");
  });
});

describe("mergeConversationMessages (ticket #47 race guard)", () => {
  it("merges positioned snapshots deterministically regardless of arrival order", () => {
    const first = { id: "msg-1", position: 1, revision: 1, role: "user", content: "first" };
    const second = { id: "msg-2", position: 2, revision: 1, role: "assistant", content: "second" };

    expect(mergeConversationMessages([second], [first]).map((message) => message.id)).toEqual(["msg-1", "msg-2"]);
    expect(mergeConversationMessages([first], [second]).map((message) => message.id)).toEqual(["msg-1", "msg-2"]);
  });

  it("keeps the highest revision for one message identity at its original position", () => {
    const current = [{
      id: "msg-assistant",
      position: 2,
      revision: 3,
      role: "assistant",
      content: "complete answer",
      reasoning: "kept reasoning",
    }];
    const stale = [{
      id: "msg-assistant",
      position: 2,
      revision: 1,
      role: "assistant",
      content: "",
    }];

    expect(mergeConversationMessages(current, stale)).toEqual(current);
  });

  it("does not drop a live/seen message when an incoming append snapshot is stale", () => {
    const current = [
      { role: "user", content: "1", createdAt: "t1" },
      { role: "assistant", content: "a1", traceId: "tr-a", createdAt: "t2" },
      { role: "user", content: "2", createdAt: "t3" },
      { role: "assistant", content: "a2", traceId: "tr-b", createdAt: "t4" },
    ];
    // Incoming snapshot from a stale append: it lacks assistant a2 (turn 2's
    // seal had not committed when turn 3's append read the store).
    const incoming = [
      { role: "user", content: "1", createdAt: "t1" },
      { role: "assistant", content: "a1", traceId: "tr-a", createdAt: "t2" },
      { role: "user", content: "2", createdAt: "t3" },
      { role: "user", content: "3", createdAt: "t5" },
    ];
    const merged = mergeConversationMessages(current, incoming);
    const contents = merged.map((m) => `${m.role}:${m.content}`);
    // The stale incoming usermsg 3 is present, and assistant a2 is NOT lost.
    expect(contents).toContain("user:3");
    expect(contents).toContain("assistant:a2");
  });

  it("merges by identity (role+createdAt) without duplicating shared messages", () => {
    const current = [
      { role: "user", content: "1", createdAt: "t1" },
      { role: "assistant", content: "a1", createdAt: "t2" },
    ];
    const incoming = [
      { role: "user", content: "1", createdAt: "t1" },
      { role: "assistant", content: "a1", createdAt: "t2" },
      { role: "user", content: "2", createdAt: "t3" },
    ];
    const merged = mergeConversationMessages(current, incoming);
    expect(merged.length).toBe(3);
    expect(merged.map((m) => m.content)).toEqual(["1", "a1", "2"]);
  });
});
