import { describe, expect, it } from "vitest";
import path from "node:path";
import { McpAgentToolGateway } from "../src/index.js";
import type {
  DocContent,
  DocsIndexPort,
  DocsHit,
  DocSummary,
  SkillRegistryPort,
  SkillProvenancePort,
  MemoryStorePort,
  MemorySnapshot,
  MemoryMutationResult,
  JobStorePort,
  Job,
  JobOutputEntry,
} from "../src/index.js";
import type { JobScheduler } from "../src/job/services/job-scheduler.js";

const fakeRuntime = {
  listPlugins: async () => [],
  listTools: async () => [{ name: "createNote", description: "Create a note", inputSchema: { type: "object", properties: { text: { type: "string" } } } }],
  startPlugin: async () => ({ pluginId: "nusashell.notes", state: "running" }),
  stopPlugin: async () => ({ pluginId: "nusashell.notes", state: "idle" }),
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
      "mcp_list", "mcp_enable", "mcp_disable", "tool_search", "tool_list", "tool_schema", "tool_schemas", "mcp_context",
      "docs_search", "docs_list", "docs_read",
      "skill_list", "skill_search", "skill_read",
      "memory", "skill_manage",
    ]);
    await expect(gateway.execute("tool_list", { pluginId: "nusashell.notes" }, "call-tool-list", "turn-1")).resolves.toEqual([
      { name: "createNote", description: "Create a note" },
    ]);
    await expect(gateway.execute("mcp_context", { pluginId: "nusashell.notes", action: "list_prompts", query: "daily" }, "call-prompt-list", "turn-1")).resolves.toEqual([
      { name: "daily", description: "Daily prompt" },
    ]);
    await expect(gateway.execute("mcp_context", { pluginId: "nusashell.notes", action: "get_prompt", name: "daily", arguments: {} }, "call-prompt-get", "turn-1")).resolves.toEqual({
      messages: [{ role: "user", content: { type: "text", text: "Review today" } }],
    });
    await expect(gateway.execute("mcp_context", { pluginId: "nusashell.notes", action: "search_resources", query: "daily" }, "call-resource-search", "turn-1")).resolves.toEqual([
      { uri: "notes://daily", name: "Daily notes", mimeType: "text/markdown" },
    ]);
    await expect(gateway.execute("mcp_context", { pluginId: "nusashell.notes", action: "list_resource_templates", query: "date" }, "call-resource-template", "turn-1")).resolves.toEqual([
      { uriTemplate: "notes://{date}", name: "Notes by date", mimeType: "text/markdown" },
    ]);
    await expect(gateway.execute("mcp_context", {
      pluginId: "nusashell.notes",
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
    await expect(gateway.execute("mcp_context", { pluginId: "nusashell.notes", action: "read_resource", uri: "notes://daily" }, "call-resource-read", "turn-1")).resolves.toEqual({
      contents: [{ uri: "notes://daily", mimeType: "text/markdown", text: "# Today" }],
    });
    const grant = await gateway.execute("tool_schema", { pluginId: "nusashell.notes", toolName: "createNote" }, "call-1", "turn-1") as { name: string };
    expect((await gateway.listTools([], "turn-1")).map((tool) => tool.name)).toContain(grant.name);
    expect((await gateway.listTools([], "turn-2")).map((tool) => tool.name)).not.toContain(grant.name);
    expect(await gateway.execute(grant.name, { text: "hello" }, "call-2", "turn-1")).toEqual({ ok: true });
    await expect(gateway.execute(grant.name, { text: "hello" }, "call-3", "turn-2")).rejects.toThrow("outside the MCP allowlist");
    gateway.endTurn("turn-1");
    expect((await gateway.listTools([], "turn-1")).map((tool) => tool.name)).not.toContain(grant.name);
  });

  it("lazily resolves an ungranted mcp_* tool when the plugin is already running", async () => {
    const callToolArgs: Array<{ toolName: string; args: Readonly<Record<string, unknown>> }> = [];
    const runtime = {
      ...fakeRuntime,
      listPlugins: async () => [
        { pluginId: "nusashell.notes", name: "Notes", state: "running", enabled: true },
      ],
      listTools: async () => [
        { name: "createNote", description: "Create a note", inputSchema: { type: "object", properties: { text: { type: "string" } } } },
      ],
      callTool: async (_pluginId: unknown, options: { toolName: string; args: Readonly<Record<string, unknown>> }) => {
        callToolArgs.push({ toolName: options.toolName, args: options.args });
        return { ok: true, created: true };
      },
    };
    const gateway = new McpAgentToolGateway(runtime as never);
    gateway.beginTurn("turn-lazy");

    const providerName = "mcp_nusashell_notes_createNote";
    expect((await gateway.listTools([], "turn-lazy")).map((tool) => tool.name)).not.toContain(providerName);
    await expect(gateway.execute(providerName, { text: "remembered" }, "call-lazy", "turn-lazy")).resolves.toEqual({
      ok: true,
      created: true,
    });
    expect(callToolArgs).toEqual([{ toolName: "createNote", args: { text: "remembered" } }]);
    // Lazy resolve auto-grants for the rest of the turn.
    expect((await gateway.listTools([], "turn-lazy")).map((tool) => tool.name)).toContain(providerName);
  });

  it("rejects an ungranted mcp_* tool when the matching plugin is not running", async () => {
    const runtime = {
      ...fakeRuntime,
      listPlugins: async () => [
        { pluginId: "nusashell.notes", name: "Notes", state: "idle", enabled: true },
      ],
      listTools: async () => [
        { name: "createNote", description: "Create a note", inputSchema: { type: "object", properties: { text: { type: "string" } } } },
      ],
    };
    const gateway = new McpAgentToolGateway(runtime as never);
    gateway.beginTurn("turn-idle");

    await expect(
      gateway.execute("mcp_nusashell_notes_createNote", { text: "nope" }, "call-idle", "turn-idle"),
    ).rejects.toThrow("outside the MCP allowlist");
  });

  it("grants multiple tools in one call via tool_schemas", async () => {
    const runtime = {
      ...fakeRuntime,
      listTools: async () => [
        { name: "createNote", description: "Create a note", inputSchema: { type: "object", properties: { text: { type: "string" } } } },
        { name: "listNotes", description: "List notes", inputSchema: { type: "object", properties: {} } },
      ],
    };
    const gateway = new McpAgentToolGateway(runtime as never);
    gateway.beginTurn("turn-batch");

    const result = await gateway.execute("tool_schemas", {
      pluginId: "nusashell.notes",
      toolNames: ["createNote", "listNotes", "missingTool"],
    }, "call-batch", "turn-batch") as { granted: Array<{ name: string }>; missing?: string[] };

    expect(result.granted.map((g) => g.name)).toHaveLength(2);
    expect(result.missing).toEqual(["missingTool"]);
    const names = (await gateway.listTools([], "turn-batch")).map((tool) => tool.name);
    for (const g of result.granted) expect(names).toContain(g.name);
    expect(names).not.toContain("missingTool");
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
      create: async () => ({ ...summary, files: [] }),
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
    expect(toolNames).toEqual(expect.arrayContaining(["skill_list", "skill_search", "skill_read", "skill_manage"]));
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

  it("returns memory_not_configured when no memory store is wired", async () => {
    const gateway = new McpAgentToolGateway(fakeRuntime as never);
    gateway.beginTurn("turn-mem");

    await expect(gateway.execute("memory", { action: "add", target: "memory", content: "test" }, "call-mem", "turn-mem")).resolves.toEqual({
      ok: false,
      error: { code: "memory_not_configured", message: "Memory store is not configured" },
      meta: {},
    });
  });

  it("handles memory add/replace/remove via the gateway", async () => {
    let memoryEntries = [{ text: "existing note", createdAt: null as string | null }];
    const fakeMemory: MemoryStorePort = {
      loadSnapshot: async (): Promise<MemorySnapshot> => ({
        memory: memoryEntries,
        user: [],
        usage: {
          memory: { chars: memoryEntries.map((e) => e.text).join("\n§\n").length, limit: 2200 },
          user: { chars: 0, limit: 1375 },
        },
      }),
      add: async (_target, content): Promise<MemoryMutationResult> => {
        memoryEntries = [...memoryEntries, { text: content, createdAt: "2026-08-03T00:00:00.000Z" }];
        return { ok: true, data: { entries: memoryEntries, usage: { chars: 100, limit: 2200 } } };
      },
      replace: async (_target, _oldText, content): Promise<MemoryMutationResult> => {
        memoryEntries = [{ text: content, createdAt: null }];
        return { ok: true, data: { entries: memoryEntries, usage: { chars: 50, limit: 2200 } } };
      },
      remove: async (_target, _oldText): Promise<MemoryMutationResult> => {
        memoryEntries = [];
        return { ok: true, data: { entries: [], usage: { chars: 0, limit: 2200 } } };
      },
    };
    const gateway = new McpAgentToolGateway(fakeRuntime as never, undefined, undefined, undefined, fakeMemory);
    gateway.beginTurn("turn-mem-2");

    const addResult = await gateway.execute("memory", { action: "add", target: "memory", content: "new note" }, "call-add", "turn-mem-2");
    expect(addResult).toEqual({
      ok: true,
      data: {
        entries: [
          { text: "existing note", createdAt: null },
          { text: "new note", createdAt: "2026-08-03T00:00:00.000Z" },
        ],
        usage: { chars: 100, limit: 2200 },
      },
    });

    const replaceResult = await gateway.execute("memory", { action: "replace", target: "memory", old_text: "existing", content: "updated" }, "call-replace", "turn-mem-2");
    expect(replaceResult).toEqual({
      ok: true,
      data: { entries: [{ text: "updated", createdAt: null }], usage: { chars: 50, limit: 2200 } },
    });

    const removeResult = await gateway.execute("memory", { action: "remove", target: "memory", old_text: "updated" }, "call-remove", "turn-mem-2");
    expect(removeResult).toEqual({
      ok: true,
      data: { entries: [], usage: { chars: 0, limit: 2200 } },
    });
  });

  it("returns memory_error on capacity overflow", async () => {
    const fakeMemory: MemoryStorePort = {
      loadSnapshot: async () => ({ memory: [], user: [], usage: { memory: { chars: 0, limit: 2200 }, user: { chars: 0, limit: 1375 } } }),
      add: async () => { throw new Error("Memory capacity exceeded for \"memory\": 2300/2200 chars (overflow 100). Remove or shorten entries first."); },
      replace: async () => { throw new Error("not reached"); },
      remove: async () => { throw new Error("not reached"); },
    };
    const gateway = new McpAgentToolGateway(fakeRuntime as never, undefined, undefined, undefined, fakeMemory);
    gateway.beginTurn("turn-mem-3");

    const result = await gateway.execute("memory", { action: "add", target: "memory", content: "x".repeat(2300) }, "call-overflow", "turn-mem-3");
    expect(result).toEqual({
      ok: false,
      error: { code: "memory_error", message: "Memory capacity exceeded for \"memory\": 2300/2200 chars (overflow 100). Remove or shorten entries first." },
      meta: {},
    });
  });

  describe("skill_manage", () => {
    const skillSummary = {
      id: "my-skill",
      name: "my-skill",
      description: "A test skill.",
      fileCount: 1,
      updatedAt: "2026-08-01T00:00:00.000Z",
    };
    const skillDetail = { ...skillSummary, files: [{ path: "SKILL.md", type: "file" as const, sizeBytes: 50, editable: true }] };
    const skillMd = "---\nname: my-skill\ndescription: A test skill.\n---\n# My Skill\nDo things.";

    function fakeRegistry(overrides: Partial<SkillRegistryPort> = {}): SkillRegistryPort {
      return {
        list: async () => [skillSummary],
        search: async () => [skillSummary],
        get: async () => skillDetail,
        read: async (skillId, path = "SKILL.md") => ({ skillId, path, content: skillMd, sizeBytes: skillMd.length, editable: true, truncated: false }),
        installFromArchive: async () => skillDetail,
        create: async () => skillDetail,
        write: async (skillId, path) => ({ skillId, path, content: skillMd, sizeBytes: skillMd.length, editable: true, truncated: false }),
        delete: async () => {},
        ...overrides,
      };
    }

    function fakeProvenance(originMap: Record<string, "agent" | "user"> = {}): SkillProvenancePort {
      return {
        get: async (id) => originMap[id] ?? "user",
        markAgent: async () => {},
        markUser: async () => {},
        clear: async () => {},
      };
    }

    it("creates a new agent-owned skill and marks provenance", async () => {
      const registry = fakeRegistry();
      const provenance = fakeProvenance();
      let markedAgent = false;
      provenance.markAgent = async () => { markedAgent = true; };
      const gateway = new McpAgentToolGateway(fakeRuntime as never, undefined, registry, undefined, undefined, provenance);
      gateway.beginTurn("turn-create");

      const result = await gateway.execute("skill_manage", { action: "create", name: "my-skill", content: skillMd }, "call-create", "turn-create");
      expect(result).toEqual({ ok: true, data: skillDetail, meta: { provenance: "agent" } });
      expect(markedAgent).toBe(true);
    });

    it("rejects create when skill already exists", async () => {
      const registry = fakeRegistry({
        create: async () => { throw new Error("Skill already exists: my-skill"); },
      });
      const gateway = new McpAgentToolGateway(fakeRuntime as never, undefined, registry, undefined, undefined, fakeProvenance());
      gateway.beginTurn("turn-dup");

      const result = await gateway.execute("skill_manage", { action: "create", name: "my-skill", content: skillMd }, "call-dup", "turn-dup");
      expect(result).toEqual({
        ok: false,
        error: { code: "skill_exists", message: "Skill already exists: my-skill" },
        meta: {},
      });
    });

    it("rejects create with description > 1024 chars", async () => {
      const longDesc = "x".repeat(1025);
      const registry = fakeRegistry({
        create: async () => { throw new Error("SKILL.md description must be 1024 characters or fewer (got 1025)"); },
      });
      const gateway = new McpAgentToolGateway(fakeRuntime as never, undefined, registry, undefined, undefined, fakeProvenance());
      gateway.beginTurn("turn-long");

      const result = await gateway.execute("skill_manage", { action: "create", name: "my-skill", content: skillMd }, "call-long", "turn-long");
      expect(result).toEqual({
        ok: false,
        error: { code: "description_too_long", message: "SKILL.md description must be 1024 characters or fewer (got 1025)" },
        meta: {},
      });
    });

    it("edits an agent-owned skill", async () => {
      const registry = fakeRegistry();
      const provenance = fakeProvenance({ "my-skill": "agent" });
      const gateway = new McpAgentToolGateway(fakeRuntime as never, undefined, registry, undefined, undefined, provenance);
      gateway.beginTurn("turn-edit");

      const result = await gateway.execute("skill_manage", { action: "edit", name: "my-skill", content: skillMd }, "call-edit", "turn-edit");
      expect(result).toEqual({ ok: true, data: expect.objectContaining({ skillId: "my-skill" }), meta: { provenance: "agent" } });
    });

    it("blocks edit on a user-owned skill", async () => {
      const registry = fakeRegistry();
      const provenance = fakeProvenance({ "my-skill": "user" });
      const gateway = new McpAgentToolGateway(fakeRuntime as never, undefined, registry, undefined, undefined, provenance);
      gateway.beginTurn("turn-protect");

      const result = await gateway.execute("skill_manage", { action: "edit", name: "my-skill", content: skillMd }, "call-protect", "turn-protect");
      expect(result).toEqual({
        ok: false,
        error: { code: "skill_protected", message: 'Skill "my-skill" is not agent-owned and cannot be mutated by the model' },
        meta: {},
      });
    });

    it("writes a support file to an agent-owned skill", async () => {
      const registry = fakeRegistry();
      const provenance = fakeProvenance({ "my-skill": "agent" });
      const gateway = new McpAgentToolGateway(fakeRuntime as never, undefined, registry, undefined, undefined, provenance);
      gateway.beginTurn("turn-writefile");

      const result = await gateway.execute("skill_manage", { action: "write_file", name: "my-skill", path: "references/guide.md", content: "# Guide" }, "call-writefile", "turn-writefile");
      expect(result).toEqual({ ok: true, data: expect.objectContaining({ skillId: "my-skill" }), meta: { provenance: "agent" } });
    });

    it("blocks write_file on a user-owned skill", async () => {
      const registry = fakeRegistry();
      const provenance = fakeProvenance({ "my-skill": "user" });
      const gateway = new McpAgentToolGateway(fakeRuntime as never, undefined, registry, undefined, undefined, provenance);
      gateway.beginTurn("turn-wf-protect");

      const result = await gateway.execute("skill_manage", { action: "write_file", name: "my-skill", path: "references/guide.md", content: "# Guide" }, "call-wf-protect", "turn-wf-protect");
      expect(result).toEqual({
        ok: false,
        error: { code: "skill_protected", message: 'Skill "my-skill" is not agent-owned and cannot be mutated by the model' },
        meta: {},
      });
    });

    it("deletes an agent-owned skill and clears provenance", async () => {
      const registry = fakeRegistry();
      let cleared = false;
      const provenance = fakeProvenance({ "my-skill": "agent" });
      provenance.clear = async () => { cleared = true; };
      const gateway = new McpAgentToolGateway(fakeRuntime as never, undefined, registry, undefined, undefined, provenance);
      gateway.beginTurn("turn-delete");

      const result = await gateway.execute("skill_manage", { action: "delete", name: "my-skill" }, "call-delete", "turn-delete");
      expect(result).toEqual({ ok: true, data: { deleted: "my-skill" }, meta: { provenance: "agent" } });
      expect(cleared).toBe(true);
    });

    it("blocks delete on a user-owned skill", async () => {
      const registry = fakeRegistry();
      const provenance = fakeProvenance({ "my-skill": "user" });
      const gateway = new McpAgentToolGateway(fakeRuntime as never, undefined, registry, undefined, undefined, provenance);
      gateway.beginTurn("turn-del-protect");

      const result = await gateway.execute("skill_manage", { action: "delete", name: "my-skill" }, "call-del-protect", "turn-del-protect");
      expect(result).toEqual({
        ok: false,
        error: { code: "skill_protected", message: 'Skill "my-skill" is not agent-owned and cannot be mutated by the model' },
        meta: {},
      });
    });

    it("returns skills_not_configured when no provenance is wired", async () => {
      const registry = fakeRegistry();
      const gateway = new McpAgentToolGateway(fakeRuntime as never, undefined, registry);
      gateway.beginTurn("turn-no-prov");

      const result = await gateway.execute("skill_manage", { action: "create", name: "my-skill", content: skillMd }, "call-no-prov", "turn-no-prov");
      expect(result).toEqual({
        ok: false,
        error: { code: "skills_not_configured", message: "Skill registry is not configured" },
        meta: { data_is_untrusted: true },
      });
    });

    it("blocks delete of a pinned skill", async () => {
      const registry = fakeRegistry();
      const provenance = fakeProvenance({ "my-skill": "agent" });
      const usage = {
        getRecord: async () => ({ skillId: "my-skill", useCount: 0, viewCount: 0, patchCount: 0, lastUsedAt: null, lastViewedAt: null, lastPatchedAt: null, state: "active" as const, pinned: true, archivedAt: null, createdAt: new Date().toISOString() }),
        record: async () => {},
        setState: async () => {},
        setPinned: async () => {},
        clear: async () => {},
        listRecords: async () => [],
      };
      const gateway = new McpAgentToolGateway(fakeRuntime as never, undefined, registry, undefined, undefined, provenance, undefined, usage as never);
      gateway.beginTurn("turn-pinned-del");

      const result = await gateway.execute("skill_manage", { action: "delete", name: "my-skill" }, "call-pinned-del", "turn-pinned-del");
      expect(result).toEqual({
        ok: false,
        error: { code: "skill_pinned", message: 'Skill "my-skill" is pinned and cannot be deleted' },
        meta: {},
      });
    });
  });

  describe("ask_question", () => {
    it("omits ask_question when the turn is not interactive", async () => {
      const { AskQuestionService } = await import("../src/index.js");
      const asks = new AskQuestionService();
      const gateway = new McpAgentToolGateway(fakeRuntime as never, undefined, undefined, undefined, undefined, undefined, undefined, undefined, asks);
      gateway.beginTurn("turn-non-interactive");
      expect((await gateway.listTools([], "turn-non-interactive")).map((tool) => tool.name)).not.toContain("ask_question");
    });

    it("lists ask_question for interactive turns and resolves via AskQuestionService", async () => {
      const { AskQuestionService } = await import("../src/index.js");
      const asks = new AskQuestionService();
      const gateway = new McpAgentToolGateway(fakeRuntime as never, undefined, undefined, undefined, undefined, undefined, undefined, undefined, asks);
      gateway.beginTurn("turn-ask", { interactive: true });
      expect((await gateway.listTools([], "turn-ask")).map((tool) => tool.name)).toContain("ask_question");

      const pending = gateway.execute("ask_question", {
        question: "Pick one",
        options: [
          { id: "a", label: "Alpha", default: true },
          { id: "b", label: "Beta", description: "Second choice" },
        ],
      }, "req-uuid", "turn-ask", "call-ask-1");

      const answered = asks.answer("turn-ask", "call-ask-1", { via: "option", optionIds: ["b"] });
      expect(answered).toEqual({
        ok: true,
        data: { via: "option", answer: "Beta", optionIds: ["b"] },
        meta: {},
      });
      await expect(pending).resolves.toEqual(answered);
    });

    it("rejects pending asks when the turn is cancelled", async () => {
      const { AskQuestionService } = await import("../src/index.js");
      const asks = new AskQuestionService();
      const gateway = new McpAgentToolGateway(fakeRuntime as never, undefined, undefined, undefined, undefined, undefined, undefined, undefined, asks);
      gateway.beginTurn("turn-cancel", { interactive: true });

      const pending = gateway.execute("ask_question", {
        question: "Continue?",
        options: [{ id: "yes", label: "Yes" }],
      }, "req-uuid", "turn-cancel", "call-ask-2");

      await gateway.cancelTurn("turn-cancel");
      await expect(pending).rejects.toThrow(/interrupted|cancelled/i);
    });
  });

  describe("concurrent execution + cancelTurn", () => {
    it("tracks overlapping activeCalls on different plugins and cancels both", async () => {
      let resolveA: (value: unknown) => void = () => {};
      let resolveB: (value: unknown) => void = () => {};
      const callToolCalls: Array<{ requestId: string; pluginId: string }> = [];
      const cancelCalls: Array<{ requestId: string; pluginId: string }> = [];
      const runtime = {
        ...fakeRuntime,
        listTools: async (pluginId: unknown) => {
          const id = String(pluginId);
          if (id === "nusashell.mail") return [{ name: "readB", description: "read B", inputSchema: { type: "object" } }];
          return [{ name: "readA", description: "read A", inputSchema: { type: "object" } }];
        },
        callTool: async (_pluginId: unknown, req: { requestId: string; toolName: string }) => {
          callToolCalls.push({ requestId: req.requestId, pluginId: String(_pluginId) });
          if (req.toolName === "readA") return new Promise((r) => { resolveA = r; });
          if (req.toolName === "readB") return new Promise((r) => { resolveB = r; });
          return { ok: true };
        },
        cancelTool: async (pluginId: unknown, requestId: string) => {
          cancelCalls.push({ requestId, pluginId: String(pluginId) });
        },
      };
      const gateway = new McpAgentToolGateway(runtime as never);
      gateway.beginTurn("turn-concurrent");
      const grantA = await gateway.execute("tool_schemas", { pluginId: "nusashell.notes", toolNames: ["readA"] }, "grant-a", "turn-concurrent") as { granted: Array<{ name: string }> };
      const grantB = await gateway.execute("tool_schemas", { pluginId: "nusashell.mail", toolNames: ["readB"] }, "grant-b", "turn-concurrent") as { granted: Array<{ name: string }> };
      const nameA = grantA.granted[0]!.name;
      const nameB = grantB.granted[0]!.name;

      const execA = gateway.execute(nameA, {}, "req-a", "turn-concurrent", "call-a");
      const execB = gateway.execute(nameB, {}, "req-b", "turn-concurrent", "call-b");

      // Both should have registered in activeCalls and started callTool.
      await Promise.resolve();
      await Promise.resolve();
      expect(callToolCalls.map((c) => c.requestId)).toEqual(expect.arrayContaining(["req-a", "req-b"]));

      await gateway.cancelTurn("turn-concurrent");
      expect(cancelCalls.map((c) => c.requestId)).toEqual(expect.arrayContaining(["req-a", "req-b"]));

      resolveA({ ok: true });
      resolveB({ ok: true });
      await Promise.allSettled([execA, execB]);
      gateway.endTurn("turn-concurrent");
    });

    it("unregisters requestIds after callTool settles", async () => {
      const runtime = {
        ...fakeRuntime,
        listTools: async () => [{ name: "ping", description: "ping", inputSchema: { type: "object" } }],
        callTool: async () => "pong",
      };
      const gateway = new McpAgentToolGateway(runtime as never);
      gateway.beginTurn("turn-unreg");
      const grant = await gateway.execute("tool_schemas", { pluginId: "nusashell.notes", toolNames: ["ping"] }, "grant", "turn-unreg") as { granted: Array<{ name: string }> };
      const name = grant.granted[0]!.name;

      await gateway.execute(name, {}, "req-1", "turn-unreg", "call-1");

      // After settling, cancelTurn should have nothing to cancel (no error, no-op).
      await expect(gateway.cancelTurn("turn-unreg")).resolves.toBeUndefined();
      gateway.endTurn("turn-unreg");
    });
  });

  describe("workspace binding", () => {
    const WS = path.resolve("/tmp/proj");

    function makeRuntime(opts: {
      tools?: Array<{ name: string; description?: string; inputSchema?: unknown }>;
      syncWorkspace?: (pluginId: unknown, workspace: string) => Promise<unknown>;
      getLaunchSpec?: (pluginId: unknown) => Promise<unknown>;
      startPlugin?: (pluginId: unknown, options?: unknown) => Promise<unknown>;
    } = {}) {
      const calls: Array<{ pluginId: string; toolName: string; args: unknown }> = [];
      const syncs: Array<{ pluginId: string; workspace: string }> = [];
      return {
        calls,
        syncs,
        runtime: {
          listPlugins: async () => [{ pluginId: "nusashell.terminal", state: "running" }],
          listTools: async () => opts.tools ?? [
            { name: "terminal_exec", description: "Run command", inputSchema: { type: "object" } },
            { name: "files_read", description: "Read file", inputSchema: { type: "object" } },
          ],
          startPlugin: async (pluginId: unknown, options?: unknown) => {
            await opts.startPlugin?.(pluginId, options);
            return { pluginId: "nusashell.terminal", state: "running" };
          },
          stopPlugin: async () => ({ pluginId: "nusashell.terminal", state: "idle" }),
          callTool: async (pluginId: unknown, options: { toolName: string; args: unknown }) => {
            calls.push({ pluginId: pluginId as string, toolName: options.toolName, args: options.args });
            return { ok: true };
          },
          ...(opts.syncWorkspace ? { syncWorkspace: async (pluginId: unknown, workspace: string) => { syncs.push({ pluginId: pluginId as string, workspace }); return opts.syncWorkspace!(pluginId, workspace); } } : {}),
          ...(opts.getLaunchSpec ? { getLaunchSpec: opts.getLaunchSpec } : {}),
        },
      };
    }

    it("wraps Terminal cwd with the turn workspace before calling the tool", async () => {
      const { runtime, calls } = makeRuntime();
      const gateway = new McpAgentToolGateway(runtime as never);
      gateway.beginTurn("turn-ws", { workspace: WS });
      const grant = await gateway.execute("tool_schemas", { pluginId: "nusashell.terminal", toolNames: ["terminal_exec"] }, "g", "turn-ws") as { granted: Array<{ name: string }> };
      await gateway.execute(grant.granted[0]!.name, { command: "pwd" }, "req", "turn-ws");
      expect(calls[0]!.args).toEqual({ command: "pwd", cwd: WS });
      gateway.endTurn("turn-ws");
    });

    it("wraps Files relative paths with the turn workspace", async () => {
      const { runtime, calls } = makeRuntime();
      const gateway = new McpAgentToolGateway(runtime as never);
      gateway.beginTurn("turn-ws", { workspace: WS });
      const grant = await gateway.execute("tool_schemas", { pluginId: "nusashell.files", toolNames: ["files_read"] }, "g", "turn-ws") as { granted: Array<{ name: string }> };
      await gateway.execute(grant.granted[0]!.name, { path: "src/foo.ts" }, "req", "turn-ws");
      const filePath = path.join(WS, "src/foo.ts");
      expect(calls[0]!.args).toEqual({ path: filePath });
      gateway.endTurn("turn-ws");
    });

    it("syncs workspace to the runtime manager before a granted call", async () => {
      const { runtime, syncs } = makeRuntime({ syncWorkspace: async () => ({ mode: "roots", respawned: false }) });
      const gateway = new McpAgentToolGateway(runtime as never);
      gateway.beginTurn("turn-sync", { workspace: WS });
      const grant = await gateway.execute("tool_schemas", { pluginId: "nusashell.files", toolNames: ["files_read"] }, "g", "turn-sync") as { granted: Array<{ name: string }> };
      await gateway.execute(grant.granted[0]!.name, { path: "x" }, "req", "turn-sync");
      expect(syncs).toEqual([{ pluginId: "nusashell.files", workspace: WS }]);
      gateway.endTurn("turn-sync");
    });

    it("does not sync or wrap when no workspace is set", async () => {
      const { runtime, calls, syncs } = makeRuntime({ syncWorkspace: async () => ({ mode: "roots", respawned: false }) });
      const gateway = new McpAgentToolGateway(runtime as never);
      gateway.beginTurn("turn-nows");
      const grant = await gateway.execute("tool_schemas", { pluginId: "nusashell.terminal", toolNames: ["terminal_exec"] }, "g", "turn-nows") as { granted: Array<{ name: string }> };
      await gateway.execute(grant.granted[0]!.name, { command: "pwd" }, "req", "turn-nows");
      expect(calls[0]!.args).toEqual({ command: "pwd" });
      expect(syncs).toEqual([]);
      gateway.endTurn("turn-nows");
    });

    it("preserves workspace when a later beginTurn omits it (AgentTurnRunner merge)", async () => {
      const { runtime, calls } = makeRuntime();
      const gateway = new McpAgentToolGateway(runtime as never);
      gateway.beginTurn("turn-merge", { workspace: WS });
      // Simulates RunAgentTurnHandler then AgentTurnRunner beginTurn(interactive only).
      gateway.beginTurn("turn-merge", { interactive: true });
      const grant = await gateway.execute("tool_schemas", { pluginId: "nusashell.terminal", toolNames: ["terminal_exec"] }, "g", "turn-merge") as { granted: Array<{ name: string }> };
      await gateway.execute(grant.granted[0]!.name, { command: "pwd" }, "req", "turn-merge");
      expect(calls[0]!.args).toEqual({ command: "pwd", cwd: WS });
      gateway.endTurn("turn-merge");
    });

    it("mcp_enable forwards args/env overrides and the turn workspace", async () => {
      const starts: Array<{ pluginId: unknown; options: unknown }> = [];
      const { runtime } = makeRuntime({
        startPlugin: async (pluginId: unknown, options: unknown) => { starts.push({ pluginId, options }); },
      });
      const gateway = new McpAgentToolGateway(runtime as never);
      gateway.beginTurn("turn-enable", { workspace: WS });
      await gateway.execute("mcp_enable", { pluginId: "nusashell.files", args: ["mcp/server.cjs", "--root", "/tmp/proj"], env: { NUSASHELL_FILES_ROOT: "/tmp/proj" } }, "call", "turn-enable");
      expect(starts[0]!.options).toEqual({
        args: ["mcp/server.cjs", "--root", "/tmp/proj"],
        env: { NUSASHELL_FILES_ROOT: "/tmp/proj" },
        workspace: WS,
      });
      gateway.endTurn("turn-enable");
    });

    it("mcp_enable passes undefined options when no overrides and no workspace", async () => {
      const starts: Array<{ pluginId: unknown; options: unknown }> = [];
      const { runtime } = makeRuntime({
        startPlugin: async (pluginId: unknown, options: unknown) => { starts.push({ pluginId, options }); },
      });
      const gateway = new McpAgentToolGateway(runtime as never);
      gateway.beginTurn("turn-enable-bare");
      await gateway.execute("mcp_enable", { pluginId: "nusashell.files" }, "call", "turn-enable-bare");
      expect(starts[0]!.options).toBeUndefined();
      gateway.endTurn("turn-enable-bare");
    });

    it("mcp_enable ignores empty args/env overrides", async () => {
      const starts: Array<{ pluginId: unknown; options: unknown }> = [];
      const { runtime } = makeRuntime({
        startPlugin: async (pluginId: unknown, options: unknown) => { starts.push({ pluginId, options }); },
      });
      const gateway = new McpAgentToolGateway(runtime as never);
      gateway.beginTurn("turn-enable-empty", { workspace: WS });
      await gateway.execute("mcp_enable", { pluginId: "nusashell.files", args: [], env: {} }, "call", "turn-enable-empty");
      expect(starts[0]!.options).toEqual({ workspace: WS });
      gateway.endTurn("turn-enable-empty");
    });

    it("mcp_list enriches plugins with launchSpec (redacted env keys)", async () => {
      const { runtime } = makeRuntime({
        getLaunchSpec: async () => ({
          pluginId: "nusashell.terminal",
          transport: "stdio",
          command: "node",
          args: ["mcp/server.cjs"],
          envKeys: ["NUSASHELL_FILES_ROOT", "SECRET_TOKEN"],
          rootsCapable: false,
        }),
      });
      const gateway = new McpAgentToolGateway(runtime as never);
      gateway.beginTurn("turn-list");
      const list = await gateway.execute("mcp_list", {}, "call", "turn-list") as Array<{ pluginId: string; launchSpec: { envKeys: string[]; command: string } }>;
      expect(list[0]!.launchSpec.envKeys).toEqual(["NUSASHELL_FILES_ROOT", "SECRET_TOKEN"]);
      expect(list[0]!.launchSpec.command).toBe("node");
      gateway.endTurn("turn-list");
    });
  });

  describe("job", () => {
    function fakeJob(overrides: Partial<Job> = {}): Job {
      return {
        id: "job-1",
        name: "Daily digest",
        trigger: { kind: "schedule", schedule: { kind: "interval", minutes: 60 } },
        mode: { type: "agent", prompt: "Summarize the inbox" },
        enabled: true,
        repeat: { times: null, completed: 0 },
        nextRunAt: "2026-08-01T10:00:00.000Z",
        lastRunAt: null,
        lastStatus: null,
        lastError: null,
        createdAt: "2026-08-01T09:00:00.000Z",
        ...overrides,
      };
    }

    function fakeStore(jobs: Job[] = [], outputs: JobOutputEntry[] = []): JobStorePort {
      const store: JobStorePort = {
        create: async (job) => { jobs.push(job); return job; },
        update: async (job) => {
          const idx = jobs.findIndex((j) => j.id === job.id);
          if (idx >= 0) jobs[idx] = job;
          return job;
        },
        get: async (id) => jobs.find((j) => j.id === id) ?? null,
        list: async () => [...jobs],
        remove: async (id) => {
          const idx = jobs.findIndex((j) => j.id === id);
          if (idx >= 0) jobs.splice(idx, 1);
        },
        markRun: async (id, status, error, nextRunAt) => {
          const job = jobs.find((j) => j.id === id);
          if (!job) return null;
          const updated = { ...job, lastRunAt: "2026-08-01T10:00:00.000Z", lastStatus: status, lastError: error, nextRunAt };
          const idx = jobs.findIndex((j) => j.id === id);
          jobs[idx] = updated;
          return updated;
        },
        claimFire: async () => true,
        releaseFire: async () => {},
        listDue: async () => [],
        appendOutput: async (jobId, entry) => { outputs.unshift(entry); },
        listOutputs: async (jobId, limit) => outputs.slice(0, limit),
      };
      return store;
    }

    function fakeScheduler(): JobScheduler {
      const runOneNow = async (jobId: string): Promise<{ ok: boolean; error?: string }> => {
        if (jobId === "missing") return { ok: false, error: "job not found" };
        return { ok: true };
      };
      const cancel = async (_jobId: string): Promise<{ ok: boolean; error?: string }> => {
        return { ok: false, error: "job is not running" };
      };
      const isRunning = (_jobId: string): boolean => false;
      const activeTraceId = (_jobId: string): string | null => null;
      return { runOneNow, cancel, isRunning, activeTraceId } as unknown as JobScheduler;
    }

    it("omits the job tool when jobs are not bound", async () => {
      const gateway = new McpAgentToolGateway(fakeRuntime as never);
      gateway.beginTurn("turn-no-jobs");
      expect((await gateway.listTools([], "turn-no-jobs")).map((t) => t.name)).not.toContain("job");
      await expect(gateway.execute("job", { action: "list" }, "c", "turn-no-jobs")).resolves.toEqual({
        ok: false,
        error: { code: "jobs_not_configured", message: "Job store is not configured" },
        meta: {},
      });
    });

    it("lists the job tool when bound and runs list", async () => {
      const store = fakeStore([fakeJob()]);
      const gateway = new McpAgentToolGateway(fakeRuntime as never);
      gateway.bindJobs(store, fakeScheduler());
      gateway.beginTurn("turn-list");
      expect((await gateway.listTools([], "turn-list")).map((t) => t.name)).toContain("job");

      const result = await gateway.execute("job", { action: "list" }, "c-list", "turn-list");
      expect(result).toEqual({
        ok: true,
        data: {
          jobs: [{
            id: "job-1",
            name: "Daily digest",
            trigger: "every 1h",
            enabled: true,
            nextRunAt: "2026-08-01T10:00:00.000Z",
            lastStatus: null,
            running: false,
          }],
        },
        meta: { count: 1 },
      });
    });

    it("validates a schedule and rejects a bad one with an envelope", async () => {
      const gateway = new McpAgentToolGateway(fakeRuntime as never);
      gateway.bindJobs(fakeStore(), fakeScheduler());
      gateway.beginTurn("turn-validate");

      const ok = await gateway.execute("job", { action: "validate_schedule", schedule: "every 30m" }, "c-vok", "turn-validate");
      expect(ok).toEqual({ ok: true, data: { description: "every 30m" }, meta: {} });

      const bad = await gateway.execute("job", { action: "validate_schedule", schedule: "garbage" }, "c-vbad", "turn-validate");
      expect(bad).toEqual({
        ok: false,
        error: { code: "job_invalid_schedule", message: expect.stringMatching(/unrecognized schedule/) },
        meta: {},
      });
    });

    it("adds an agent-mode job and returns the created job", async () => {
      const store = fakeStore();
      const gateway = new McpAgentToolGateway(fakeRuntime as never);
      gateway.bindJobs(store, fakeScheduler());
      gateway.beginTurn("turn-add");

      const result = await gateway.execute("job", {
        action: "add",
        name: "Inbox sweep",
        schedule: "0 9 * * *",
        mode: "agent",
        prompt: "Summarize the inbox",
      }, "c-add", "turn-add") as { ok: boolean; data: Job };
      expect(result.ok).toBe(true);
      expect(result.data.name).toBe("Inbox sweep");
      expect(result.data.trigger).toEqual({ kind: "schedule", schedule: { kind: "cron", expr: "0 9 * * *" } });
      expect(result.data.mode).toEqual({ type: "agent", prompt: "Summarize the inbox" });
      expect(result.data.enabled).toBe(true);
      expect(result.data.nextRunAt).not.toBeNull();
    });

    it("adds a tool-mode job with static args", async () => {
      const store = fakeStore();
      const gateway = new McpAgentToolGateway(fakeRuntime as never);
      gateway.bindJobs(store, fakeScheduler());
      gateway.beginTurn("turn-add-tool");

      const result = await gateway.execute("job", {
        action: "add",
        name: "Notes ping",
        schedule: "every 1h",
        mode: "tool",
        pluginId: "nusashell.notes",
        toolName: "createNote",
        args: { text: "hello" },
        repeat_times: 5,
      }, "c-add-tool", "turn-add-tool") as { ok: boolean; data: Job };
      expect(result.ok).toBe(true);
      expect(result.data.mode).toEqual({
        type: "tool",
        pluginId: "nusashell.notes",
        toolName: "createNote",
        args: { text: "hello" },
      });
      expect(result.data.repeat.times).toBe(5);
    });

    it("rejects add with an invalid schedule via an envelope", async () => {
      const store = fakeStore();
      const gateway = new McpAgentToolGateway(fakeRuntime as never);
      gateway.bindJobs(store, fakeScheduler());
      gateway.beginTurn("turn-add-bad");

      const result = await gateway.execute("job", {
        action: "add",
        name: "bad",
        schedule: "not a schedule",
        mode: "agent",
        prompt: "x",
      }, "c-add-bad", "turn-add-bad");
      expect(result).toEqual({
        ok: false,
        error: { code: "job_invalid_schedule", message: expect.stringMatching(/unrecognized schedule/) },
        meta: {},
      });
      expect(await store.list()).toHaveLength(0);
    });

    it("set_enabled pauses and resumes a job", async () => {
      const store = fakeStore([fakeJob()]);
      const gateway = new McpAgentToolGateway(fakeRuntime as never);
      gateway.bindJobs(store, fakeScheduler());
      gateway.beginTurn("turn-pause");

      const paused = await gateway.execute("job", { action: "set_enabled", id: "job-1", enabled: false }, "c-pause", "turn-pause") as { ok: boolean; data: Job };
      expect(paused.ok).toBe(true);
      expect(paused.data.enabled).toBe(false);

      const resumed = await gateway.execute("job", { action: "set_enabled", id: "job-1", enabled: true }, "c-resume", "turn-pause") as { ok: boolean; data: Job };
      expect(resumed.data.enabled).toBe(true);
    });

    it("set_enabled returns job_not_found for a missing id", async () => {
      const gateway = new McpAgentToolGateway(fakeRuntime as never);
      gateway.bindJobs(fakeStore(), fakeScheduler());
      gateway.beginTurn("turn-missing");

      const result = await gateway.execute("job", { action: "set_enabled", id: "nope", enabled: true }, "c", "turn-missing");
      expect(result).toEqual({
        ok: false,
        error: { code: "job_not_found", message: expect.stringMatching(/Job not found/) },
        meta: {},
      });
    });

    it("run fires a job immediately via the scheduler", async () => {
      const gateway = new McpAgentToolGateway(fakeRuntime as never);
      gateway.bindJobs(fakeStore([fakeJob()]), fakeScheduler());
      gateway.beginTurn("turn-run");

      const result = await gateway.execute("job", { action: "run", id: "job-1" }, "c-run", "turn-run");
      expect(result).toEqual({ ok: true, data: { ok: true }, meta: {} });
    });

    it("run maps a not-found scheduler error to a job_not_found envelope", async () => {
      const gateway = new McpAgentToolGateway(fakeRuntime as never);
      gateway.bindJobs(fakeStore(), fakeScheduler());
      gateway.beginTurn("turn-run-missing");

      const result = await gateway.execute("job", { action: "run", id: "missing" }, "c-run-missing", "turn-run-missing");
      expect(result).toEqual({
        ok: false,
        error: { code: "job_not_found", message: "job not found" },
        meta: {},
      });
    });

    it("removes a job and returns job_not_found when absent", async () => {
      const store = fakeStore([fakeJob()]);
      const gateway = new McpAgentToolGateway(fakeRuntime as never);
      gateway.bindJobs(store, fakeScheduler());
      gateway.beginTurn("turn-remove");

      const removed = await gateway.execute("job", { action: "remove", id: "job-1" }, "c-rm", "turn-remove");
      expect(removed).toEqual({ ok: true, data: { id: "job-1", removed: true }, meta: {} });
      expect(await store.list()).toHaveLength(0);

      const missing = await gateway.execute("job", { action: "remove", id: "job-1" }, "c-rm2", "turn-remove");
      expect(missing).toEqual({
        ok: false,
        error: { code: "job_not_found", message: expect.stringMatching(/Job not found/) },
        meta: {},
      });
    });

    it("returns recent output entries with a limit", async () => {
      const outputs: JobOutputEntry[] = [
        { jobId: "job-1", runAt: "2026-08-01T10:00:00.000Z", status: "ok", summary: "ok", path: "/o/1.md" },
        { jobId: "job-1", runAt: "2026-08-01T09:00:00.000Z", status: "error", summary: "err", path: "/o/2.md" },
      ];
      const gateway = new McpAgentToolGateway(fakeRuntime as never);
      gateway.bindJobs(fakeStore([fakeJob()], outputs), fakeScheduler());
      gateway.beginTurn("turn-out");

      const result = await gateway.execute("job", { action: "output", id: "job-1", limit: 1 }, "c-out", "turn-out");
      expect(result).toEqual({ ok: true, data: { entries: [outputs[0]] }, meta: { count: 1 } });
    });

    it("update edits an existing job's name and schedule", async () => {
      const store = fakeStore([fakeJob()]);
      const gateway = new McpAgentToolGateway(fakeRuntime as never);
      gateway.bindJobs(store, fakeScheduler());
      gateway.beginTurn("turn-update");

      const result = await gateway.execute(
        "job",
        { action: "update", id: "job-1", name: "Weekly digest", schedule: "every 7d" },
        "c-upd",
        "turn-update",
      );
      expect(result.ok).toBe(true);
      const updated = (result as { data: Job }).data;
      expect(updated.name).toBe("Weekly digest");
      expect(updated.trigger).toEqual({ kind: "schedule", schedule: { kind: "interval", minutes: 10080 } });
    });

    it("update returns job_not_found when the id is absent", async () => {
      const gateway = new McpAgentToolGateway(fakeRuntime as never);
      gateway.bindJobs(fakeStore(), fakeScheduler());
      gateway.beginTurn("turn-update-missing");

      const result = await gateway.execute("job", { action: "update", id: "nope", name: "x" }, "c", "turn-update-missing");
      expect(result).toEqual({
        ok: false,
        error: { code: "job_not_found", message: expect.stringMatching(/Job not found/) },
        meta: {},
      });
    });

    it("update maps an invalid schedule to job_invalid_schedule", async () => {
      const gateway = new McpAgentToolGateway(fakeRuntime as never);
      gateway.bindJobs(fakeStore([fakeJob()]), fakeScheduler());
      gateway.beginTurn("turn-update-bad-sched");

      const result = await gateway.execute("job", { action: "update", id: "job-1", schedule: "every" }, "c", "turn-update-bad-sched");
      expect(result).toEqual({
        ok: false,
        error: { code: "job_invalid_schedule", message: expect.any(String) },
        meta: {},
      });
    });

    it("update can switch mode from agent to tool", async () => {
      const store = fakeStore([fakeJob()]);
      const gateway = new McpAgentToolGateway(fakeRuntime as never);
      gateway.bindJobs(store, fakeScheduler());
      gateway.beginTurn("turn-update-mode");

      const result = await gateway.execute(
        "job",
        { action: "update", id: "job-1", mode: "tool", pluginId: "nusashell.mail", toolName: "send", args: { to: "x@y.com" } },
        "c",
        "turn-update-mode",
      );
      expect(result.ok).toBe(true);
      const updated = (result as { data: Job }).data;
      expect(updated.mode).toEqual({ type: "tool", pluginId: "nusashell.mail", toolName: "send", args: { to: "x@y.com" } });
    });

    it("add with agent mode inherits the caller turn's provider/model/effort", async () => {
      const store = fakeStore();
      const gateway = new McpAgentToolGateway(fakeRuntime as never);
      gateway.bindJobs(store, fakeScheduler());
      gateway.beginTurn("turn-inherit", {
        interactive: true,
        providerId: "openai",
        model: "gpt-4o",
        effort: "medium",
      });

      const result = await gateway.execute(
        "job",
        { action: "add", name: "Inherited", schedule: "every 1h", mode: "agent", prompt: "ping" },
        "c",
        "turn-inherit",
      );
      expect(result.ok).toBe(true);
      const created = (result as { data: Job }).data;
      expect(created.mode).toEqual({ type: "agent", prompt: "ping", providerId: "openai", model: "gpt-4o", effort: "medium" });
    });

    it("add with agent mode respects explicit model over caller inheritance", async () => {
      const store = fakeStore();
      const gateway = new McpAgentToolGateway(fakeRuntime as never);
      gateway.bindJobs(store, fakeScheduler());
      gateway.beginTurn("turn-override", { providerId: "openai", model: "gpt-4o" });

      const result = await gateway.execute(
        "job",
        { action: "add", name: "Override", schedule: "every 1h", mode: "agent", prompt: "ping", model: "o3-mini" },
        "c",
        "turn-override",
      );
      expect(result.ok).toBe(true);
      const created = (result as { data: Job }).data;
      expect(created.mode).toEqual({ type: "agent", prompt: "ping", providerId: "openai", model: "o3-mini" });
    });

    it("add with agent mode omits model fields when neither caller nor explicit set them", async () => {
      const store = fakeStore();
      const gateway = new McpAgentToolGateway(fakeRuntime as never);
      gateway.bindJobs(store, fakeScheduler());
      gateway.beginTurn("turn-no-model");

      const result = await gateway.execute(
        "job",
        { action: "add", name: "Plain", schedule: "every 1h", mode: "agent", prompt: "ping" },
        "c",
        "turn-no-model",
      );
      expect(result.ok).toBe(true);
      const created = (result as { data: Job }).data;
      expect(created.mode).toEqual({ type: "agent", prompt: "ping" });
    });

    it("update preserves existing agent model fields when only prompt changes", async () => {
      const store = fakeStore([fakeJob({ mode: { type: "agent", prompt: "old", providerId: "openai", model: "gpt-4o", effort: "high" } })]);
      const gateway = new McpAgentToolGateway(fakeRuntime as never);
      gateway.bindJobs(store, fakeScheduler());
      gateway.beginTurn("turn-update-prompt");

      const result = await gateway.execute(
        "job",
        { action: "update", id: "job-1", prompt: "new prompt" },
        "c",
        "turn-update-prompt",
      );
      expect(result.ok).toBe(true);
      const updated = (result as { data: Job }).data;
      expect(updated.mode).toEqual({ type: "agent", prompt: "new prompt", providerId: "openai", model: "gpt-4o", effort: "high" });
    });

    it("rejects an unknown action with an envelope", async () => {
      const gateway = new McpAgentToolGateway(fakeRuntime as never);
      gateway.bindJobs(fakeStore(), fakeScheduler());
      gateway.beginTurn("turn-bad-action");

      const result = await gateway.execute("job", { action: "frobnicate" }, "c", "turn-bad-action");
      expect(result).toEqual({
        ok: false,
        error: { code: "job_error", message: expect.stringMatching(/Unsupported job action/) },
        meta: {},
      });
    });
  });
});

