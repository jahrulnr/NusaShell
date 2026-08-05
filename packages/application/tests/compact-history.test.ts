import { describe, expect, it } from "vitest";
import type { AgentMessage } from "../src/agent/ports/agent-provider.port.js";
import {
  SUMMARY_PREFIX,
  COMPACT_USER_MESSAGE_MAX_TOKENS,
  MIN_SUMMARY_CHARS,
  isSummaryMessage,
  userMessageText,
  collectUserMessages,
  buildCompactedHistory,
  splitLeadingSystemInjects,
  approxTokenCount,
} from "../src/agent/services/compact-history.js";

const user = (content: string): AgentMessage => ({ role: "user", content });
const system = (content: string): AgentMessage => ({ role: "system", content });
const assistant = (content: string): AgentMessage => ({ role: "assistant", content });
const tool = (name: string, content: string): AgentMessage => ({ role: "tool", toolCallId: "1", name, content });

describe("isSummaryMessage", () => {
  it("detects the Codex SUMMARY_PREFIX marker", () => {
    expect(isSummaryMessage(`${SUMMARY_PREFIX}\nSome body.`)).toBe(true);
  });

  it("detects the legacy Conversation summary: marker", () => {
    expect(isSummaryMessage("Conversation summary:\nOld checkpoint.")).toBe(true);
  });

  it("returns false for ordinary user text", () => {
    expect(isSummaryMessage("Please fix the bug in curl.go")).toBe(false);
  });
});

describe("userMessageText", () => {
  it("extracts string content", () => {
    expect(userMessageText(user("hello"))).toBe("hello");
  });

  it("extracts text from AgentContentPart[] content", () => {
    expect(userMessageText({ role: "user", content: [{ type: "text", text: "part text" }] })).toBe("part text");
  });

  it("returns undefined for non-user messages", () => {
    expect(userMessageText(system("sys"))).toBe(undefined);
    expect(userMessageText(assistant("as"))).toBe(undefined);
  });

  it("returns undefined for image-only user messages", () => {
    expect(userMessageText({ role: "user", content: [{ type: "image", dataUrl: "data:..." }] })).toBe(undefined);
  });
});

describe("collectUserMessages", () => {
  it("collects only user text, skipping assistants/tools/systems", () => {
    const messages: AgentMessage[] = [
      system("sys"),
      user("first goal"),
      assistant("ok"),
      tool("notes.create", "{}"),
      user("second request"),
    ];
    expect(collectUserMessages(messages)).toEqual(["first goal", "second request"]);
  });

  it("skips prior summary-shaped user messages", () => {
    const messages: AgentMessage[] = [
      user("first goal"),
      user(`${SUMMARY_PREFIX}\nold checkpoint body`),
      user("follow-up question"),
    ];
    expect(collectUserMessages(messages)).toEqual(["first goal", "follow-up question"]);
  });

  it("skips legacy Conversation summary: user messages", () => {
    const messages: AgentMessage[] = [
      user("first goal"),
      user("Conversation summary:\nlegacy body"),
      user("follow-up question"),
    ];
    expect(collectUserMessages(messages)).toEqual(["first goal", "follow-up question"]);
  });

  it("returns empty for an empty history", () => {
    expect(collectUserMessages([])).toEqual([]);
  });
});

describe("buildCompactedHistory", () => {
  it("packs user messages newest-first up to the token budget, then appends the summary user", () => {
    const users = ["old question", "mid question", "recent question"];
    const summaryText = `${SUMMARY_PREFIX}\nA good summary body.`;
    const history = buildCompactedHistory(users, summaryText, COMPACT_USER_MESSAGE_MAX_TOKENS);
    // All three fit (each ~3 tokens), in chronological order.
    expect(history.map((m) => (m.role === "user" ? m.content : ""))).toEqual([
      "old question",
      "mid question",
      "recent question",
      summaryText,
    ]);
  });

  it("drops oldest user messages when the budget is small", () => {
    const users = ["old question", "mid question", "recent question"];
    const summaryText = `${SUMMARY_PREFIX}\nSummary.`;
    // Budget only fits the newest user — older ones are dropped entirely.
    const history = buildCompactedHistory(users, summaryText, approxTokenCount("recent question"));
    const contents = history.map((m) => (m.role === "user" ? m.content : ""));
    expect(contents).toEqual(["recent question", summaryText]);
  });

  it("truncates the boundary user message when it exceeds the remaining budget", () => {
    const longText = "x".repeat(1000);
    const users = [longText, "short"];
    const summaryText = `${SUMMARY_PREFIX}\nSummary.`;
    // Budget fits "short" + a truncated slice of longText.
    const budget = approxTokenCount("short") + 10;
    const history = buildCompactedHistory(users, summaryText, budget);
    const contents = history.map((m) => (m.role === "user" ? m.content : ""));
    // Newest ("short") is always kept; oldest is truncated or dropped.
    expect(contents).toContain("short");
    expect(contents[contents.length - 1]).toBe(summaryText);
  });

  it("uses (no summary available) when the summary body is empty", () => {
    const history = buildCompactedHistory(["a user msg"], "   ");
    expect(history.at(-1)).toMatchObject({ role: "user", content: "(no summary available)" });
  });

  it("returns only the summary user when there are no user messages", () => {
    const history = buildCompactedHistory([], `${SUMMARY_PREFIX}\nBody.`);
    expect(history).toHaveLength(1);
    expect(history[0]).toMatchObject({ role: "user" });
  });
});

describe("splitLeadingSystemInjects", () => {
  it("separates leading system messages from the rest", () => {
    const messages: AgentMessage[] = [
      system("system.md"),
      system("mcp-tools.md"),
      user("hi"),
      assistant("hello"),
    ];
    const { leadingSystem, rest } = splitLeadingSystemInjects(messages);
    expect(leadingSystem).toHaveLength(2);
    expect(rest).toEqual([user("hi"), assistant("hello")]);
  });

  it("returns empty leadingSystem when the first message is not system", () => {
    const messages: AgentMessage[] = [user("hi"), system("late")];
    const { leadingSystem, rest } = splitLeadingSystemInjects(messages);
    expect(leadingSystem).toEqual([]);
    expect(rest).toEqual(messages);
  });
});

describe("constants", () => {
  it("exposes the Codex-aligned user pack budget", () => {
    expect(COMPACT_USER_MESSAGE_MAX_TOKENS).toBe(20_000);
  });

  it("exposes a non-empty MIN_SUMMARY_CHARS", () => {
    expect(MIN_SUMMARY_CHARS).toBeGreaterThan(0);
  });
});
