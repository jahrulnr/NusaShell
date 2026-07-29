import { clipboard, contextBridge, ipcRenderer } from "electron";
import type { PublicAiRegistry, ReasoningEffort, SaveAiProviderInput } from "../shared/ai-contract.js";
import type {
  AgentConversation,
  AgentConversationCheckpoint,
  AgentConversationMessage,
  AgentConversationSummary,
} from "../shared/agent-conversation-contract.js";

export interface ShellApi {
  readonly wsUrl: string;
  callTool(pluginId: string, toolName: string, args: Record<string, unknown>): Promise<unknown>;
  listTools(pluginId: string): Promise<unknown>;
  openPlugin(pluginId: string, name: string, icon: string, installPath: string, windowMode?: string): Promise<void>;
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
    updateRuntime(input: Pick<PublicAiRegistry, "strategy" | "totalAttemptBudget" | "stream" | "vision">): Promise<PublicAiRegistry>;
  };
  readonly agentConversations: {
    list(): Promise<readonly AgentConversationSummary[]>;
    create(): Promise<AgentConversation>;
    get(id: string): Promise<AgentConversation | null>;
    append(id: string, message: AgentConversationMessage): Promise<AgentConversation>;
    saveCheckpoint(id: string, checkpoint: AgentConversationCheckpoint): Promise<AgentConversation>;
    delete(id: string): Promise<void>;
  };
}

export type ShellLogLevel = "debug" | "info" | "warn" | "error";

export interface ShellLogEntry {
  readonly id: number;
  readonly timestamp: string;
  readonly source: "backend" | "ipc" | "main" | "mcp" | "renderer";
  readonly level: ShellLogLevel;
  readonly message: string;
}

const wsUrl = `ws://127.0.0.1:${process.env.NUSASHELL_PORT ?? "9130"}`;

const api: ShellApi = {
  wsUrl,
  callTool(pluginId, toolName, args) {
    return ipcRenderer.invoke("tool:call", pluginId, toolName, args);
  },
  listTools(pluginId) {
    return ipcRenderer.invoke("tool:list", pluginId);
  },
  openPlugin(pluginId, name, icon, installPath, windowMode) {
    return ipcRenderer.invoke("window:open-plugin", pluginId, name, icon, installPath, windowMode);
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
    create: () => ipcRenderer.invoke("agent-conversations:create"),
    get: (id) => ipcRenderer.invoke("agent-conversations:get", id),
    append: (id, message) => ipcRenderer.invoke("agent-conversations:append", id, message),
    saveCheckpoint: (id, checkpoint) => ipcRenderer.invoke("agent-conversations:checkpoint", id, checkpoint),
    delete: (id) => ipcRenderer.invoke("agent-conversations:delete", id),
  },
};

contextBridge.exposeInMainWorld("shell", api);
