import { describe, it, expect } from "vitest";
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const bundlePath = path.resolve(__dirname, "../mcp/server.cjs");

/**
 * Regression guard for finding 1 (Files path escape via stale bundle).
 *
 * The source `mcp/config.js` rejects relative paths that escape the files
 * root via `../` traversal, but allows absolute paths (the agent is a trusted
 * actor). Production runs the esbuild bundle `mcp/server.cjs`. A stale bundle
 * that predates the guard reintroduces the escape silently. These tests
 * assert the *shipped* artifact contains the traversal check so a forgotten
 * rebuild cannot regress the guard.
 *
 * See plan: plugin_sandbox_readiness_b0476ef9 — P0.
 */
describe("server.cjs bundle containment (finding 1 regression guard)", () => {
  it("bundle exists", () => {
    expect(fs.existsSync(bundlePath)).toBe(true);
  });

  it("bundle resolvePath rejects relative paths that escape the root via traversal", () => {
    const source = fs.readFileSync(bundlePath, "utf8");
    // The guarded resolvePath must throw "Path escapes files root" for `../`
    // traversal — the stale bundle returned the resolved path with no check.
    expect(source).toContain("escapes files root");
    // The guard must compute a relative path and reject `..` escape.
    expect(source).toContain('startsWith("..")');
  });

  it("bundle does not contain the unguarded resolvePath that returns resolved directly", () => {
    const source = fs.readFileSync(bundlePath, "utf8");
    // The stale (buggy) bundle ended resolvePath with `return resolved;`
    // immediately after the isAbsolute branch, with no relative check.
    // The guarded version normalizes the root and computes `relative`.
    expect(source).toContain("normalizedRoot");
    // esbuild rewrites `path.relative` to a namespaced import; assert the
    // relative computation is present in either form.
    expect(source.includes(".relative(") || source.includes("path.relative")).toBe(true);
  });
});
