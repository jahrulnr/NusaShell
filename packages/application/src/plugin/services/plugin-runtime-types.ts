import type {
  PluginId,
  PluginRuntimeState,
} from "@nusashell/domain";
import type {
  McpClientPort,
  CompletionReference,
  CompletionResult,
  PromptDescriptor,
  PromptResult,
  ResourceDescriptor,
  ResourceReadResult,
  ResourceTemplateDescriptor,
  RootDescriptor,
  ToolDescriptor,
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
  readonly queue: PluginOperationQueue;
  process: ProcessHandle | null;
  mcpClient: McpClientPort | null;
  readonly pendingCalls: Map<string, PendingToolCall>;
  restartCount: number;
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
  readonly transport: string;
  readonly command?: string;
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
  readonly ui: RuntimeEntry["ui"];
  readonly keepAliveOnClose: boolean;
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
