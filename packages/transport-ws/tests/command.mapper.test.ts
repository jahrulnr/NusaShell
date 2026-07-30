import { describe, expect, it } from "vitest";
import { mapToCommand } from "../src/mapping/command.mapper.js";
import { mapToQuery } from "../src/mapping/query.mapper.js";
import type { ParsedRequest } from "@nusashell/contracts";
import type { InstallPluginCommand } from "@nusashell/application";
import type { UninstallPluginCommand } from "@nusashell/application";

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
    const result = mapToCommand(makeRequest("plugin.start", { pluginId: "nusashell.notes" }));
    expect(result.kind).toBe("command");
    if (result.kind === "command") {
      expect(result.command.kind).toBe("start-plugin");
    }
  });

  it("maps plugin.stop to StopPluginCommand", () => {
    const result = mapToCommand(makeRequest("plugin.stop", { pluginId: "nusashell.notes" }));
    expect(result.kind).toBe("command");
    if (result.kind === "command") {
      expect(result.command.kind).toBe("stop-plugin");
    }
  });

  it("maps tool.call to CallToolCommand", () => {
    const result = mapToCommand(
      makeRequest("tool.call", {
        pluginId: "nusashell.notes",
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

  it("maps plugin.install (url) to InstallPluginCommand", () => {
    const result = mapToCommand(
      makeRequest("plugin.install", { source: "url", path: "https://example.com/plugin.zip" }),
    );
    expect(result.kind).toBe("command");
    if (result.kind === "command") {
      expect(result.command.kind).toBe("install-plugin");
      const cmd = result.command as InstallPluginCommand;
      expect(cmd.source).toBe("url");
      expect(cmd.path).toBe("https://example.com/plugin.zip");
    }
  });

  it("maps plugin.install (local) to InstallPluginCommand", () => {
    const result = mapToCommand(
      makeRequest("plugin.install", { source: "local", path: "/home/user/plugin.zip" }),
    );
    expect(result.kind).toBe("command");
    if (result.kind === "command") {
      expect(result.command.kind).toBe("install-plugin");
      const cmd = result.command as InstallPluginCommand;
      expect(cmd.source).toBe("local");
      expect(cmd.path).toBe("/home/user/plugin.zip");
    }
  });

  it("maps plugin.uninstall to UninstallPluginCommand", () => {
    const result = mapToCommand(
      makeRequest("plugin.uninstall", { pluginId: "nusashell.notes" }),
    );
    expect(result.kind).toBe("command");
    if (result.kind === "command") {
      expect(result.command.kind).toBe("uninstall-plugin");
      const cmd = result.command as UninstallPluginCommand;
      expect(cmd.pluginId).toBe("nusashell.notes");
    }
  });

  it("returns query for plugin.list", () => {
    const result = mapToCommand(makeRequest("plugin.list", {}));
    expect(result.kind).toBe("query");
  });

  it("maps agent.cancel to the active-turn cancellation command", () => {
    const result = mapToCommand(makeRequest("agent.cancel", { traceId: "trace-1" }));
    expect(result).toEqual({
      kind: "command",
      command: { kind: "cancel-agent-turn", traceId: "trace-1" },
    });
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
