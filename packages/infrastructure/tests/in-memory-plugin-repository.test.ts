import { describe, expect, it, beforeEach } from "vitest";
import { InMemoryPluginRepository } from "../src/persistence/in-memory/in-memory-plugin.repository.js";
import { Plugin, PluginId, PluginManifest, PluginVersion } from "@nusashell/domain";

function makePlugin(id: string, enabled = true): Plugin {
  const manifestResult = PluginManifest.create({
    id,
    name: "Test Plugin",
    version: "1.0.0",
    icon: "test",
    ui: { entry: "index.html" },
    mcp: { transport: "stdio", command: "node", args: ["mcp/server.js"], env: {} },
  });
  if (!manifestResult.ok) throw new Error(manifestResult.error.message);

  const idResult = PluginId.create(id);
  if (!idResult.ok) throw new Error(idResult.error.message);

  const versionResult = PluginVersion.create("1.0.0");
  if (!versionResult.ok) throw new Error(versionResult.error.message);

  return Plugin.create({
    id: idResult.value,
    version: versionResult.value,
    manifest: manifestResult.value,
    enabled,
    installPath: `/plugins/${id}`,
    installedAt: new Date("2026-01-01T00:00:00Z"),
  });
}

describe("InMemoryPluginRepository", () => {
  let repo: InMemoryPluginRepository;

  beforeEach(() => {
    repo = new InMemoryPluginRepository();
  });

  it("returns null for unknown plugin", async () => {
    const idResult = PluginId.create("com.unknown.plugin");
    if (!idResult.ok) throw new Error("bad id");

    const plugin = await repo.findById(idResult.value);
    expect(plugin).toBeNull();
  });

  it("saves and finds a plugin by id", async () => {
    const plugin = makePlugin("nusashell.notes");
    repo.add(plugin);

    const found = await repo.findById(plugin.id);
    expect(found).not.toBeNull();
    expect(PluginId.equals(found!.id, plugin.id)).toBe(true);
  });

  it("lists all saved plugins", async () => {
    repo.add(makePlugin("example.a"));
    repo.add(makePlugin("example.b"));

    const list = await repo.list();
    expect(list).toHaveLength(2);
  });

  it("removes a plugin", async () => {
    const plugin = makePlugin("nusashell.notes");
    repo.add(plugin);

    await repo.remove(plugin.id);
    const found = await repo.findById(plugin.id);
    expect(found).toBeNull();
  });

  it("overwrites on save with same id", async () => {
    const plugin = makePlugin("nusashell.notes");
    repo.add(plugin);

    const updated = makePlugin("nusashell.notes", false);
    await repo.save(updated);

    const found = await repo.findById(plugin.id);
    expect(found).not.toBeNull();
    expect(found!.enabled).toBe(false);
  });
});
