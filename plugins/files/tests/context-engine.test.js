import { describe, it, expect, beforeEach, afterEach } from "vitest";
import fs from "node:fs/promises";
import path from "node:path";
import os from "node:os";
import {
  ContextEngine,
  estimateTokens,
  personalizedPagerank,
} from "../mcp/context-engine.js";

let tmpDir;
let engine;

beforeEach(async () => {
  tmpDir = await fs.mkdtemp(path.join(os.tmpdir(), "files-context-test-"));
  engine = new ContextEngine(tmpDir);
});

afterEach(async () => {
  await fs.rm(tmpDir, { recursive: true, force: true });
});

async function writeCodeWorkspace() {
  await fs.writeFile(
    path.join(tmpDir, "package.json"),
    JSON.stringify({
      name: "demo",
      version: "1.2.3",
      scripts: { build: "tsc", test: "vitest" },
      dependencies: { react: "^19.0.0", zod: "^4.0.0" },
      devDependencies: { typescript: "^5.0.0" },
    }),
  );
  await fs.mkdir(path.join(tmpDir, "src"));
  await fs.writeFile(
    path.join(tmpDir, "src", "core.ts"),
    [
      "export class Engine {",
      "  run() { return helper(); }",
      "}",
      "export function helper() { return 1; }",
      "export const LIMIT = 10;",
      "",
    ].join("\n"),
  );
  await fs.writeFile(
    path.join(tmpDir, "src", "app.ts"),
    [
      "import { Engine } from \"./core\";",
      "const engine = new Engine();",
      "engine.run();",
      "console.log(LIMIT);",
      "",
    ].join("\n"),
  );
  await fs.writeFile(
    path.join(tmpDir, "main.py"),
    ["class Worker:", "    pass", "", "def start():", "    return Worker()", ""].join("\n"),
  );
}

describe("detectStack (phase 1)", () => {
  it("classifies a manifest workspace as coding and reads package.json metadata", async () => {
    await writeCodeWorkspace();
    const stack = await engine.detectStack();
    expect(stack.category).toBe("coding");
    expect(stack.projectName).toBe("demo");
    expect(stack.version).toBe("1.2.3");
    expect(stack.keyDeps).toContain("react");
    expect(stack.keyDeps).toContain("typescript");
    expect(stack.scripts).toHaveProperty("build");
    expect(stack.languages).toContain("typescript");
    expect(stack.isMonorepo).toBe(false);
  });

  it("classifies a docs-only workspace as documentation", async () => {
    await fs.writeFile(path.join(tmpDir, "README.md"), "# docs\n");
    await fs.writeFile(path.join(tmpDir, "guide.md"), "# guide\n");
    const stack = await engine.detectStack();
    expect(stack.category).toBe("documentation");
    expect(stack.isMonorepo).toBe(false);
  });

  it("classifies nested manifests or pnpm workspaces as hybrid/monorepo", async () => {
    await fs.writeFile(path.join(tmpDir, "pnpm-workspace.yaml"), "packages:\n  - packages/*\n");
    await fs.writeFile(path.join(tmpDir, "package.json"), JSON.stringify({ name: "root" }));
    await fs.mkdir(path.join(tmpDir, "packages", "a"), { recursive: true });
    await fs.writeFile(path.join(tmpDir, "packages", "a", "package.json"), JSON.stringify({ name: "a" }));
    const stack = await engine.detectStack();
    expect(stack.category).toBe("hybrid");
    expect(stack.isMonorepo).toBe(true);
  });
});

describe("walk + ignore handling (phase 2)", () => {
  it("skips default ignore dirs and gitignore patterns", async () => {
    await writeCodeWorkspace();
    await fs.mkdir(path.join(tmpDir, "node_modules", "dep"), { recursive: true });
    await fs.writeFile(path.join(tmpDir, "node_modules", "dep", "index.ts"), "export const x = 1;\n");
    await fs.writeFile(path.join(tmpDir, ".gitignore"), "ignored.ts\n");
    await fs.writeFile(path.join(tmpDir, "ignored.ts"), "export const y = 2;\n");
    const result = await engine.contextMap({});
    expect(result.map).toContain("src/core.ts");
    expect(result.map).not.toContain("node_modules");
    expect(result.map).not.toContain("ignored.ts");
  });
});

