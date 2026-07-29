import { describe, expect, it } from "vitest";
import { McpAgentToolGateway } from "../src/index.js";
import type {
  DocContent,
  DocsIndexPort,
  DocsHit,
  DocSummary,
  SkillRegistryPort,
} from "../src/index.js";

const fakeRuntime = {
  listPlugins: async () => [],
  listTools: async () => [{ name: "createNote", description: "Create a note", inputSchema: { type: "object", properties: { text: { type: "string" } } } }],
  startPlugin: async () => ({ pluginId: "com.example.notes", state: "running" }),
  stopPlugin: async () => ({ pluginId: "com.example.notes", state: "idle" }),
  callTool: async () => ({ ok: true }),
  listPrompts: async () => [{ name: "daily", description: "Daily prompt" }],
  getPrompt: async () => ({ messages: [{ role: "user", content: { type: "text", text: "Review today" } }] }),
  listResources: async () => [{ uri: "notes://daily", name: "Daily notes", mimeType: "text/markdown" }],
  listResourceTemplates: async () => [{ uriTemplate: "notes://{date}", name: "Notes by date", mimeType: "text/markdown" }],
  complete: async () => ({ values: ["2026-07-29"], total: 1, hasMore: false }),
  readResource: async () => ({ contents: [{ uri: "notes://daily", mimeType: "text/markdown", text: "# Today" }] }),
};

