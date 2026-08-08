import { describe, expect, it } from "vitest";
import { readFile } from "node:fs/promises";

describe("scripts/run-tests.mjs Windows spawn contract", () => {
  it("spawns Node→vitest instead of pnpm.cmd (Node 20+ spawn EINVAL on .cmd)", async () => {
    const source = await readFile(new URL("./run-tests.mjs", import.meta.url), "utf8");
    // Direct vitest entry avoids spawning pnpm.cmd/vitest.cmd without shell.
    expect(source).toMatch(/vitest\/vitest\.mjs/);
    expect(source).toMatch(/process\.execPath/);
    // Must not unconditionally spawn pnpm.cmd (CVE-2024-27980 / Node EINVAL).
    expect(source).not.toMatch(/spawn\(\s*process\.platform\s*===\s*["']win32["']\s*\?\s*["']pnpm\.cmd["']/);
  });
});
