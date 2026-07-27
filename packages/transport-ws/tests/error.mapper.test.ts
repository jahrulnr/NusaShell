import { describe, expect, it } from "vitest";
import { mapApplicationError } from "../src/mapping/error.mapper.js";
import { ApplicationError } from "@nusashell/application";

describe("mapApplicationError", () => {
  it("maps PLUGIN_NOT_FOUND", () => {
    const error = new ApplicationError("PLUGIN_NOT_FOUND", "Plugin not found");
    const mapped = mapApplicationError(error, "req_001");
    expect(mapped.code).toBe("PLUGIN_NOT_FOUND");
    expect(mapped.message).toBe("Plugin not found");
    expect(mapped.requestId).toBe("req_001");
  });

  it("maps PLUGIN_CRASHED with details", () => {
    const error = new ApplicationError("PLUGIN_CRASHED", "Process exited", { exitCode: 1 });
    const mapped = mapApplicationError(error, "req_002");
    expect(mapped.code).toBe("PLUGIN_CRASHED");
    expect(mapped.details).toEqual({ exitCode: 1 });
  });

  it("maps all known error codes", () => {
    const codes = [
      "PLUGIN_NOT_FOUND",
      "PLUGIN_DISABLED",
      "INVALID_RUNTIME_TRANSITION",
      "PLUGIN_START_FAILED",
      "PLUGIN_STOP_FAILED",
      "PLUGIN_CRASHED",
      "TOOL_NOT_FOUND",
      "TOOL_CALL_TIMEOUT",
      "TOOL_CALL_CANCELLED",
      "MCP_CONNECTION_FAILED",
      "OPERATION_CONFLICT",
      "OPERATION_TIMEOUT",
      "INTERNAL_ERROR",
    ] as const;

    for (const code of codes) {
      const error = new ApplicationError(code, `test ${code}`);
      const mapped = mapApplicationError(error);
      expect(mapped.code).toBe(code);
    }
  });
});