describe("McpAgentToolGateway", () => {
  it("exposes bounded MCP discovery and one prompt/resource context meta-tool", async () => {
    const gateway = new McpAgentToolGateway(fakeRuntime as never);
    gateway.beginTurn("turn-1");

    expect((await gateway.listTools([], "turn-1")).map((tool) => tool.name)).toEqual([
      "mcp_list", "mcp_enable", "mcp_disable", "tool_search", "tool_list", "tool_schema", "mcp_context",
      "docs_search", "docs_list", "docs_read",
      "skill_list", "skill_search", "skill_read",
    ]);
    await expect(gateway.execute("tool_list", { pluginId: "com.example.notes" }, "call-tool-list", "turn-1")).resolves.toEqual([
      { name: "createNote", description: "Create a note" },
    ]);
    await expect(gateway.execute("mcp_context", { pluginId: "com.example.notes", action: "list_prompts", query: "daily" }, "call-prompt-list", "turn-1")).resolves.toEqual([
      { name: "daily", description: "Daily prompt" },
    ]);
    await expect(gateway.execute("mcp_context", { pluginId: "com.example.notes", action: "get_prompt", name: "daily", arguments: {} }, "call-prompt-get", "turn-1")).resolves.toEqual({
      messages: [{ role: "user", content: { type: "text", text: "Review today" } }],
    });
    await expect(gateway.execute("mcp_context", { pluginId: "com.example.notes", action: "search_resources", query: "daily" }, "call-resource-search", "turn-1")).resolves.toEqual([
      { uri: "notes://daily", name: "Daily notes", mimeType: "text/markdown" },
    ]);
    await expect(gateway.execute("mcp_context", { pluginId: "com.example.notes", action: "list_resource_templates", query: "date" }, "call-resource-template", "turn-1")).resolves.toEqual([
      { uriTemplate: "notes://{date}", name: "Notes by date", mimeType: "text/markdown" },
    ]);
    await expect(gateway.execute("mcp_context", {
      pluginId: "com.example.notes",
      action: "complete",
      refType: "resource",
      uri: "notes://{date}",
      argumentName: "date",
      argumentValue: "2026-07",
      arguments: {},
    }, "call-completion", "turn-1")).resolves.toEqual({
      values: ["2026-07-29"],
      total: 1,
      hasMore: false,
    });
    await expect(gateway.execute("mcp_context", { pluginId: "com.example.notes", action: "read_resource", uri: "notes://daily" }, "call-resource-read", "turn-1")).resolves.toEqual({
      contents: [{ uri: "notes://daily", mimeType: "text/markdown", text: "# Today" }],
    });
    const grant = await gateway.execute("tool_schema", { pluginId: "com.example.notes", toolName: "createNote" }, "call-1", "turn-1") as { name: string };
    expect((await gateway.listTools([], "turn-1")).map((tool) => tool.name)).toContain(grant.name);
    expect((await gateway.listTools([], "turn-2")).map((tool) => tool.name)).not.toContain(grant.name);
    expect(await gateway.execute(grant.name, { text: "hello" }, "call-2", "turn-1")).toEqual({ ok: true });
    await expect(gateway.execute(grant.name, { text: "hello" }, "call-3", "turn-2")).rejects.toThrow("outside the MCP allowlist");
    gateway.endTurn("turn-1");
    expect((await gateway.listTools([], "turn-1")).map((tool) => tool.name)).not.toContain(grant.name);
  });

  it("exposes docs_* meta-tools and returns envelope results", async () => {
    const hits: DocsHit[] = [
      { path: "getting-started.md", title: "Getting Started", heading: "Launcher", chunkId: "launcher", excerpt: "...launcher...", score: 2 },
    ];
    const summaries: DocSummary[] = [
      { path: "getting-started.md", title: "Getting Started", headings: ["Launcher"], domain: "root" },
    ];
    const content: DocContent = {
      path: "getting-started.md",
      title: "Getting Started",
      headings: ["Launcher"],
      domain: "root",
      text: "The launcher is a grid of icons.",
      chunkId: "launcher",
      chunk: "The launcher is a grid of icons.",
    };
    const fakeDocs: DocsIndexPort = {
      usable: () => true,
      reindex: async () => {},
      search: async (_query: string, _topK: number) => hits,
      listDocs: async () => summaries,
      readDoc: async (path: string, _chunkId?: string) => (path === "getting-started.md" ? content : undefined),
    };
    const gateway = new McpAgentToolGateway(fakeRuntime as never, fakeDocs);
    gateway.beginTurn("turn-docs");

    expect((await gateway.listTools([], "turn-docs")).map((tool) => tool.name)).toContain("docs_search");

    await expect(gateway.execute("docs_search", { query: "launcher" }, "call-search", "turn-docs")).resolves.toEqual({
      ok: true,
      data: { chunks: hits },
      meta: { count: hits.length, truncated: false, index_ready: true, data_is_untrusted: true },
    });

    await expect(gateway.execute("docs_list", {}, "call-list", "turn-docs")).resolves.toEqual({
      ok: true,
      data: { documents: summaries },
      meta: { count: summaries.length, truncated: false, index_ready: true, data_is_untrusted: true },
    });

    await expect(gateway.execute("docs_read", { path: "getting-started.md" }, "call-read", "turn-docs")).resolves.toEqual({
      ok: true,
      data: {
        path: content.path,
        title: content.title,
        headings: content.headings,
        domain: content.domain,
        text: content.text,
        chunk_id: content.chunkId,
        has_more: false,
        next_offset: undefined,
      },
      meta: { index_ready: true, data_is_untrusted: true },
    });

    await expect(gateway.execute("docs_read", { path: "missing.md" }, "call-read-missing", "turn-docs")).resolves.toEqual({
      ok: false,
      error: { code: "not_found", message: "Document not found in docs corpus" },
      meta: { index_ready: true, data_is_untrusted: true },
    });
  });

  it("returns docs_not_configured when no docs index is wired", async () => {
    const gateway = new McpAgentToolGateway(fakeRuntime as never);
    gateway.beginTurn("turn-1");

    await expect(gateway.execute("docs_search", { query: "launcher" }, "call-search", "turn-1")).resolves.toEqual({
      ok: false,
      error: { code: "docs_not_configured", message: "Documentation index is not configured" },
      meta: { index_ready: false },
    });
  });

  it("exposes bounded read-only skill tools without exposing skill mutations", async () => {
    const summary = {
      id: "code-review",
      name: "code-review",
      description: "Review code changes carefully.",
      fileCount: 2,
      updatedAt: "2026-07-30T00:00:00.000Z",
    };
    const fakeSkills: SkillRegistryPort = {
      list: async () => [summary],
      search: async () => [summary],
      get: async () => ({ ...summary, files: [] }),
      read: async (skillId, path = "SKILL.md") => ({
        skillId,
        path,
        content: "# Code Review",
        sizeBytes: 13,
        editable: true,
        truncated: false,
      }),
      installFromArchive: async () => ({ ...summary, files: [] }),
      write: async () => ({
        skillId: summary.id,
        path: "SKILL.md",
        content: "",
        sizeBytes: 0,
        editable: true,
        truncated: false,
      }),
      delete: async () => {},
    };
    const gateway = new McpAgentToolGateway(fakeRuntime as never, undefined, fakeSkills);
    gateway.beginTurn("turn-skills");

    const toolNames = (await gateway.listTools([], "turn-skills")).map((tool) => tool.name);
    expect(toolNames).toEqual(expect.arrayContaining(["skill_list", "skill_search", "skill_read"]));
    expect(toolNames).not.toEqual(expect.arrayContaining(["skill_install", "skill_edit", "skill_delete", "skill_exec"]));

    await expect(gateway.execute("skill_list", {}, "call-list", "turn-skills")).resolves.toEqual({
      ok: true,
      data: { skills: [summary] },
      meta: { count: 1, truncated: false, data_is_untrusted: true },
    });
    await expect(gateway.execute("skill_search", { query: "review", limit: 5 }, "call-search", "turn-skills")).resolves.toEqual({
      ok: true,
      data: { skills: [summary] },
      meta: { count: 1, truncated: false, data_is_untrusted: true },
    });
    await expect(gateway.execute("skill_read", {
      skill_id: "code-review",
      path: "SKILL.md",
    }, "call-read", "turn-skills")).resolves.toEqual({
      ok: true,
      data: {
        skillId: "code-review",
        path: "SKILL.md",
        content: "# Code Review",
        sizeBytes: 13,
        editable: true,
        truncated: false,
      },
      meta: { data_is_untrusted: true },
    });
  });

  it("returns skills_not_configured when no skill registry is wired", async () => {
    const gateway = new McpAgentToolGateway(fakeRuntime as never);
    gateway.beginTurn("turn-skills");

    await expect(gateway.execute("skill_list", {}, "call-list", "turn-skills")).resolves.toEqual({
      ok: false,
      error: { code: "skills_not_configured", message: "Skill registry is not configured" },
      meta: { data_is_untrusted: true },
    });
  });
});
