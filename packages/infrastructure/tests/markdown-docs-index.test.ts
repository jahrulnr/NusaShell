import { describe, expect, it, beforeEach, afterEach } from "vitest";
import { mkdtemp, mkdir, rm, writeFile } from "node:fs/promises";
import { join } from "node:path";
import { tmpdir } from "node:os";
import { MarkdownDocsIndex } from "../src/index.js";
// @ts-expect-error Scan script is a plain Node .mjs file without type declarations.
import { generate } from "../../scripts/scan-ui-docs.mjs";
import { fileURLToPath } from "node:url";
import { dirname, resolve } from "node:path";

const repoRoot = resolve(dirname(fileURLToPath(import.meta.url)), "../../..");

describe("MarkdownDocsIndex", () => {
  let tempDir: string;
  let docsRoot: string;
  let storageRoot: string;

  beforeEach(async () => {
    tempDir = await mkdtemp(join(tmpdir(), "nusashell-docs-test-"));
    docsRoot = join(tempDir, "docs");
    storageRoot = join(tempDir, "index");
  });

  afterEach(async () => {
    await rm(tempDir, { recursive: true, force: true });
  });

  it("reports not usable before indexing", () => {
    const index = new MarkdownDocsIndex(docsRoot, storageRoot);
    expect(index.usable()).toBe(false);
  });

  it("builds an empty index and becomes usable", async () => {
    await mkdir(docsRoot, { recursive: true });
    const index = new MarkdownDocsIndex(docsRoot, storageRoot);
    await index.reindex();
    expect(index.usable()).toBe(true);
    expect(await index.search("anything", 5)).toEqual([]);
    expect(await index.listDocs()).toEqual([]);
  });

  it("indexes documents and lists summaries", async () => {
    await mkdir(join(docsRoot, "plugins"), { recursive: true });
    await writeFile(
      join(docsRoot, "getting-started.md"),
      ["# Getting Started", "", "## Launcher", "", "The launcher is a grid of icons."].join("\n"),
    );
    await writeFile(
      join(docsRoot, "plugins", "lifecycle.md"),
      ["# Plugin Lifecycle", "", "## Start", "", "Start a plugin with mcp_enable.", "", "## Stop", "", "Stop a plugin to save resources."].join("\n"),
    );

    const index = new MarkdownDocsIndex(docsRoot, storageRoot);
    await index.reindex();

    const docs = await index.listDocs();
    expect(docs).toHaveLength(2);
    const paths = docs.map((d) => d.path).sort();
    expect(paths).toEqual(["getting-started.md", "plugins/lifecycle.md"]);

    const startDoc = docs.find((d) => d.path === "plugins/lifecycle.md");
    expect(startDoc?.title).toBe("Plugin Lifecycle");
    expect(startDoc?.headings).toContain("Start");
    expect(startDoc?.domain).toBe("plugins");

    const rootDoc = docs.find((d) => d.path === "getting-started.md");
    expect(rootDoc?.title).toBe("Getting Started");
    expect(rootDoc?.domain).toBe("root");
  });

  it("searches and ranks chunks by keyword", async () => {
    await mkdir(docsRoot, { recursive: true });
    await writeFile(
      join(docsRoot, "plugins.md"),
      ["# Plugins", "", "## Start", "", "Start a plugin with mcp_enable."].join("\n"),
    );
    await writeFile(
      join(docsRoot, "agent.md"),
      ["# Agent", "", "## Compaction", "", "Compaction summarizes older turns."].join("\n"),
    );

    const index = new MarkdownDocsIndex(docsRoot, storageRoot);
    await index.reindex();

    const hits = await index.search("start plugin", 5);
    expect(hits.length).toBeGreaterThan(0);
    expect(hits[0]!.path).toBe("plugins.md");
    expect(hits[0]!.heading).toBe("Start");
    expect(hits[0]!.score).toBeGreaterThan(0);
  });

  it("reads a full document and a single chunk", async () => {
    await mkdir(docsRoot, { recursive: true });
    await writeFile(
      join(docsRoot, "settings.md"),
      ["# Settings", "", "## Port", "", "Default port is 9130.", "", "## Model", "", "Default model is auto."].join("\n"),
    );

    const index = new MarkdownDocsIndex(docsRoot, storageRoot);
    await index.reindex();

    const full = await index.readDoc("settings.md");
    expect(full?.title).toBe("Settings");
    expect(full?.text).toContain("Default port is 9130");
    expect(full?.text).toContain("Default model is auto");

    const hits = await index.search("port", 1);
    expect(hits).toHaveLength(1);
    const chunk = await index.readDoc(hits[0]!.path, hits[0]!.chunkId);
    expect(chunk).toBeDefined();
    expect(chunk!.text).toContain("Default port is 9130");
    expect(chunk!.text).not.toContain("Default model is auto");
    expect(chunk!.chunkId).toBe(hits[0]!.chunkId);
  });

  it("returns undefined for unknown document or chunk", async () => {
    await mkdir(docsRoot, { recursive: true });
    await writeFile(join(docsRoot, "a.md"), "# A\n\n## B\n\ntext.");

    const index = new MarkdownDocsIndex(docsRoot, storageRoot);
    await index.reindex();

    expect(await index.readDoc("missing.md")).toBeUndefined();
    expect(await index.readDoc("a.md", "missing-chunk")).toBeUndefined();
  });

  it("rebuilds when new documents are added", async () => {
    await mkdir(docsRoot, { recursive: true });
    await writeFile(join(docsRoot, "first.md"), "# First\n\ncontent.");

    const index = new MarkdownDocsIndex(docsRoot, storageRoot);
    await index.reindex();
    expect((await index.listDocs())).toHaveLength(1);

    await writeFile(join(docsRoot, "second.md"), "# Second\n\nmore.");
    await index.reindex();
    expect((await index.listDocs())).toHaveLength(2);
  });

  it("remains not usable when the docs root is missing", async () => {
    const index = new MarkdownDocsIndex(docsRoot, storageRoot);
    await expect(index.reindex()).rejects.toThrow("Docs index build failed");
    expect(index.usable()).toBe(false);
    expect(await index.search("test", 5)).toEqual([]);
    expect(await index.listDocs()).toEqual([]);
    expect(await index.readDoc("missing.md")).toBeUndefined();
  });

  it("indexes generated UI docs under the ui/ domain", async () => {
    const uiOut = join(tempDir, "ui");
    const indexRoot = tempDir;
    const htmlPath = resolve(repoRoot, "apps/desktop/src/renderer/index.html");
    const jsPaths = [
      resolve(repoRoot, "apps/desktop/src/renderer/launcher.js"),
      resolve(repoRoot, "apps/desktop/src/renderer/launcher-ui.js"),
      resolve(repoRoot, "apps/desktop/src/renderer/agent-conversation-controller.js"),
      resolve(repoRoot, "apps/desktop/src/renderer/agent-conversation-ui.js"),
      resolve(repoRoot, "apps/desktop/src/renderer/ai-model-ui.js"),
    ];

    await generate({
      uiMapPath: resolve(repoRoot, "resources/agent/docs/ui-source/ui-map.json"),
      htmlPath,
      jsPaths,
      outDir: uiOut,
      exitOnError: false,
    });

    const index = new MarkdownDocsIndex(indexRoot, storageRoot);
    await index.reindex();

    const docs = await index.listDocs();
    expect(docs.length).toBeGreaterThan(0);
    expect(docs.some((d) => d.domain === "ui" && d.path.startsWith("ui/"))).toBe(true);

    const hits = await index.search("launcher plugin grid", 5);
    expect(hits.length).toBeGreaterThan(0);
    const first = hits[0]!;
    expect(first.path.startsWith("ui/")).toBe(true);

    const full = await index.readDoc(first.path);
    expect(full).toBeDefined();
    expect(full!.text).toContain("#");
  });
});
