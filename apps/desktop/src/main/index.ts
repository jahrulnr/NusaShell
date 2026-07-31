import { app, BrowserWindow, dialog, ipcMain, Menu, shell, type IpcMainInvokeEvent, type OpenDialogOptions } from "electron";
import { resolve } from "node:path";
import { randomUUID } from "node:crypto";
import { bootstrap, type BootstrapResult } from "@nusashell/backend";
import { LogTail, type ShellLogLevel, type ShellLogSource } from "./log-tail.js";
import {
  createLauncherWindow,
  closeAllPluginWindows,
  closePluginWindow,
  getLauncherWindow,
  isPluginWindowSender,
  registerWindowIpc,
  setLauncherClosePolicy,
  showLauncherWindow,
  toggleLauncherWindow,
} from "./window-manager.js";
import { LINUX_DESKTOP_APP_NAME } from "./window-assets.js";
import { AppUpdater } from "./updater.js";
import { loadConfig, type CallToolCommand, type ListToolsQuery, type StartPluginCommand } from "@nusashell/application";
import { AiSettingsStore, type AiRegistrySettings, type SaveAiProviderInput } from "./ai-settings.js";
import { AcpProviderStore } from "./acp-provider-store.js";
import type { AcpProviderSaveInput } from "../shared/acp-provider-contract.js";
import { flattenModelCatalog } from "./ai-provider-registry.js";
import { AgentConversationStore } from "./agent-conversation-store.js";
import type {
  AgentConversationCheckpoint,
  AgentConversationMessage,
} from "../shared/agent-conversation-contract.js";
import { MailSettingsStore } from "./mail-settings.js";
import type { SaveMailAccountInput } from "../shared/mail-contract.js";
import { loadPluginPngDataUrl } from "./plugin-icon.js";
import {
  AppBehaviorStore,
  shouldHideOnClose,
  shouldQuitOnAllWindowsClosed,
  type AppBehaviorPatch,
  type AppBehaviorSettings,
} from "./app-behavior-settings.js";
import { createLoginAutostart, type LoginAutostart } from "./login-autostart.js";
import { TrayManager } from "./tray.js";

let backend: BootstrapResult | null = null;
let updater: AppUpdater | null = null;
let aiSettingsStore: AiSettingsStore | null = null;
let aiSettings: AiRegistrySettings | null = null;
let agentConversationStore: AgentConversationStore | null = null;
let mailSettingsStore: MailSettingsStore | null = null;
let acpProviderStore: AcpProviderStore | null = null;
let appBehaviorStore: AppBehaviorStore | null = null;
let appBehavior: AppBehaviorSettings | null = null;
let loginAutostart: LoginAutostart | null = null;
let trayManager: TrayManager | null = null;
let isQuitting = false;
const isDev = process.argv.includes("--dev");
const startHidden = process.argv.includes("--hidden") || process.argv.includes("--background");
const logTail = new LogTail(1000);
const shellLogLevels = new Set<ShellLogLevel>(["debug", "info", "warn", "error"]);
const aiRuntimeConfig = loadConfig().ai;
const aiStubEnabled = aiRuntimeConfig.stubEnabled;
const DOCS_URL = "https://github.com/jahrulnr/NusaShell/tree/master/docs";
const MAIL_PLUGIN_ID = "nusashell.mail";

const gotSingleInstanceLock = app.requestSingleInstanceLock();
if (!gotSingleInstanceLock) {
  app.quit();
} else {
  app.on("second-instance", () => {
    showLauncherWindow();
  });
}

if (process.platform === "linux") {
  app.setName(LINUX_DESKTOP_APP_NAME);
}

