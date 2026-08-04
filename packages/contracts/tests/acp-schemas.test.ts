import { describe, expect, it } from "vitest";
import {
  RequestSchema,
  AcpRunRequestSchema,
  AcpCancelRequestSchema,
  AcpPermissionAnswerRequestSchema,
  AcpAskAnswerRequestSchema,
  AcpSessionInfoRequestSchema,
  EventSchema,
  AcpTextDeltaEventSchema,
  AcpThoughtDeltaEventSchema,
  AcpToolCallEventSchema,
  AcpToolCallUpdateEventSchema,
  AcpPlanEventSchema,
  AcpPermissionRequestEventSchema,
  AcpAskRequestEventSchema,
  AcpTurnEndEventSchema,
  AcpSessionStateEventSchema,
} from "../src/index.js";

const provider = {
  providerId: "cursor",
  command: "agent",
  args: ["acp"],
  authMethodId: "cursor_login",
};

describe("ACP request schemas", () => {
  it("parses acp.run with text prompt", () => {
    const result = AcpRunRequestSchema.safeParse({
      kind: "request",
      id: "req_acp_001",
      method: "acp.run",
      payload: {
        traceId: "trace-1",
        conversationId: "conv-1",
        workspace: "/home/user/project",
        provider,
        prompt: [{ type: "text", text: "List files" }],
      },
    });
    expect(result.success).toBe(true);
  });

  it("parses acp.run with image prompt", () => {
    const result = AcpRunRequestSchema.safeParse({
      kind: "request",
      id: "req_acp_002",
      method: "acp.run",
      payload: {
        traceId: "trace-2",
        conversationId: "conv-2",
        provider,
        prompt: [{ type: "image", data: "base64", mimeType: "image/png" }],
      },
    });
    expect(result.success).toBe(true);
  });

  it("rejects acp.run without provider command", () => {
    const result = AcpRunRequestSchema.safeParse({
      kind: "request",
      id: "req_acp_003",
      method: "acp.run",
      payload: {
        traceId: "trace-3",
        conversationId: "conv-3",
        provider: { providerId: "cursor", command: "", args: [] },
        prompt: [{ type: "text", text: "Hi" }],
      },
    });
    expect(result.success).toBe(false);
  });

  it("parses acp.cancel", () => {
    const result = AcpCancelRequestSchema.safeParse({
      kind: "request",
      id: "req_acp_004",
      method: "acp.cancel",
      payload: { traceId: "trace-4", conversationId: "conv-4" },
    });
    expect(result.success).toBe(true);
  });

  it("parses acp.permission_answer", () => {
    const result = AcpPermissionAnswerRequestSchema.safeParse({
      kind: "request",
      id: "req_acp_005",
      method: "acp.permission_answer",
      payload: { traceId: "trace-5", conversationId: "conv-5", requestId: "perm-1", optionId: "allow" },
    });
    expect(result.success).toBe(true);
  });

  it("parses acp.ask_answer", () => {
    const result = AcpAskAnswerRequestSchema.safeParse({
      kind: "request",
      id: "req_acp_006",
      method: "acp.ask_answer",
      payload: { traceId: "trace-6", conversationId: "conv-6", requestId: "ask-1", text: "Use npm" },
    });
    expect(result.success).toBe(true);
  });

  it("parses acp.session_info", () => {
    const result = AcpSessionInfoRequestSchema.safeParse({
      kind: "request",
      id: "req_acp_007",
      method: "acp.session_info",
      payload: { conversationId: "conv-7" },
    });
    expect(result.success).toBe(true);
  });

  it("includes acp methods in RequestSchema", () => {
    const result = RequestSchema.safeParse({
      kind: "request",
      id: "req_acp_008",
      method: "acp.run",
      payload: {
        traceId: "trace-8",
        conversationId: "conv-8",
        provider,
        prompt: [{ type: "text", text: "Hello" }],
      },
    });
    expect(result.success).toBe(true);
    if (result.success) {
      expect(result.data.method).toBe("acp.run");
    }
  });
});

