import { ipcMain } from "electron";
import { mkdir, readFile, writeFile } from "node:fs/promises";
import { relative, resolve } from "node:path";
import { ManifestSchema } from "@nusashell/contracts";
import type { IpcContext } from "./ipc-context.js";

export interface NativeMcpInput {
  readonly id: string;
  readonly name: string;
  readonly icon?: string;
  readonly transport: "stdio" | "http" | "sse";
  readonly command?: string;
  readonly args?: readonly string[];
  readonly url?: string;
  readonly env?: Readonly<Record<string, string>>;
  readonly headers?: Readonly<Record<string, string>>;
  readonly autostart?: boolean;
}

export function registerNativeMcpIpc(ctx: IpcContext): void {
  ipcMain.handle("plugins:register-native-mcp", async (_event, input: NativeMcpInput) => {
    const result = await writeNativeMcp(ctx, input, undefined);
    return result;
  });

  ipcMain.handle("plugins:update-native-mcp", async (_event, pluginId: string, input: NativeMcpInput) => {
    return writeNativeMcp(ctx, input, pluginId);
  });
}

async function writeNativeMcp(ctx: IpcContext, input: NativeMcpInput, updateId?: string): Promise<unknown> {
  const root = ctx.getBackend().config.userPluginsRoot;
  if (!root) throw new Error("Writable user plugin root is not configured");
  const id = input.id.trim();
  if (updateId && updateId !== id) throw new Error("Native MCP id cannot change during edit");
  if (!/^[a-z][a-z0-9-]*\.[a-z][a-z0-9-]*$/.test(id)) {
    throw new Error("id must follow publisher.name format");
  }
  const folder = resolve(root, id);
  const rootPath = resolve(root);
  if (!isDirectChild(rootPath, folder)) throw new Error("Native MCP id resolves outside the user plugins root");
  if (updateId) {
    const current = JSON.parse(await readFile(resolve(folder, "manifest.json"), "utf8")) as { source?: string };
    if (current.source !== "native-mcp") throw new Error("Only native MCP plugins can be edited");
    await ctx.commandBus.execute({ kind: "stop-plugin", pluginId: id }).catch(() => {});
  }
  const manifest = ManifestSchema.parse({
    source: "native-mcp",
    id,
    name: input.name.trim(),
    version: "0.1.0",
    icon: input.icon?.trim() || "M",
    mcp: {
      transport: input.transport,
      ...(input.command ? { command: input.command.trim() } : {}),
      ...(input.args ? { args: [...input.args] } : {}),
      ...(input.url ? { url: input.url.trim() } : {}),
      ...(input.env ? { env: input.env } : {}),
      ...(input.headers ? { headers: input.headers } : {}),
      autostart: Boolean(input.autostart),
      keepAliveOnClose: false,
    },
  });
  await mkdir(folder, { recursive: true });
  await writeFile(resolve(folder, "manifest.json"), `${JSON.stringify(manifest, null, 2)}\n`, "utf8");
  await ctx.syncPlugins();
  return { ok: true, pluginId: id, installPath: folder, restartRequired: Boolean(updateId) };
}

function isDirectChild(root: string, candidate: string): boolean {
  const child = relative(root, candidate);
  return child.length > 0 && !child.startsWith("..") && !child.includes("/") && !child.includes("\\");
}
