/**
 * Run vitest with an isolated TEMP/TMPDIR so app-settings and other local
 * fixtures never pollute the developer's real OS temp tree, and so CI runs
 * cleanup after the suite finishes.
 *
 * Spawns Node → vitest entry directly (not `pnpm.cmd` / `vitest.cmd`). Node 20+
 * on Windows rejects spawn of .cmd/.bat without shell:true (CVE-2024-27980),
 * which surfaced as `spawn EINVAL` in the Windows CI matrix.
 */
import { createRequire } from "node:module";
import { mkdtemp, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { spawn } from "node:child_process";

const require = createRequire(import.meta.url);
const argv = process.argv.slice(2);

function resolveSpawn(args) {
  // `node scripts/run-tests.mjs vitest run …` → node + vitest.mjs + run …
  if (args[0] === "vitest") {
    const vitestEntry = require.resolve("vitest/vitest.mjs");
    return {
      command: process.execPath,
      args: [vitestEntry, ...args.slice(1)],
      shell: false,
    };
  }
  // Generic fallback. On Windows, shell is required for .cmd/.bat shims.
  if (process.platform === "win32") {
    return {
      command: args[0] ?? "pnpm",
      args: args.slice(1),
      shell: true,
    };
  }
  return {
    command: args[0] ?? "pnpm",
    args: args.slice(1),
    shell: false,
  };
}

const testRoot = await mkdtemp(join(tmpdir(), "nusashell-test-"));
const tempEnv = process.platform === "win32"
  ? { TEMP: testRoot, TMP: testRoot, TMPDIR: testRoot }
  : { TMPDIR: testRoot, TMP: testRoot, TEMP: testRoot };

let child;
let interrupted = false;

try {
  const { command, args, shell } = resolveSpawn(argv);
  child = spawn(command, args, {
    stdio: "inherit",
    env: { ...process.env, ...tempEnv },
    shell,
    // Windows: keep cwd at the monorepo root (spawn inherits process.cwd()).
    cwd: process.cwd(),
  });

  const forwardSignal = (signal) => {
    interrupted = true;
    if (!child.killed) child.kill(signal);
  };
  process.on("SIGINT", () => forwardSignal("SIGINT"));
  process.on("SIGTERM", () => forwardSignal("SIGTERM"));

  const exitCode = await new Promise((resolve, reject) => {
    child.once("error", reject);
    child.once("exit", (code, signal) => resolve(signal ? 1 : (code ?? 1)));
  });
  process.exitCode = interrupted ? 130 : exitCode;
} finally {
  await rm(testRoot, { recursive: true, force: true }).catch(() => {});
}