function redactLogMessage(message: string): string {
  return message
    .replace(/([?&](?:token|password|secret|api[_-]?key|authorization)=)[^&\s]+/gi, "$1[REDACTED]")
    .replace(/((?:token|password|secret|api[_-]?key|authorization)["']?\s*[:=]\s*["']?)[^,\s}"']+/gi, "$1[REDACTED]");
}

function formatLogArguments(args: readonly unknown[]): string {
  const message = args.map((arg) => {
    if (arg instanceof Error) return arg.stack ?? arg.message;
    if (typeof arg === "string") return arg;
    try {
      return JSON.stringify(arg);
    } catch {
      return String(arg);
    }
  }).join(" ");
  return redactLogMessage(message);
}

function toShellLogLevel(level: string): ShellLogLevel {
  if (level === "error" || level === "fatal") return "error";
  if (level === "warn") return "warn";
  if (level === "debug" || level === "trace") return "debug";
  return "info";
}

function captureMainConsole(): void {
  for (const level of ["debug", "info", "warn", "error"] as const) {
    const original = console[level].bind(console);
    console[level] = (...args: unknown[]) => {
      logTail.add("main", level, formatLogArguments(args));
      original(...args);
    };
  }
}

captureMainConsole();

if (isDev) {
  app.commandLine.appendSwitch("no-sandbox");
}

function getRuntimeRoot(): string {
  return app.isPackaged ? process.resourcesPath : resolve(__dirname, "..", "..", "..", "..");
}

function getDataRoot(): string {
  return app.isPackaged ? app.getPath("userData") : resolve(__dirname, "..", "..", "..", "..");
}

async function startBackend(): Promise<BootstrapResult> {
  const runtimeRoot = getRuntimeRoot();
  const dataRoot = getDataRoot();
  const pluginsRoot = app.isPackaged
    ? resolve(process.resourcesPath, "plugins")
    : resolve(__dirname, "..", "..", "..", "..", "plugins");
  const promptsRoot = resolve(runtimeRoot, "resources", "agent", "prompts");
  const docsRoot = resolve(runtimeRoot, "resources", "agent", "docs");
  const docsIndexStorageRoot = resolve(dataRoot, ".nusashell", "agent", "docs-index");
  const skillsRoot = resolve(app.getPath("userData"), "skills");
  const memoryRoot = resolve(app.getPath("userData"), "memories");
  mailSettingsStore ??= new MailSettingsStore(resolve(app.getPath("userData"), "mail-settings.json"));
  await mailSettingsStore.load();

  // SQLite requires better-sqlite3 native module rebuilt for Electron's ABI.
  // Until that's set up, default to filesystem registry. Set NUSASHELL_DB_PATH to opt in.
  const dbPath = process.env.NUSASHELL_DB_PATH || undefined;
  aiSettingsStore = new AiSettingsStore(
    resolve(app.getPath("userData"), "ai-settings.json"),
    resolve(app.getPath("userData"), "user-prompt.md"),
  );
  aiSettings = await aiSettingsStore.load();
  const activeProvider = aiSettings.providers.find((provider) => provider.id === aiSettings?.activeProviderId);
  const activeModel = flattenModelCatalog(aiSettings.providers).find((model) => model.key === aiSettings?.activeModelKey);
  const result = await bootstrap({
    promptsRoot,
    docsRoot,
    docsIndexStorageRoot,
    skillsRoot,
    memoryRoot,
    logFile: resolve(app.getPath("userData"), "logs", "nusashell.log"),
    resolvePluginRuntimeEnvironment: (pluginId) =>
      pluginId === MAIL_PLUGIN_ID ? mailSettingsStore?.runtimeEnvironment() ?? {} : {},
    config: { port: 9130, host: "127.0.0.1", pluginsRoot, dbPath, logLevel: isDev ? "debug" : "info", ai: {
      providerId: activeProvider?.id ?? (aiStubEnabled ? "stub" : ""),
      stubEnabled: aiStubEnabled,
      api: activeProvider?.api,
      model: activeModel?.id,
      baseUrl: activeProvider?.baseUrl || undefined,
      apiKey: activeProvider?.apiKey,
      maxToolRounds: aiSettings.maxToolRounds,
      maxRepeatedToolCalls: aiSettings.maxRepeatedToolCalls,
      strategy: aiSettings.strategy,
      totalAttemptBudget: aiSettings.totalAttemptBudget,
      stream: aiSettings.stream,
      vision: aiSettings.vision,
      userPrompt: aiSettings.userPrompt,
      timeoutMs: activeProvider?.timeoutMs ?? aiRuntimeConfig.timeoutMs,
      retry: {
        ...aiRuntimeConfig.retry,
        attemptBudget: activeProvider?.maxAttempts ?? aiRuntimeConfig.retry.attemptBudget,
      },
      context: {
        compactionEnabled: aiSettings.compactionEnabled,
        maxInputTokens: aiSettings.maxInputTokens,
        reserveTokens: aiSettings.reserveTokens,
        recentTurns: aiSettings.recentTurns,
        summaryMaxChars: aiSettings.summaryMaxChars,
      },
    } },
    loggerObserver: ({ level, args }) => {
      const message = formatLogArguments(args);
      const source: ShellLogSource = /\bmcp\b|stdio/i.test(message) ? "mcp" : "backend";
      logTail.add(source, toShellLogLevel(level), message);
    },
  });
  for (const provider of aiSettings.providers) configureProvider(result, provider);
  return result;
}

function configureProvider(target: BootstrapResult, provider: AiRegistrySettings["providers"][number]): void {
  if (!provider.enabled || !provider.baseUrl) return;
  if (!provider.apiKeyOptional && !provider.apiKey) return;
  target.container.configureAi({
    providerId: provider.id,
    api: provider.api,
    baseUrl: provider.baseUrl,
    ...(provider.apiKey ? { apiKey: provider.apiKey } : {}),
    ...(provider.defaultModel ? { model: provider.defaultModel } : {}),
    timeoutMs: provider.timeoutMs,
    maxAttempts: provider.maxAttempts,
  });
}

async function waitForBackend(port: number, maxRetries = 30): Promise<void> {
  for (let i = 0; i < maxRetries; i++) {
    try {
      const res = await fetch(`http://127.0.0.1:${port}`);
      if (res.ok || res.status === 400 || res.status === 404) return;
    } catch {
      // backend not ready yet
    }
    await new Promise(r => setTimeout(r, 100));
  }
}

app.whenReady().then(async () => {
  Menu.setApplicationMenu(null);
  agentConversationStore = new AgentConversationStore(resolve(app.getPath("userData"), "agent-conversations.json"));
  acpProviderStore = new AcpProviderStore(resolve(app.getPath("userData"), "acp-providers.json"));
  appBehaviorStore = new AppBehaviorStore(resolve(app.getPath("userData"), "app-behavior.json"));
  appBehavior = await appBehaviorStore.load();
  loginAutostart = createLoginAutostart({
    platform: process.platform,
    isPackaged: app.isPackaged,
    exePath: app.getPath("exe"),
    homeDir: app.getPath("home"),
    ...(process.env.XDG_CONFIG_HOME ? { xdgConfigHome: process.env.XDG_CONFIG_HOME } : {}),
    setLoginItemSettings: (settings) => app.setLoginItemSettings(settings),
    getLoginItemSettings: () => app.getLoginItemSettings(),
    log: (message) => logTail.add("main", "info", message),
  });
  await loginAutostart.reconcile(appBehavior);
  setLauncherClosePolicy({
    shouldHide: () => shouldHideOnClose({
      keepInBackground: appBehavior?.keepInBackground ?? true,
      isQuitting,
    }),
  });
  registerWindowIpc(
    (level, message) => logTail.add("ipc", level, message),
    async (pluginId) => {
      if (!backend) throw new Error("Backend not ready");
      const command: StartPluginCommand = { kind: "start-plugin", pluginId };
      await backend.container.commandBus.execute(command);
    },
  );
  logTail.add("main", "info", "Electron main process ready");
  logTail.add("ipc", "debug", "Shell IPC handlers registered");

  ipcMain.handle("shell:open-docs", async () => {
    await shell.openExternal(DOCS_URL);
  });
  ipcMain.handle("plugin-icons:read", (event, source: string, installPath: string) => {
    if (event.sender !== getLauncherWindow()?.webContents) {
      throw new Error("Plugin icons are only available to the launcher");
    }
    return loadPluginPngDataUrl(source, installPath);
  });
  ipcMain.handle("shell:pick-plugin-source", async (event, kind: "directory" | "archive") => {
    if (kind !== "directory" && kind !== "archive") return null;
    const owner = BrowserWindow.fromWebContents(event.sender);
    const options: OpenDialogOptions = kind === "directory"
      ? {
          title: "Choose a NusaShell plugin folder",
          buttonLabel: "Choose folder",
          properties: ["openDirectory"],
        }
      : {
          title: "Choose a NusaShell plugin archive",
          buttonLabel: "Choose archive",
          properties: ["openFile"],
          filters: [
            { name: "Plugin archives", extensions: ["zip", "tgz", "gz"] },
            { name: "All files", extensions: ["*"] },
          ],
        };
    const result = owner
      ? await dialog.showOpenDialog(owner, options)
      : await dialog.showOpenDialog(options);
    return result.canceled ? null : (result.filePaths[0] ?? null);
  });
  ipcMain.handle("skills:install", async (event) => {
    const owner = BrowserWindow.fromWebContents(event.sender);
    const options: OpenDialogOptions = {
      title: "Install an agent skill",
      buttonLabel: "Install skill",
      properties: ["openFile"],
      filters: [
        { name: "Agent skill packages", extensions: ["skill", "zip"] },
        { name: "All files", extensions: ["*"] },
      ],
    };
    const selection = owner
      ? await dialog.showOpenDialog(owner, options)
      : await dialog.showOpenDialog(options);
    if (selection.canceled || !selection.filePaths[0]) return null;
    const installed = await requireBackend().container.skillRegistry.installFromArchive(selection.filePaths[0]);
    await requireBackend().container.skillProvenance.markUser(installed.id);
    return installed;
  });
  ipcMain.handle("skills:list", () => requireBackend().container.skillRegistry.list());
  ipcMain.handle("skills:get", (_event, skillId: string) =>
    requireBackend().container.skillRegistry.get(skillId));
  ipcMain.handle("skills:read", (_event, skillId: string, path?: string) =>
    requireBackend().container.skillRegistry.read(skillId, path));
  ipcMain.handle("skills:write", (_event, skillId: string, path: string, content: string) =>
    requireBackend().container.skillRegistry.write(skillId, path, content));
  ipcMain.handle("skills:delete", (_event, skillId: string) =>
    requireBackend().container.skillRegistry.delete(skillId));
  ipcMain.handle("skills:pending:list", () =>
    requireBackend().container.skillApprovalStaging.list());
  ipcMain.handle("skills:pending:approve", async (_event, id: string) => {
    const staging = requireBackend().container.skillApprovalStaging;
    const pending = await staging.get(id);
    if (!pending) throw new Error(`Pending write not found: ${id}`);
    const registry = requireBackend().container.skillRegistry;
    const provenance = requireBackend().container.skillProvenance;
    switch (pending.action) {
      case "create": {
        const detail = await registry.create(pending.skillId, pending.content);
        await provenance.markAgent(pending.skillId);
        await staging.remove(id);
        return detail;
      }
      case "edit": {
        const result = await registry.write(pending.skillId, "SKILL.md", pending.content);
        await staging.remove(id);
        return result;
      }
      case "write_file": {
        const result = await registry.write(pending.skillId, pending.path, pending.content);
        await staging.remove(id);
        return result;
      }
      case "delete": {
        await registry.delete(pending.skillId);
        await provenance.clear(pending.skillId);
        await staging.remove(id);
        return { deleted: pending.skillId };
      }
      default:
        throw new Error(`Unknown pending action: ${pending.action}`);
    }
  });
  ipcMain.handle("skills:pending:reject", async (_event, id: string) => {
    await requireBackend().container.skillApprovalStaging.remove(id);
  });
  ipcMain.handle("skills:curator:status", () => {
    const c = requireBackend().container;
    return {
      ...c.skillCuratorScheduler.getStatus(),
      scheduler: c.skillCuratorScheduler.getSettings(),
      curator: c.skillCurator.getSettings(),
    };
  });
  ipcMain.handle("skills:curator:run", async (_event, dryRun: boolean) => {
    return requireBackend().container.skillCuratorScheduler.runManual(dryRun);
  });
  ipcMain.handle("skills:curator:configure", async (_event, settings: Record<string, unknown>) => {
    const c = requireBackend().container;
    if (settings.curator) c.configureCurator(settings.curator as Record<string, never>);
    if (settings.scheduler) c.configureCuratorScheduler(settings.scheduler as Record<string, never>);
    return {
      scheduler: c.skillCuratorScheduler.getSettings(),
      curator: c.skillCurator.getSettings(),
    };
  });
  ipcMain.handle("skills:pin", async (_event, skillId: string, pinned: boolean) => {
    await requireBackend().container.skillUsage.setPinned(skillId, pinned);
    return { ok: true };
  });
  ipcMain.handle("skills:restore", async (_event, skillId: string) => {
    await requireBackend().container.skillRegistry.restore(skillId);
    await requireBackend().container.skillUsage.setState(skillId, "active");
    return { ok: true };
  });
  ipcMain.handle("skills:archived:list", () =>
    requireBackend().container.skillRegistry.listArchived());

  ipcMain.handle("learning:graph", () =>
    requireBackend().container.learningGraph.buildGraph());
  ipcMain.handle("learning:node:get", (_event, nodeId: string) =>
    requireBackend().container.learningGraph.getNode(nodeId));
  ipcMain.handle("learning:node:edit", (_event, nodeId: string, content: string) =>
    requireBackend().container.learningGraph.editNode(nodeId, content));
  ipcMain.handle("learning:node:delete", (_event, nodeId: string) =>
    requireBackend().container.learningGraph.deleteNode(nodeId));
  ipcMain.handle("mail-accounts:list", (event) => {
    assertMailPluginSender(event);
    return requireMailSettingsStore().getPublic();
  });
  ipcMain.handle("mail-accounts:save", async (event, input: SaveMailAccountInput) => {
    assertMailPluginSender(event);
    const result = await requireMailSettingsStore().save(input);
    await restartMailPlugin();
    return result;
  });
  ipcMain.handle("mail-accounts:delete", async (event, accountId: string) => {
    assertMailPluginSender(event);
    const result = await requireMailSettingsStore().delete(accountId);
    await restartMailPlugin();
    return result;
  });

  logTail.subscribe((entry) => {
    for (const window of BrowserWindow.getAllWindows()) {
      window.webContents.send("logs:entry", entry);
    }
  });

  ipcMain.handle("logs:list", () => logTail.list());
  ipcMain.handle("ai-providers:list", async () => {
    if (!aiSettingsStore) throw new Error("AI settings are not ready");
    return aiSettingsStore.getPublic();
  });
  ipcMain.handle("ai-providers:save", async (_event, input: SaveAiProviderInput) => {
    if (!aiSettingsStore || !backend) throw new Error("Backend not ready");
    const result = await aiSettingsStore.saveProvider(input);
    aiSettings = await aiSettingsStore.load();
    const savedId = normalizeProviderId(input.id);
    backend.container.removeAi(savedId);
    const provider = aiSettings.providers.find((item) => item.id === savedId);
    if (provider) configureProvider(backend, provider);
    return result;
  });
  ipcMain.handle("ai-providers:delete", async (_event, providerId: string) => {
    if (!aiSettingsStore || !backend) throw new Error("Backend not ready");
    const result = await aiSettingsStore.deleteProvider(providerId);
    backend.container.removeAi(providerId);
    aiSettings = await aiSettingsStore.load();
    return result;
  });
  ipcMain.handle("ai-providers:import-models", async (_event, providerId: string) => {
    if (!aiSettingsStore) throw new Error("AI settings are not ready");
    const result = await aiSettingsStore.importModels(providerId);
    aiSettings = await aiSettingsStore.load();
    return result;
  });
  ipcMain.handle("ai-providers:add-model", async (_event, providerId: string, model: { id: string; label: string; contextWindow?: number }) => {
    if (!aiSettingsStore) throw new Error("AI settings are not ready");
    const result = await aiSettingsStore.addModel(providerId, model);
    aiSettings = await aiSettingsStore.load();
    return result;
  });
  ipcMain.handle("ai-providers:select", async (_event, input: { modelKey?: string; effort?: AiRegistrySettings["effort"] }) => {
    if (!aiSettingsStore) throw new Error("AI settings are not ready");
    const result = await aiSettingsStore.select(input);
    aiSettings = await aiSettingsStore.load();
    return result;
  });
  ipcMain.handle("ai-providers:update-runtime", async (_event, input: Pick<AiRegistrySettings, "strategy" | "totalAttemptBudget" | "stream" | "vision" | "userPrompt" | "maxToolRounds" | "maxRepeatedToolCalls" | "compactionEnabled" | "maxInputTokens" | "reserveTokens" | "recentTurns" | "summaryMaxChars">) => {
    if (!aiSettingsStore || !backend) throw new Error("Backend not ready");
    const result = await aiSettingsStore.updateRuntime(input);
    aiSettings = await aiSettingsStore.load();
    backend.container.configureAiRuntime({
      strategy: aiSettings.strategy,
      totalAttemptBudget: aiSettings.totalAttemptBudget,
      stream: aiSettings.stream,
      vision: aiSettings.vision,
      userPrompt: aiSettings.userPrompt,
      maxToolRounds: aiSettings.maxToolRounds,
      maxRepeatedToolCalls: aiSettings.maxRepeatedToolCalls,
      compactionEnabled: aiSettings.compactionEnabled,
      maxInputTokens: aiSettings.maxInputTokens,
      reserveTokens: aiSettings.reserveTokens,
      recentTurns: aiSettings.recentTurns,
      summaryMaxChars: aiSettings.summaryMaxChars,
    });
    for (const provider of aiSettings.providers) {
      backend.container.removeAi(provider.id);
      configureProvider(backend, provider);
    }
    return result;
  });
  ipcMain.handle("agent-conversations:list", () => requireConversationStore().list());
  ipcMain.handle("agent-conversations:create", (_event, options?: { kind?: "agent" | "acp"; acp?: { providerId: string; sessionId?: string; workspace?: string } }) =>
    requireConversationStore().create(options));
  ipcMain.handle("acp-providers:list", () => requireAcpProviderStore().list());
  ipcMain.handle("acp-providers:save", (_event, input: AcpProviderSaveInput) => requireAcpProviderStore().save(input));
  ipcMain.handle("acp-providers:get", (_event, providerId: string) => requireAcpProviderStore().getEffective(providerId));
  ipcMain.handle("agent-conversations:get", (_event, id: string) => requireConversationStore().get(id));
  ipcMain.handle("agent-conversations:append", (_event, id: string, message: AgentConversationMessage) =>
    requireConversationStore().appendMessage(id, message));
  ipcMain.handle("agent-conversations:checkpoint", (_event, id: string, checkpoint: AgentConversationCheckpoint) =>
    requireConversationStore().saveCheckpoint(id, checkpoint));
  ipcMain.handle("agent-conversations:delete", (_event, id: string) => requireConversationStore().delete(id));
  ipcMain.handle("agent-conversations:set-workspace", (_event, id: string, workspace: string) =>
    requireConversationStore().setWorkspace(id, workspace));
  ipcMain.handle("background-review:configure", (_event, settings: Record<string, unknown>) => {
    requireBackend().container.configureBackgroundReview(settings);
    return { ok: true };
  });
  ipcMain.handle("background-review:settings", () =>
    requireBackend().container.backgroundReviewScheduler.getSettings());
  ipcMain.on("logs:write", (_event, level: ShellLogLevel, message: string) => {
    if (!shellLogLevels.has(level) || typeof message !== "string") return;
    logTail.add("renderer", level, redactLogMessage(message.slice(0, 4000)));
  });

  try {
    backend = await startBackend();
    await waitForBackend(backend.config.port);
    logTail.add("backend", "info", `Backend ready on ${backend.config.host}:${backend.config.port}`);

    backend.container.eventDispatcher.on("plugin.uninstalled", {
      handle: (event) => {
        const pluginId = (event as { aggregateId: string }).aggregateId;
        logTail.add("main", "info", `plugin.uninstalled closing window for ${pluginId}`);
        closePluginWindow(pluginId);
      },
    });
  } catch (err) {
    console.error("[main] startBackend failed:", err);
  }

  // IPC handlers for plugin tool calls (in-process, no WS roundtrip)
  ipcMain.handle("tool:call", async (_event, pluginId: string, toolName: string, args: Record<string, unknown>) => {
    if (!backend) throw new Error("Backend not ready");
    const command: CallToolCommand = {
      kind: "call-tool",
      pluginId,
      requestId: randomUUID(),
      toolName,
      args: args ?? {},
    };
    logTail.add("ipc", "info", `tool.call ${pluginId}.${toolName} (${command.requestId})`);
    try {
      const result = await backend.container.commandBus.execute(command);
      logTail.add("ipc", "info", `tool.call completed ${pluginId}.${toolName} (${command.requestId})`);
      return result;
    } catch (error) {
      logTail.add("ipc", "error", `tool.call failed ${pluginId}.${toolName}: ${String(error)}`);
      throw error;
    }
  });

  ipcMain.handle("tool:list", async (_event, pluginId: string) => {
    if (!backend) throw new Error("Backend not ready");
    const query: ListToolsQuery = {
      kind: "list-tools",
      pluginId,
    };
    logTail.add("ipc", "debug", `tool.list ${pluginId}`);
    return backend.container.queryBus.execute(query);
  });

  ipcMain.handle("app-behavior:get", async () => {
    const settings = await requireAppBehaviorStore().load();
    return {
      ...settings,
      canSetLoginAutostart: app.isPackaged,
    };
  });
  ipcMain.handle("app-behavior:set", async (_event, patch: AppBehaviorPatch) => {
    const store = requireAppBehaviorStore();
    const previous = await store.load();
    const next = await store.set(patch);
    appBehavior = next;
    const loginSettingsChanged =
      next.launchAtLogin !== previous.launchAtLogin
      || (next.launchAtLogin && next.startHidden !== previous.startHidden);
    if (loginSettingsChanged) {
      try {
        await requireLoginAutostart().set(next.launchAtLogin, { hidden: next.startHidden });
      } catch (error) {
        appBehavior = await store.set({
          launchAtLogin: previous.launchAtLogin,
          startHidden: previous.startHidden,
        });
        throw error;
      }
    }
    return {
      ...next,
      canSetLoginAutostart: app.isPackaged,
    };
  });

  trayManager = new TrayManager({
    isPackaged: app.isPackaged,
    moduleDir: __dirname,
    resourcesPath: process.resourcesPath,
    getStatusLabel: () => backend ? "NusaShell — running" : "NusaShell — starting",
    onOpen: () => {
      showLauncherWindow();
    },
    onQuit: () => {
      isQuitting = true;
      app.quit();
    },
    onToggle: () => {
      toggleLauncherWindow();
    },
  });
  trayManager.create();

  if (!startHidden) {
    createLauncherWindow();
  } else {
    logTail.add("main", "info", "Started hidden in tray (--hidden)");
  }

  if (app.isPackaged) {
    updater = new AppUpdater();
    void updater.checkForUpdates();
  }

  // Register updater IPC handlers always (no-op in dev) to prevent renderer errors
  ipcMain.handle("updater:check", async () => updater?.checkForUpdates() ?? null);
  ipcMain.handle("updater:quit-install", () => updater?.quitAndInstall());
  ipcMain.handle("updater:status", () => updater?.getStatus() ?? null);

  app.on("activate", () => {
    showLauncherWindow();
  });
});

function requireConversationStore(): AgentConversationStore {
  if (!agentConversationStore) throw new Error("Agent conversations are not ready");
  return agentConversationStore;
}

function requireAcpProviderStore(): AcpProviderStore {
  if (!acpProviderStore) throw new Error("ACP provider store is not ready");
  return acpProviderStore;
}

function requireBackend(): BootstrapResult {
  if (!backend) throw new Error("Backend not ready");
  return backend;
}

function requireMailSettingsStore(): MailSettingsStore {
  if (!mailSettingsStore) throw new Error("Mail settings are not ready");
  return mailSettingsStore;
}

function requireAppBehaviorStore(): AppBehaviorStore {
  if (!appBehaviorStore) throw new Error("App behavior settings are not ready");
  return appBehaviorStore;
}

function requireLoginAutostart(): LoginAutostart {
  if (!loginAutostart) throw new Error("Login autostart is not ready");
  return loginAutostart;
}

async function restartMailPlugin(): Promise<void> {
  await requireBackend().container.commandBus.execute({
    kind: "restart-plugin",
    pluginId: MAIL_PLUGIN_ID,
  });
}

function assertMailPluginSender(event: IpcMainInvokeEvent): void {
  let source: URL;
  try {
    source = new URL(event.sender.getURL());
  } catch {
    throw new Error("Mail account settings are only available to the Mail plugin");
  }
  if (
    source.protocol !== "file:"
    || source.searchParams.get("pluginId") !== MAIL_PLUGIN_ID
    || !isPluginWindowSender(event.sender, MAIL_PLUGIN_ID)
  ) {
    throw new Error("Mail account settings are only available to the Mail plugin");
  }
}

function normalizeProviderId(value: string): string {
  return value.trim().toLowerCase().replace(/[^a-z0-9._-]+/g, "-").replace(/^-+|-+$/g, "");
}

app.on("window-all-closed", () => {
  closeAllPluginWindows();
  if (shouldQuitOnAllWindowsClosed({
    keepInBackground: appBehavior?.keepInBackground ?? true,
    platform: process.platform,
  })) {
    app.quit();
  }
});

app.on("before-quit", async (e) => {
  isQuitting = true;
  trayManager?.destroy();
  trayManager = null;
  if (backend) {
    e.preventDefault();
    try {
      await backend.shutdown.shutdown();
    } catch {
      // best-effort
    }
    backend = null;
    app.quit();
  }
});
