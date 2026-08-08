import { mkdir, mkdtemp, rm } from "node:fs/promises";
import { join } from "node:path";
import { tmpdir } from "node:os";
import { describe, expect, it, afterEach } from "vitest";
import { AskQuestionService } from "../src/agent/services/ask-question-service.js";
import { execMcpRegister, execMcpUnregister } from "../src/agent/services/mcp-plugin-tool-handlers.js";

const roots: string[] = [];

afterEach(async () => {
  await Promise.all(roots.splice(0).map((root) => rm(root, { recursive: true, force: true })));
});

function deps(root: string, overrides: Record<string, unknown> = {}) {
  const plugin = {
    id: { value: "example.plugin" },
    installPath: join(root, "example.plugin"),
  };
  return {
    installer: {
      installFromPath: async (path: string) => ({ installPath: path, pluginId: "example.plugin", version: "0.1.0" }),
      uninstall: async () => {},
    },
    repository: {
      findById: async () => plugin,
      remove: async () => {},
    },
    runtimeManager: { removePlugin: async () => {} },
    syncPlugins: async () => {},
    userPluginsRoot: root,
    askQuestions: new AskQuestionService(),
    ...overrides,
  } as never;
}

async function confirm(askQuestions: AskQuestionService, operation: Promise<unknown>, turnId: string, callId: string): Promise<unknown> {
  while (!askQuestions.hasPending(turnId, callId)) await new Promise((resolve) => setTimeout(resolve, 0));
  askQuestions.answer(turnId, callId, { via: "option", optionIds: ["confirm"] });
  return operation;
}

describe("MCP plugin registration handlers", () => {
  it("rejects registration outside an interactive turn", async () => {
    const root = await mkdtemp(join(tmpdir(), "nusashell-mcp-register-"));
    roots.push(root);
    await expect(execMcpRegister(deps(root), { folder: "example.plugin" }, "turn", "call", false)).rejects.toThrow("interactive");
  });

  it("rejects path traversal and non-direct child folders", async () => {
    const root = await mkdtemp(join(tmpdir(), "nusashell-mcp-register-"));
    roots.push(root);
    await expect(execMcpRegister(deps(root), { path: join(root, "..") }, "turn", "call", true)).rejects.toThrow("exactly one folder");
  });

  it("requires confirmation before registering a valid user folder", async () => {
    const root = await mkdtemp(join(tmpdir(), "nusashell-mcp-register-"));
    roots.push(root);
    await mkdir(join(root, "example.plugin"));
    const askQuestions = new AskQuestionService();
    const registrationDeps = deps(root, { askQuestions });
    const operation = execMcpRegister(registrationDeps, { folder: "example.plugin" }, "turn", "call", true);
    const result = await confirm(askQuestions, operation, "turn", "call");
    expect(result).toMatchObject({ ok: true, data: { pluginId: "example.plugin" } });
  });

  // Decision #49-B: bundled plugins are fully writable (seeded into the user
  // root). A previously-bundled plugin whose install path lives under the user
  // root may now be unregistered like any user plugin.
  it("allows unregistering a bundled-seeded plugin when it lives in the user root", async () => {
    const root = await mkdtemp(join(tmpdir(), "nusashell-mcp-register-"));
    const bundled = await mkdtemp(join(tmpdir(), "nusashell-mcp-bundled-"));
    roots.push(root, bundled);
    await mkdir(join(root, "example.plugin"));
    const registrationDeps = deps(root, {
      bundledPluginsRoot: bundled,
      installer: { uninstall: async () => {} },
      repository: {
        findById: async () => ({ id: { value: "example.plugin" }, installPath: join(root, "example.plugin") }),
        remove: async () => {},
      },
    });
    const askQuestions = new AskQuestionService();
    Object.assign(registrationDeps, { askQuestions });
    const result = await confirm(askQuestions, execMcpUnregister(registrationDeps, { pluginId: "example.plugin" }, "turn", "call", true), "turn", "call");
    expect(result).toMatchObject({ ok: true, data: { pluginId: "example.plugin" } });
  });

  it("blocks unregistering a plugin whose install path is outside the user root", async () => {
    const root = await mkdtemp(join(tmpdir(), "nusashell-mcp-register-"));
    const bundled = await mkdtemp(join(tmpdir(), "nusashell-mcp-bundled-"));
    roots.push(root, bundled);
    const registrationDeps = deps(root, {
      repository: { findById: async () => ({ id: { value: "example.plugin" }, installPath: join(bundled, "example.plugin") }) },
    });
    await expect(execMcpUnregister(registrationDeps, { pluginId: "example.plugin" }, "turn", "call", true)).rejects.toThrow("user plugins root");
  });
});
