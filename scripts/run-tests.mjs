import { mkdtemp, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { spawn } from "node:child_process";

const testRoot = await mkdtemp(join(tmpdir(), "nusashell-test-"));
const tempEnv = process.platform === "win32"
  ? { TEMP: testRoot, TMP: testRoot, TMPDIR: testRoot }
  : { TMPDIR: testRoot, TMP: testRoot, TEMP: testRoot };

let child;
let interrupted = false;

try {
  child = spawn(process.platform === "win32" ? "pnpm.cmd" : "pnpm", process.argv.slice(2), {
    stdio: "inherit",
    env: { ...process.env, ...tempEnv },
  });

  const forwardSignal = (signal) => {
    interrupted = true;
    child.kill(signal);
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
