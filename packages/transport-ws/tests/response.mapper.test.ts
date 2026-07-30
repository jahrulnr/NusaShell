import { describe, expect, it } from "vitest";
import { mapSuccessResponse, mapErrorResponse, mapResponse } from "../src/mapping/response.mapper.js";
import { ProtocolError } from "../src/protocol/websocket-error.js";

describe("response mapper", () => {
  it("maps success response", () => {
    const response = mapSuccessResponse("req_001", { pluginId: "nusashell.notes", state: "running" });
    expect(response.kind).toBe("response");
    expect(response.id).toBe("req_001");
    expect(response.ok).toBe(true);
  });

  it("maps error response", () => {
    const error = new ProtocolError("PLUGIN_NOT_FOUND", "Plugin not found", "req_001");
    const response = mapErrorResponse("req_001", error);
    expect(response.kind).toBe("response");
    expect(response.id).toBe("req_001");
    expect(response.ok).toBe(false);
    expect(response.error.code).toBe("PLUGIN_NOT_FOUND");
  });

  it("maps error response with details", () => {
    const error = new ProtocolError("INTERNAL_ERROR", "fail", "req_001", { detail: "x" });
    const response = mapErrorResponse("req_001", error);
    expect(response.error.details).toEqual({ detail: "x" });
  });

  it("mapResponse returns success when no error", () => {
    const response = mapResponse("req_001", { data: "test" }, null);
    expect(response.ok).toBe(true);
  });

  it("mapResponse returns error when error provided", () => {
    const error = new ProtocolError("INTERNAL_ERROR", "fail", "req_001");
    const response = mapResponse("req_001", null, error);
    expect(response.ok).toBe(false);
  });
});
