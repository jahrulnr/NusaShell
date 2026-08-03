import os from "node:os";
import path from "node:path";
import { describe, expect, it, vi } from "vitest";
import { execSubagent } from "../src/agent/services/subagent-tool-handler.js";
import { resolveAgentWorkspace } from "../src/agent/services/resolve-agent-workspace.js";
import type { SubagentPort } from "../src/agent/ports/subagent-port.js";

describe("resolveAgentWorkspace", () => {
  it("prefers the explicit override", () => {
    expect(resolveAgentWorkspace("/tmp/a", "/tmp/b")).toBe(path.resolve("/tmp/a"));
  });

  it("falls back to the turn workspace", () => {
    expect(resolveAgentWorkspace(undefined, "/tmp/profile")).toBe(path.resolve("/tmp/profile"));
  });

  it("falls back to the user home directory, never process.cwd()", () => {
    expect(resolveAgentWorkspace(undefined, undefined)).toBe(os.homedir());
    expect(resolveAgentWorkspace("", "")).toBe(os.homedir());
  });
});

describe("execSubagent", () => {
  it("returns a string error and warns when tryOrder is empty", async () => {
    const warn = vi.fn();
    const port: SubagentPort = {
      resolve: async () => ({ tryOrder: [], candidates: new Map() }),
      run: async () => ({ ok: true, providerId: "cursor", summary: "unused" }),
      cancel: async () => undefined,
    };

    const result = await execSubagent(port, { prompt: "do a thing", title: "Test" }, "turn-1", "/tmp", { warn, info: vi.fn(), error: vi.fn(), debug: vi.fn() });

    expect(result).toEqual({
      ok: false,
      code: "no_acp_provider",
      error: "No ACP providers are connected. Connect one in Settings → ACP Agents.",
      workspace: path.resolve("/tmp"),
    });
    expect(warn).toHaveBeenCalled();
  });

  it("passes the turn workspace to port.run and returns it on success", async () => {
    const run = vi.fn(async (request) => {
      expect(request.workspace).toBe(path.resolve("/tmp/profile"));
      expect(request.prompt[0]?.text).toContain("Working directory (cwd): " + path.resolve("/tmp/profile"));
      return { ok: true, providerId: "cursor", summary: "done" };
    });
    const port: SubagentPort = {
      resolve: async () => ({
        tryOrder: ["cursor"],
        candidates: new Map([
          ["cursor", { providerId: "cursor", descriptor: { providerId: "cursor", command: "cursor-agent", args: [] }, preferredConfig: undefined }],
        ]),
      }),
      run,
      cancel: async () => undefined,
    };

    const result = await execSubagent(
      port,
      { prompt: "Build index.html", title: "Profile" },
      "turn-1",
      "/tmp/profile",
      { warn: vi.fn(), info: vi.fn(), error: vi.fn(), debug: vi.fn() },
    );

    expect(result).toMatchObject({
      ok: true,
      providerId: "cursor",
      workspace: path.resolve("/tmp/profile"),
      summary: "done",
    });
    expect(run).toHaveBeenCalledOnce();
  });

  it("uses homedir when neither arg nor turn workspace is set", async () => {
    const run = vi.fn(async (request) => {
      expect(request.workspace).toBe(os.homedir());
      return { ok: true, providerId: "cursor", summary: "done" };
    });
    const port: SubagentPort = {
      resolve: async () => ({
        tryOrder: ["cursor"],
        candidates: new Map([
          ["cursor", { providerId: "cursor", descriptor: { providerId: "cursor", command: "cursor-agent", args: [] }, preferredConfig: undefined }],
        ]),
      }),
      run,
      cancel: async () => undefined,
    };

    const result = await execSubagent(port, { prompt: "noop" }, "turn-1", undefined);
    expect(result).toMatchObject({ ok: true, workspace: os.homedir() });
  });
});
