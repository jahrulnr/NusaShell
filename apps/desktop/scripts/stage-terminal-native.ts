/**
 * Stage node-pty into the terminal plugin directory for packaging.
 *
 * The Terminal MCP bundle (server.cjs) externalizes node-pty — it's a native
 * module that can't be bundled by esbuild. In dev, pnpm hoists node-pty to the
 * workspace root node_modules, so it resolves at runtime. In a packaged app,
 * the plugins directory is copied as extraResource without node_modules, so
 * node-pty must be staged into plugins/terminal/node_modules/ before Forge
 * runs.
 *
 * This script:
 * 1. Resolves node-pty from the workspace node_modules
 * 2. Copies it into plugins/terminal/node_modules/node-pty
 * 3. Runs electron-rebuild on it so the .node binary matches the Electron ABI
 *
 * Usage: node --experimental-strip-types scripts/stage-terminal-native.ts
 */
import { access, cp, mkdir, rm } from "node:fs/promises";
import { createRequire } from "node:module";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { promisify } from "node:util";
import { execFile } from "node:child_process";

const execFileAsync = promisify(execFile);
const __dirname = dirname(fileURLToPath(import.meta.url));
const workspaceRoot = resolve(__dirname, "../../..");
const pluginDir = join(workspaceRoot, "plugins", "terminal");
const targetDir = join(pluginDir, "node_modules", "node-pty");

async function main(): Promise<void> {
  // Resolve node-pty from the workspace root
  const require = createRequire(join(workspaceRoot, "package.json"));
  const nodePtyPath = dirname(require.resolve("node-pty/package.json"));
  console.log(`[stage-terminal-native] resolved node-pty from ${nodePtyPath}`);

  // Check if already staged (idempotent)
  try {
    await access(join(targetDir, "package.json"));
    console.log("[stage-terminal-native] node-pty already staged, skipping copy");
  } catch {
    await mkdir(join(pluginDir, "node_modules"), { recursive: true });
    await cp(nodePtyPath, targetDir, { recursive: true, force: true });
    console.log(`[stage-terminal-native] copied node-pty to ${targetDir}`);
  }

  // Rebuild node-pty for the Electron ABI
  const electronPath = join(workspaceRoot, "node_modules", "electron");
  console.log("[stage-terminal-native] running electron-rebuild for node-pty...");
  try {
    await execFileAsync(
      process.execPath,
      [
        join(workspaceRoot, "node_modules", "@electron", "rebuild", "lib", "cli.js"),
        "-w", "node-pty",
        "-m", pluginDir,
      ],
      {
        cwd: workspaceRoot,
        env: { ...process.env },
      },
    );
    console.log("[stage-terminal-native] electron-rebuild complete");
  } catch (err) {
    console.error("[stage-terminal-native] electron-rebuild failed:", err);
    throw err;
  }
}

main().catch((err) => {
  console.error("[stage-terminal-native] fatal:", err);
  process.exit(1);
});