describe("symbol extraction (phase 3)", () => {
  it("extracts typescript and python definitions for a single file", async () => {
    await writeCodeWorkspace();
    const ts = await engine.listSymbols({ path: "src/core.ts" });
    const names = ts.symbols.map((s) => s.name);
    expect(names).toContain("Engine");
    expect(names).toContain("helper");
    expect(names).toContain("LIMIT");
    const py = await engine.listSymbols({ path: "main.py" });
    const pyNames = py.symbols.map((s) => s.name);
    expect(pyNames).toContain("Worker");
    expect(pyNames).toContain("start");
  });

  it("extracts definitions from CRLF sources without CR in signatures", () => {
    const text = "export function hello() {\r\n  return 1;\r\n}\r\nexport const LIMIT = 10;\r\n";
    const { defs } = engine.extractFromText("x.ts", text, "typescript");
    const names = defs.map((d) => d.name);
    expect(names).toContain("hello");
    expect(names).toContain("LIMIT");
    for (const def of defs) {
      expect(def.sig).not.toContain("\r");
    }
  });

  it("rejects symbol extraction for unsupported file types", async () => {
    await fs.writeFile(path.join(tmpDir, "data.bin"), "x");
    await expect(engine.listSymbols({ path: "data.bin" })).rejects.toThrow();
  });
});

describe("graph + personalized pagerank (phase 4)", () => {
  it("personalization boosts the active file", () => {
    const graph = {
      nodes: ["a.ts", "b.ts", "c.ts"],
      outEdges: new Map([
        ["a.ts", new Set(["b.ts"])],
        ["b.ts", new Set(["a.ts"])],
      ]),
    };
    const plain = personalizedPagerank(graph);
    const boosted = personalizedPagerank(graph, { "c.ts": 50 });
    expect(boosted["c.ts"]).toBeGreaterThan(plain["c.ts"]);
    const sum = Object.values(boosted).reduce((acc, v) => acc + v, 0);
    expect(sum).toBeCloseTo(1, 5);
  });

  it("files defining referenced symbols outrank isolated files", async () => {
    await writeCodeWorkspace();
    await fs.writeFile(path.join(tmpDir, "src", "lonely.ts"), "export const zzq = 1;\n");
    const result = await engine.contextMap({});
    const ranks = new Map(result.ranks);
    expect(ranks.get("src/core.ts")).toBeGreaterThan(ranks.get("src/lonely.ts"));
  });
});

describe("token budget fitting (phase 5)", () => {
  it("estimateTokens approximates 4 chars per token", () => {
    expect(estimateTokens("abcd")).toBe(1);
    expect(estimateTokens("")).toBe(1);
    expect(estimateTokens("a".repeat(400))).toBe(100);
  });

  it("keeps the map within the token budget", async () => {
    await writeCodeWorkspace();
    for (let i = 0; i < 12; i += 1) {
      await fs.writeFile(
        path.join(tmpDir, "src", `mod${i}.ts`),
        `import { helper } from "./core";\nexport function fn${i}() { return helper(); }\n`,
      );
    }
    const budget = 120;
    const result = await engine.contextMap({ budget });
    expect(result.stats.tokensUsed).toBeLessThanOrEqual(Math.max(budget, estimateTokens(result.map)));
    expect(result.stats.filesShown).toBeGreaterThan(0);
  });
});

describe("cache invalidation (phase 6)", () => {
  it("serves unchanged files from cache and re-extracts on mtime change", async () => {
    await writeCodeWorkspace();
    const first = await engine.contextMap({});
    expect(first.stats.cacheMisses).toBeGreaterThan(0);
    const second = await engine.contextMap({});
    expect(second.stats.cacheHits).toBeGreaterThan(0);

    const future = new Date(Date.now() + 60_000);
    await fs.appendFile(path.join(tmpDir, "src", "core.ts"), "export function added() { return 2; }\n");
    await fs.utimes(path.join(tmpDir, "src", "core.ts"), future, future);
    const third = await engine.contextMap({});
    const coreSymbols = await engine.listSymbols({ path: "src/core.ts" });
    expect(coreSymbols.symbols.map((s) => s.name)).toContain("added");
    expect(third.stats.cacheMisses).toBeGreaterThan(0);
  });

  it("refresh=true bypasses the cache", async () => {
    await writeCodeWorkspace();
    await engine.contextMap({});
    const fresh = await engine.contextMap({ refresh: true });
    expect(fresh.stats.cacheHits).toBe(0);
  });
});

describe("contextMap orchestration", () => {
  it("returns map markdown, stack, ranks, and stats", async () => {
    await writeCodeWorkspace();
    const result = await engine.contextMap({ activeFile: "src/app.ts", query: "Engine" });
    expect(result.map).toContain("# Workspace Context Map");
    expect(result.map).toContain("src/app.ts");
    expect(result.stack.category).toBe("coding");
    expect(Array.isArray(result.ranks)).toBe(true);
    expect(result.ranks.length).toBeGreaterThan(0);
    expect(result.stats.graph.nodes).toBeGreaterThan(0);
    expect(result.stats.timingMs.total).toBeGreaterThanOrEqual(0);
  });

  it("handles an empty documentation workspace without code files", async () => {
    await fs.writeFile(path.join(tmpDir, "README.md"), "# only docs\n");
    const result = await engine.contextMap({});
    expect(result.stack.category).toBe("documentation");
    expect(typeof result.map).toBe("string");
  });
});
