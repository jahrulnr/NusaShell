import { clipboard, contextBridge, ipcRenderer } from "electron";
import type { PublicAiRegistry, ReasoningEffort, SaveAiProviderInput } from "../shared/ai-contract.js";
import { checkEventSkew } from "../shared/event-skew-checker.js";
import type {
  AgentCanvasArtifact,
  AgentConversation,
  AgentConversationCheckpoint,
  AgentConversationMessage,
  AgentConversationModelBinding,
  AgentConversationSummary,
  AgentSubagentRun,
  AgentSubagentRunStatus,
  AgentSubagentStreamStep,
} from "../shared/agent-conversation-contract.js";
import type {
  AcpModelOption,
  AcpProviderPublic,
  AcpProviderSaveInput,
  AcpRoutingPublic,
  AcpRoutingSettings,
} from "../shared/acp-provider-contract.js";
import type {
  SkillDetail,
  SkillReadResult,
  SkillSummary,
  LearningGraph,
  LearningNodeDetail,
  MutationResult,
} from "@nusashell/application";
import type { PendingSkillWrite } from "@nusashell/contracts";
import type {
  PublicMailSettings,
  SaveMailAccountInput,
} from "../shared/mail-contract.js";
import type { PluginWindowOptionsInput } from "../main/plugin-window-options.js";
import type { NativeMcpInput } from "../main/ipc/native-mcp.js";
import { resolveBuildLabel } from "../main/runtime-mode.js";

