import { clipboard, contextBridge, ipcRenderer } from "electron";
import type { PublicAiRegistry, ReasoningEffort, SaveAiProviderInput } from "../shared/ai-contract.js";
import type {
  AgentConversation,
  AgentConversationCheckpoint,
  AgentConversationMessage,
  AgentConversationSummary,
} from "../shared/agent-conversation-contract.js";
import type {
  AcpProviderPublic,
  AcpProviderSaveInput,
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
import { resolveBuildLabel, resolveWsPort } from "../main/runtime-mode.js";

export interface ShellApi {
  readonly wsUrl: string;
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
  };
  readonly acpProviders: {
    list(): Promise<readonly AcpProviderPublic[]>;
    save(input: AcpProviderSaveInput): Promise<readonly AcpProviderPublic[]>;
    get(providerId: string): Promise<AcpProviderPublic | null>;
    probe(providerId: string): Promise<AcpProviderPublic | null>;
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
}

export interface AppBehaviorPublic {
  readonly launchAtLogin: boolean;
  readonly startHidden: boolean;
  readonly keepInBackground: boolean;
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
const wsUrl = `ws://127.0.0.1:${resolveWsPort({
  isDev,
  envPort: process.env.NUSASHELL_PORT,
})}`;

const api: ShellApi = {
  wsUrl,
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
  },
  acpProviders: {
    list: () => ipcRenderer.invoke("acp-providers:list"),
    save: (input) => ipcRenderer.invoke("acp-providers:save", input),
    get: (providerId) => ipcRenderer.invoke("acp-providers:get", providerId),
    probe: (providerId) => ipcRenderer.invoke("acp-providers:probe", providerId),
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
};

contextBridge.exposeInMainWorld("shell", api);