describe("ACP event schemas", () => {
  it("parses acp.text_delta", () => {
    const result = AcpTextDeltaEventSchema.safeParse({
      kind: "event",
      event: "acp.text_delta",
      sequence: 1,
      payload: { traceId: "trace-1", delta: "Hello", timestamp: "2026-01-01T00:00:00Z" },
    });
    expect(result.success).toBe(true);
  });

  it("parses acp.thought_delta", () => {
    const result = AcpThoughtDeltaEventSchema.safeParse({
      kind: "event",
      event: "acp.thought_delta",
      sequence: 2,
      payload: { traceId: "trace-1", delta: "Thinking", timestamp: "2026-01-01T00:00:00Z" },
    });
    expect(result.success).toBe(true);
  });

  it("parses acp.tool_call", () => {
    const result = AcpToolCallEventSchema.safeParse({
      kind: "event",
      event: "acp.tool_call",
      sequence: 3,
      payload: {
        traceId: "trace-1",
        call: { id: "tc-1", title: "Run npm test", kind: "terminal", status: "pending", summary: "" },
        timestamp: "2026-01-01T00:00:00Z",
      },
    });
    expect(result.success).toBe(true);
  });

  it("parses acp.tool_call_update", () => {
    const result = AcpToolCallUpdateEventSchema.safeParse({
      kind: "event",
      event: "acp.tool_call_update",
      sequence: 4,
      payload: { traceId: "trace-1", callId: "tc-1", status: "ok", timestamp: "2026-01-01T00:00:00Z" },
    });
    expect(result.success).toBe(true);
  });

  it("parses acp.plan", () => {
    const result = AcpPlanEventSchema.safeParse({
      kind: "event",
      event: "acp.plan",
      sequence: 5,
      payload: {
        traceId: "trace-1",
        steps: [
          { id: "s1", text: "Read files", done: false },
          { id: "s2", text: "Run tests", done: true },
        ],
        timestamp: "2026-01-01T00:00:00Z",
      },
    });
    expect(result.success).toBe(true);
  });

  it("parses acp.permission_request", () => {
    const result = AcpPermissionRequestEventSchema.safeParse({
      kind: "event",
      event: "acp.permission_request",
      sequence: 6,
      payload: {
        traceId: "trace-1",
        conversationId: "conv-1",
        requestId: "perm-1",
        toolTitle: "Run npm test",
        detail: "Allow running tests?",
        options: [
          { optionId: "allow_once", name: "Allow once", kind: "allow_once" },
          { optionId: "deny", name: "Deny", kind: "deny" },
        ],
        timestamp: "2026-01-01T00:00:00Z",
      },
    });
    expect(result.success).toBe(true);
  });

  it("rejects acp.permission_request without conversationId", () => {
    const result = AcpPermissionRequestEventSchema.safeParse({
      kind: "event",
      event: "acp.permission_request",
      sequence: 6,
      payload: {
        traceId: "trace-1",
        requestId: "perm-1",
        toolTitle: "Run npm test",
        options: [
          { optionId: "allow_once", name: "Allow once", kind: "allow_once" },
        ],
        timestamp: "2026-01-01T00:00:00Z",
      },
    });
    expect(result.success).toBe(false);
  });

  it("parses acp.ask_request", () => {
    const result = AcpAskRequestEventSchema.safeParse({
      kind: "event",
      event: "acp.ask_request",
      sequence: 7,
      payload: {
        traceId: "trace-1",
        conversationId: "conv-1",
        requestId: "ask-1",
        question: "Which package manager?",
        options: [{ optionId: "npm", name: "npm" }],
        multiSelect: false,
        allowFreeText: true,
        timestamp: "2026-01-01T00:00:00Z",
      },
    });
    expect(result.success).toBe(true);
  });

  it("rejects acp.ask_request without conversationId", () => {
    const result = AcpAskRequestEventSchema.safeParse({
      kind: "event",
      event: "acp.ask_request",
      sequence: 7,
      payload: {
        traceId: "trace-1",
        requestId: "ask-1",
        question: "Which package manager?",
        timestamp: "2026-01-01T00:00:00Z",
      },
    });
    expect(result.success).toBe(false);
  });

  it("parses acp.turn_end", () => {
    const result = AcpTurnEndEventSchema.safeParse({
      kind: "event",
      event: "acp.turn_end",
      sequence: 8,
      payload: { traceId: "trace-1", ok: true, timestamp: "2026-01-01T00:00:00Z" },
    });
    expect(result.success).toBe(true);
  });

  it("parses acp.session_state", () => {
    const result = AcpSessionStateEventSchema.safeParse({
      kind: "event",
      event: "acp.session_state",
      sequence: 9,
      payload: { traceId: "trace-1", conversationId: "conv-1", state: "running", timestamp: "2026-01-01T00:00:00Z" },
    });
    expect(result.success).toBe(true);
  });

  it("includes acp events in EventSchema", () => {
    const result = EventSchema.safeParse({
      kind: "event",
      event: "acp.text_delta",
      sequence: 10,
      payload: { traceId: "trace-10", delta: "Hi", timestamp: "2026-01-01T00:00:00Z" },
    });
    expect(result.success).toBe(true);
    if (result.success) {
      expect(result.data.event).toBe("acp.text_delta");
    }
  });
});
