import type {
  PluginId,
  PluginRuntimeState,
} from "@nusashell/domain";
import type {
  McpClientPort,
} from "../ports/mcp-client.port.js";
import type { ProcessHandle } from "../ports/plugin-process.port.js";
import type { PluginOperationQueue } from "./plugin-operation-queue.js";
import type { PluginRuntimeManagerDeps } from "./plugin-runtime-manager.js";

export interface PendingToolCall {
  readonly toolCall: import("@nusashell/domain").ToolCall;
  readonly resolve: (value: unknown) => void;
  readonly reject: (error: unknown) => void;
  readonly timer: ReturnType<typeof setTimeout> | undefined;
}

export interface RuntimeEntry {
  readonly pluginId: PluginId;
  name: string;
  version: string;
  icon: string;
  installPath: string;
  source?: "native-mcp" | "package";
  transport?: string;
  category?: string;
  command: string | undefined;
  args: readonly string[];
  url: string | undefined;
  env: Readonly<Record<string, string>>;
  headers: Readonly<Record<string, string>>;
  enabled: boolean;
  autostart: boolean;
  ui?: {
    readonly entry: string;
    readonly window?: {
      readonly mode?: "panel" | "fullscreen" | "widget";
      readonly defaultSize?: { readonly width: number; readonly height: number };
      readonly resizable?: boolean;
    };
  } | undefined;
  keepAliveOnClose: boolean;
  runtime: import("@nusashell/domain").PluginRuntime;
  startPromise: Promise<void> | null;
  /** Set when stop interrupts an in-flight start so doStart skips crash. */
  startAborted: boolean;
  readonly queue: PluginOperationQueue;
  process: ProcessHandle | null;
  mcpClient: McpClientPort | null;
  readonly pendingCalls: Map<string, PendingToolCall>;
  restartCount: number;
  /** True while a scheduled auto-restart is waiting or running. */
  restarting: boolean;
  /** Ms timestamp of the first crash in the current circuit window. */
  restartWindowStartAt: number;
  /** Pending auto-restart timer (cancelled on stop). */
  restartTimer: ReturnType<typeof setTimeout> | null;
  /** Reason of the most recent crash (surfaced in PluginView). */
  lastCrashReason: string | undefined;
  /**
   * Cached tool catalog (populated lazily on first tools/list after start;
   * null = not cached yet). Invalidated on start/restart/stop/crash and on
   * MCP `notifications/tools/list_changed`.
   */
  cachedTools: readonly import("../ports/mcp-client.port.js").ToolDescriptor[] | null;
  /** Last workspace bound to this plugin (for roots sync / spawn env). */
  workspace: string | undefined;
  /** Last workspace reported to the client via roots (to detect change). */
  lastRootsWorkspace: string | undefined;
  /** Agent-supplied launch overrides (Phase 3). command is always immutable. */
  launchArgs: readonly string[] | undefined;
  launchEnv: Readonly<Record<string, string>> | undefined;
  /** Launch spec the currently-running process was started with (for respawn detection). */
  runningArgs: readonly string[] | undefined;
  runningEnv: Readonly<Record<string, string>> | undefined;
  /** Automation config from the plugin manifest (emits + poll). */
  automation: import("@nusashell/domain").AutomationConfig | undefined;
}

export type { PluginRuntimeManagerDeps };

export interface StartPluginOptions {
  /**
   * Agent-supplied launch overrides (Phase 3). `command` is always immutable;
   * only `args` and `env` may be patched. A different launchSpec while the
   * plugin is running triggers a stop+start respawn.
   */
  readonly args?: readonly string[];
  readonly env?: Readonly<Record<string, string>>;
  /** Conversation workspace to bind at spawn (sets NUSASHELL_WORKSPACE env). */
  readonly workspace?: string;
}

export interface WorkspaceSyncResult {
  readonly mode: "roots" | "static" | "idle";
  readonly respawned: boolean;
}

export interface PluginLaunchSpec {
  readonly pluginId: string;
  readonly source?: "native-mcp" | "package";
  readonly transport?: string;
  readonly category?: string;
  readonly command?: string;
  readonly url?: string;
  readonly headerKeys?: readonly string[];
  readonly args: readonly string[];
  /** Env keys only — values are redacted (secrets may live in env). */
  readonly envKeys: readonly string[];
  readonly workspace?: string;
  readonly rootsCapable: boolean;
}

export interface CallToolOptions {
  readonly requestId: string;
  readonly toolName: string;
  readonly args: Readonly<Record<string, unknown>>;
  readonly timeoutMs?: number;
  /** When provided, the call rejects with TOOL_CALL_CANCELLED if the signal aborts. */
  readonly signal?: AbortSignal;
  /** Progress notification callback (MCP `notifications/progress`). */
  readonly onProgress?: (progress: { progress: number; total?: number | undefined; message?: string | undefined }) => void;
}

export interface PluginView {
  readonly pluginId: string;
  readonly name: string;
  readonly version: string;
  readonly icon: string;
  readonly installPath: string;
  readonly state: PluginRuntimeState;
  readonly enabled: boolean;
  readonly autostart: boolean;
  readonly restarting?: boolean;
  readonly restartCount?: number;
  readonly lastCrashReason?: string;
  readonly ui: RuntimeEntry["ui"];
  readonly source?: "native-mcp" | "package";
  readonly transport?: string;
  readonly category?: string;
  readonly keepAliveOnClose: boolean;
  readonly command?: string;
  readonly args?: readonly string[];
  readonly url?: string;
  readonly env?: Readonly<Record<string, string>>;
  readonly headers?: Readonly<Record<string, string>>;
  readonly automation?: RuntimeEntry["automation"];
}

/**
 * Refresh RuntimeEntry manifest/display fields from the repository plugin.
 * Runtime state, process, and mcpClient stay the live SoT.
 */
export function hydrateEntryFromPlugin(
  entry: RuntimeEntry,
  plugin: import("@nusashell/domain").Plugin,
  resolveIcon: (icon: string, installPath: string) => string,
): void {
  entry.name = plugin.manifest.name;
  entry.version = plugin.manifest.version.toString();
  entry.icon = resolveIcon(plugin.manifest.icon, plugin.installPath);
  entry.installPath = plugin.installPath;
  entry.source = plugin.manifest.source;
  entry.transport = plugin.manifest.mcp.transport;
  if (plugin.manifest.category !== undefined) {
    entry.category = plugin.manifest.category;
  } else {
    delete entry.category;
  }
  entry.command = plugin.manifest.mcp.command;
  entry.args = plugin.manifest.mcp.args;
  entry.url = plugin.manifest.mcp.url;
  entry.env = plugin.manifest.mcp.env;
  entry.headers = plugin.manifest.mcp.headers;
  entry.enabled = plugin.enabled;
  entry.autostart = plugin.manifest.mcp.autostart;
  entry.ui = plugin.manifest.ui;
  entry.keepAliveOnClose = plugin.manifest.mcp.keepAliveOnClose;
  entry.automation = plugin.manifest.automation;
}

export function arrayEquals(a: readonly string[], b: readonly string[]): boolean {
  if (a.length !== b.length) return false;
  for (let i = 0; i < a.length; i++) {
    if (a[i] !== b[i]) return false;
  }
  return true;
}

export function recordEquals(
  a: Readonly<Record<string, string>>,
  b: Readonly<Record<string, string>>,
): boolean {
  const ak = Object.keys(a);
  const bk = Object.keys(b);
  if (ak.length !== bk.length) return false;
  for (const k of ak) {
    if (a[k] !== b[k]) return false;
  }
  return true;
}
