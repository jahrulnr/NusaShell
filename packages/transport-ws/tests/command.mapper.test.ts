import { describe, expect, it } from "vitest";
import { mapToCommand } from "../src/mapping/command.mapper.js";
import { mapToQuery } from "../src/mapping/query.mapper.js";
import type { ParsedRequest } from "@nusashell/contracts";

function makeRequest(method: string, payload: Record<string, unknown>): ParsedRequest {
  return {
    kind: "request",
    id: "req_001",
    method: method as ParsedRequest["method"],
    payload,
  } as ParsedRequest;
}

describe("mapToCommand", () => {
  it("maps plugin.start to StartPluginCommand", () => {
    const result = mapToCommand(makeRequest("plugin.start", { pluginId: "com.example.notes" }));
    expect(result.kind).toBe("command");
    if (result.kind === "command") {
      expect(result.command.kind).toBe("start-plugin");
    }
  });

  it("maps plugin.stop to StopPluginCommand", () => {
    const result = mapToCommand(makeRequest("plugin.stop", { pluginId: "com.example.notes" }));
    expect(result.kind).toBe("command");
    if (result.kind === "command") {
      expect(result.command.kind).toBe("stop-plugin");
    }
  });

  it("maps tool.call to CallToolCommand", () => {
    const result = mapToCommand(
      makeRequest("tool.call", {
        pluginId: "com.example.notes",
        requestId: "req-uuid-001",
        toolName: "echo",
        args: { message: "hello" },
      }),
    );
    expect(result.kind).toBe("command");
    if (result.kind === "command") {
      expect(result.command.kind).toBe("call-tool");
    }
  });

  it("returns query for plugin.list", () => {
    const result = mapToCommand(makeRequest("plugin.list", {}));
    expect(result.kind).toBe("query");
  });
});

describe("mapToQuery", () => {
  it("maps plugin.list to ListPluginsQuery", () => {
    const query = mapToQuery(makeRequest("plugin.list", {}));
    expect(query).not.toBeNull();
    expect(query!.kind).toBe("list-plugins");
  });

  it("returns null for non-query methods", () => {
    const query = mapToQuery(makeRequest("plugin.start", { pluginId: "x" }));
    expect(query).toBeNull();
  });
});