export interface ShellApi {
  readonly build: "dev" | "production";
  callTool(pluginId: string, toolName: string, args: Record<string, unknown>): Promise<unknown>;
  listTools(pluginId: string): Promise<unknown>;
  openPlugin(pluginId: string, name: string, icon: string, installPath: string, options?: PluginWindowOptionsInput): Promise<void>;
  closePlugin(pluginId: string): Promise<void>;
  readonly windowControls: {
    minimize(): Promise<void>;
    toggleMaximize(): Promise<boolean>;
    toggleAlwaysOnTop(): Promise<boolean>;
    close(): Promise<void>;
  };
  readonly shellControls: {
    openDocs(): Promise<void>;
    openExternal(url: string): Promise<void>;
    pickPluginSource(kind: "directory" | "archive"): Promise<string | null>;
  };
  readonly pluginIcons: {
    read(source: string, installPath: string): Promise<string>;
  };
  readonly plugins: {
    registerNativeMcp(input: NativeMcpInput): Promise<unknown>;
    updateNativeMcp(pluginId: string, input: NativeMcpInput): Promise<unknown>;
  };
  readonly clipboard: {
    readText(): string;
    writeText(value: string): void;
  };
  readonly logs: {
    list(): Promise<readonly ShellLogEntry[]>;
    write(level: ShellLogLevel, message: string): void;
    onEntry(callback: (entry: ShellLogEntry) => void): () => void;
  };
  readonly aiProviders: {
    list(): Promise<PublicAiRegistry>;
    save(input: SaveAiProviderInput): Promise<PublicAiRegistry>;
    delete(providerId: string): Promise<PublicAiRegistry>;
    importModels(providerId: string): Promise<PublicAiRegistry>;
    addModel(providerId: string, model: { id: string; label: string }): Promise<PublicAiRegistry>;
    select(input: { modelKey?: string; effort?: ReasoningEffort }): Promise<PublicAiRegistry>;
    updateRuntime(input: Pick<PublicAiRegistry, "strategy" | "totalAttemptBudget" | "stream" | "vision" | "userPrompt" | "maxToolRounds" | "maxRepeatedToolCalls" | "compactionEnabled" | "maxInputTokens" | "reserveTokens" | "recentTurns" | "summaryMaxChars">): Promise<PublicAiRegistry>;
  };
  readonly agentConversations: {
    list(): Promise<readonly AgentConversationSummary[]>;
    create(options?: { kind?: "agent" | "acp"; acp?: { providerId: string; sessionId?: string; workspace?: string } }): Promise<AgentConversation>;
    get(id: string): Promise<AgentConversation | null>;
    append(id: string, message: AgentConversationMessage): Promise<AgentConversation>;
    saveCheckpoint(id: string, checkpoint: AgentConversationCheckpoint): Promise<AgentConversation>;
    replaceLastInterrupted(id: string, message: AgentConversationMessage): Promise<AgentConversation>;
    delete(id: string): Promise<void>;
    setWorkspace(id: string, workspace: string): Promise<AgentConversation>;
    setModel(id: string, model: AgentConversationModelBinding | null): Promise<AgentConversation>;
    upsertCanvasArtifact(id: string, artifact: AgentCanvasArtifact): Promise<AgentConversation>;
    setActiveCanvasArtifact(id: string, artifactId: string | null): Promise<AgentConversation>;
    upsertSubagentRun(id: string, run: AgentSubagentRun): Promise<AgentConversation>;
    setActiveSubagentRun(id: string, runId: string | null): Promise<AgentConversation>;
    updateSubagentRunStatus(id: string, runId: string, status: AgentSubagentRunStatus, patch?: { summary?: string; error?: string; steps?: readonly AgentSubagentStreamStep[] }): Promise<AgentConversation>;
  };
  readonly acpProviders: {
    list(): Promise<readonly AcpProviderPublic[]>;
    save(input: AcpProviderSaveInput): Promise<readonly AcpProviderPublic[]>;
    get(providerId: string): Promise<AcpProviderPublic | null>;
    probe(providerId: string, options?: { interactive?: boolean }): Promise<AcpProviderPublic | null>;
    getRouting(): Promise<AcpRoutingPublic>;
    saveRouting(settings: AcpRoutingSettings): Promise<AcpRoutingPublic>;
    importModels(providerId: string): Promise<{ models: AcpModelOption[]; error?: string }>;
    setDefaultModel(providerId: string, modelId: string): Promise<readonly AcpProviderPublic[]>;
    setDefaultMode(providerId: string, mode: string): Promise<readonly AcpProviderPublic[]>;
  };
  readonly skills: {
    list(): Promise<readonly SkillSummary[]>;
    get(skillId: string): Promise<SkillDetail>;
    read(skillId: string, path?: string): Promise<SkillReadResult>;
    install(): Promise<SkillDetail | null>;
    write(skillId: string, path: string, content: string): Promise<SkillReadResult>;
    delete(skillId: string): Promise<void>;
    pendingList(): Promise<readonly PendingSkillWrite[]>;
    pendingApprove(id: string): Promise<unknown>;
    pendingReject(id: string): Promise<void>;
    curatorStatus(): Promise<unknown>;
    curatorRun(dryRun: boolean): Promise<unknown>;
    curatorConfigure(settings: Record<string, unknown>): Promise<unknown>;
    pin(skillId: string, pinned: boolean): Promise<{ ok: boolean }>;
    restore(skillId: string): Promise<{ ok: boolean }>;
    archivedList(): Promise<readonly unknown[]>;
  };
  readonly learning: {
    graph(): Promise<LearningGraph>;
    getNode(nodeId: string): Promise<LearningNodeDetail>;
    editNode(nodeId: string, content: string): Promise<MutationResult>;
    deleteNode(nodeId: string): Promise<MutationResult>;
  };
  readonly backgroundReview: {
    configure(settings: Record<string, unknown>): Promise<{ ok: boolean }>;
    settings(): Promise<Record<string, unknown>>;
  };
  readonly appBehavior: {
    get(): Promise<AppBehaviorPublic>;
    set(patch: AppBehaviorPatch): Promise<AppBehaviorPublic>;
  };
  readonly mailAccounts: {
    list(): Promise<PublicMailSettings>;
    save(input: SaveMailAccountInput): Promise<PublicMailSettings>;
    delete(accountId: string): Promise<PublicMailSettings>;
  };
  /**
   * Generic host RPC bridge (Phase 1 of desktop-inprocess-ipc-plan).
   * Replaces the loopback WebSocket for renderer → backend communication.
   * Method strings and payloads match the former WS contract.
   */
  readonly backend: {
    request(method: string, payload?: unknown, opts?: { timeoutMs?: number }): Promise<unknown>;
    onEvent(eventType: string, handler: (payload: unknown, sequence?: number) => void): () => void;
    whenReady(): Promise<void>;
  };
}

export interface AppBehaviorPublic {
  readonly launchAtLogin: boolean;
  readonly startHidden: boolean;
  readonly keepInBackground: boolean;
  readonly canvasEnabled: boolean;
  readonly canSetLoginAutostart: boolean;
}

export type AppBehaviorPatch = Partial<Pick<AppBehaviorPublic, "launchAtLogin" | "startHidden" | "keepInBackground">>;

export type ShellLogLevel = "debug" | "info" | "warn" | "error";

