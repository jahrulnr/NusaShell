import { describe, expect, it } from "vitest";
import { validateIncomingMessage } from "../src/validation/incoming-message.validator.js";

describe("validateIncomingMessage", () => {
  it("parses a valid plugin.start request", () => {
    const result = validateIncomingMessage({
      kind: "request",
      id: "req_001",
      method: "plugin.start",
      payload: { pluginId: "com.example.notes" },
    });

    expect(result.success).toBe(true);
    if (result.success) {
      expect(result.request.method).toBe("plugin.start");
      expect(result.request.id).toBe("req_001");
    }
  });

  it("parses a valid JSON string", () => {
    const result = validateIncomingMessage(
      JSON.stringify({
        kind: "request",
        id: "req_002",
        method: "plugin.list",
        payload: {},
      }),
    );

    expect(result.success).toBe(true);
  });

  it("rejects malformed JSON string", () => {
    const result = validateIncomingMessage("{not valid json");

    expect(result.success).toBe(false);
    if (!result.success) {
      expect(result.error.code).toBe("INVALID_REQUEST");
    }
  });

  it("rejects missing method", () => {
    const result = validateIncomingMessage({
      kind: "request",
      id: "req_003",
      payload: {},
    });

    expect(result.success).toBe(false);
  });

  it("rejects unknown method", () => {
    const result = validateIncomingMessage({
      kind: "request",
      id: "req_004",
      method: "plugin.unknown",
      payload: {},
    });

    expect(result.success).toBe(false);
    if (!result.success) {
      expect(result.error.code).toBe("INVALID_REQUEST");
    }
  });

  it("preserves request id in validation error", () => {
    const result = validateIncomingMessage({
      kind: "request",
      id: "req_005",
      method: "plugin.start",
      payload: {},
    });

    expect(result.success).toBe(false);
    if (!result.success) {
      expect(result.error.requestId).toBe("req_005");
    }
  });
});