export interface ShellLogEntry {
  readonly id: number;
  readonly timestamp: string;
  readonly source: "backend" | "ipc" | "main" | "mcp" | "renderer";
  readonly level: ShellLogLevel;
  readonly message: string;
}

const isDev = process.env.NUSASHELL_IS_DEV === "true";

let lastSkewWarnAt = 0;

const api: ShellApi = {
  build: resolveBuildLabel(isDev),
  callTool(pluginId, toolName, args) {
    return ipcRenderer.invoke("tool:call", pluginId, toolName, args);
  },
  listTools(pluginId) {
    return ipcRenderer.invoke("tool:list", pluginId);
  },
  openPlugin(pluginId, name, icon, installPath, options) {
    return ipcRenderer.invoke("window:open-plugin", pluginId, name, icon, installPath, options);
  },
  closePlugin(pluginId) {
    return ipcRenderer.invoke("window:close-plugin", pluginId);
  },
  windowControls: {
    minimize() {
      return ipcRenderer.invoke("window:minimize");
    },
    toggleMaximize() {
      return ipcRenderer.invoke("window:toggle-maximize");
    },
    toggleAlwaysOnTop() {
      return ipcRenderer.invoke("window:toggle-always-on-top");
    },
    close() {
      return ipcRenderer.invoke("window:close");
    },
  },
  shellControls: {
    openDocs() {
      return ipcRenderer.invoke("shell:open-docs");
    },
    openExternal(url) {
      return ipcRenderer.invoke("shell:open-external", url);
    },
    pickPluginSource(kind) {
      return ipcRenderer.invoke("shell:pick-plugin-source", kind);
    },
  },
  pluginIcons: {
    read(source, installPath) {
      return ipcRenderer.invoke("plugin-icons:read", source, installPath);
    },
  },
  plugins: {
    registerNativeMcp(input) {
      return ipcRenderer.invoke("plugins:register-native-mcp", input);
    },
    updateNativeMcp(pluginId, input) {
      return ipcRenderer.invoke("plugins:update-native-mcp", pluginId, input);
    },
  },
  clipboard: {
    readText() {
      return clipboard.readText();
    },
    writeText(value) {
      clipboard.writeText(value);
    },
  },
  logs: {
    list() {
      return ipcRenderer.invoke("logs:list");
    },
    write(level, message) {
      ipcRenderer.send("logs:write", level, message);
    },
    onEntry(callback) {
      const listener = (_event: Electron.IpcRendererEvent, entry: ShellLogEntry) => callback(entry);
      ipcRenderer.on("logs:entry", listener);
      return () => ipcRenderer.removeListener("logs:entry", listener);
    },
  },
  aiProviders: {
    list: () => ipcRenderer.invoke("ai-providers:list"),
    save: (input) => ipcRenderer.invoke("ai-providers:save", input),
    delete: (providerId) => ipcRenderer.invoke("ai-providers:delete", providerId),
    importModels: (providerId) => ipcRenderer.invoke("ai-providers:import-models", providerId),
    addModel: (providerId, model) => ipcRenderer.invoke("ai-providers:add-model", providerId, model),
    select: (input) => ipcRenderer.invoke("ai-providers:select", input),
    updateRuntime: (input) => ipcRenderer.invoke("ai-providers:update-runtime", input),
  },
  agentConversations: {
    list: () => ipcRenderer.invoke("agent-conversations:list"),
    create: (options) => ipcRenderer.invoke("agent-conversations:create", options),
    get: (id) => ipcRenderer.invoke("agent-conversations:get", id),
    append: (id, message) => ipcRenderer.invoke("agent-conversations:append", id, message),
    saveCheckpoint: (id, checkpoint) => ipcRenderer.invoke("agent-conversations:checkpoint", id, checkpoint),
    replaceLastInterrupted: (id, message) => ipcRenderer.invoke("agent-conversations:replace-interrupted", id, message),
    delete: (id) => ipcRenderer.invoke("agent-conversations:delete", id),
    setWorkspace: (id, workspace) => ipcRenderer.invoke("agent-conversations:set-workspace", id, workspace),
    setModel: (id, model) => ipcRenderer.invoke("agent-conversations:set-model", id, model),
    upsertCanvasArtifact: (id, artifact) => ipcRenderer.invoke("agent-conversations:upsert-canvas-artifact", id, artifact),
    setActiveCanvasArtifact: (id, artifactId) => ipcRenderer.invoke("agent-conversations:set-active-canvas-artifact", id, artifactId),
    upsertSubagentRun: (id, run) => ipcRenderer.invoke("agent-conversations:upsert-subagent-run", id, run),
    setActiveSubagentRun: (id, runId) => ipcRenderer.invoke("agent-conversations:set-active-subagent-run", id, runId),
    updateSubagentRunStatus: (id, runId, status, patch) => ipcRenderer.invoke("agent-conversations:update-subagent-run-status", id, runId, status, patch),
  },
  acpProviders: {
    list: () => ipcRenderer.invoke("acp-providers:list"),
    save: (input) => ipcRenderer.invoke("acp-providers:save", input),
    get: (providerId) => ipcRenderer.invoke("acp-providers:get", providerId),
    probe: (providerId, options) => ipcRenderer.invoke("acp-providers:probe", providerId, options),
    getRouting: () => ipcRenderer.invoke("acp-providers:get-routing"),
    saveRouting: (settings) => ipcRenderer.invoke("acp-providers:save-routing", settings),
    importModels: (providerId) => ipcRenderer.invoke("acp-providers:import-models", providerId),
    setDefaultModel: (providerId, modelId) => ipcRenderer.invoke("acp-providers:set-default-model", providerId, modelId),
    setDefaultMode: (providerId, mode) => ipcRenderer.invoke("acp-providers:set-default-mode", providerId, mode),
  },
  skills: {
    list: () => ipcRenderer.invoke("skills:list"),
    get: (skillId) => ipcRenderer.invoke("skills:get", skillId),
    read: (skillId, path) => ipcRenderer.invoke("skills:read", skillId, path),
    install: () => ipcRenderer.invoke("skills:install"),
    write: (skillId, path, content) => ipcRenderer.invoke("skills:write", skillId, path, content),
    delete: (skillId) => ipcRenderer.invoke("skills:delete", skillId),
    pendingList: () => ipcRenderer.invoke("skills:pending:list"),
    pendingApprove: (id) => ipcRenderer.invoke("skills:pending:approve", id),
    pendingReject: (id) => ipcRenderer.invoke("skills:pending:reject", id),
    curatorStatus: () => ipcRenderer.invoke("skills:curator:status"),
    curatorRun: (dryRun) => ipcRenderer.invoke("skills:curator:run", dryRun),
    curatorConfigure: (settings) => ipcRenderer.invoke("skills:curator:configure", settings),
    pin: (skillId, pinned) => ipcRenderer.invoke("skills:pin", skillId, pinned),
    restore: (skillId) => ipcRenderer.invoke("skills:restore", skillId),
    archivedList: () => ipcRenderer.invoke("skills:archived:list"),
  },
  learning: {
    graph: () => ipcRenderer.invoke("learning:graph"),
    getNode: (nodeId) => ipcRenderer.invoke("learning:node:get", nodeId),
    editNode: (nodeId, content) => ipcRenderer.invoke("learning:node:edit", nodeId, content),
    deleteNode: (nodeId) => ipcRenderer.invoke("learning:node:delete", nodeId),
  },
  backgroundReview: {
    configure: (settings) => ipcRenderer.invoke("background-review:configure", settings),
    settings: () => ipcRenderer.invoke("background-review:settings"),
  },
  appBehavior: {
    get: () => ipcRenderer.invoke("app-behavior:get"),
    set: (patch) => ipcRenderer.invoke("app-behavior:set", patch),
  },
  mailAccounts: {
    list: () => ipcRenderer.invoke("mail-accounts:list"),
    save: (input) => ipcRenderer.invoke("mail-accounts:save", input),
    delete: (accountId) => ipcRenderer.invoke("mail-accounts:delete", accountId),
  },
  backend: {
    request(method, payload, opts) {
      return ipcRenderer.invoke("shell:request", method, payload, opts);
    },
    onEvent(eventType, handler) {
      const listener = (_event: Electron.IpcRendererEvent, frame: { event: string; payload: unknown; sequence: number; emittedAt?: number }) => {
        const skew = checkEventSkew(frame, {
          now: Date.now(),
          warn: (msg) => console.warn(msg),
          lastWarnAt: lastSkewWarnAt,
        });
        lastSkewWarnAt = skew.lastWarnAt;
        if (frame.event === eventType) handler(frame.payload, frame.sequence);
      };
      ipcRenderer.on("shell:event", listener);
      return () => ipcRenderer.removeListener("shell:event", listener);
    },
    whenReady() {
      // Main is in-process; ready when preload is loaded. Resolve immediately.
      return Promise.resolve();
    },
  },
};

contextBridge.exposeInMainWorld("shell", api);
