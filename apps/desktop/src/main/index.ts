import { app, BrowserWindow, dialog, Menu } from "electron";
import { resolve } from "node:path";
import { bootstrap, type BootstrapResult } from "@nusashell/backend";
import { LogTail, type ShellLogLevel, type ShellLogSource } from "./log-tail.js";
import {
  createLauncherWindow,
  closeAllPluginWindows,
  closePluginWindow,
  isPluginWindowSender,
  registerWindowIpc,
  setLauncherClosePolicy,
  showLauncherWindow,
  toggleLauncherWindow,
} from "./window-manager.js";
import { LINUX_DESKTOP_APP_NAME } from "./window-assets.js";
import { AppUpdater } from "./updater.js";
import { loadConfig, type StartPluginCommand } from "@nusashell/application";
import { AiSettingsStore, type AiRegistrySettings } from "./ai-settings.js";
import { AcpProviderStore } from "./acp-provider-store.js";
import { flattenModelCatalog } from "./ai-provider-registry.js";
import { AgentConversationStore } from "./agent-conversation-store.js";
import { MailSettingsStore } from "./mail-settings.js";
import {
  AppBehaviorStore,
  shouldHideOnClose,
  shouldQuitOnAllWindowsClosed,
  type AppBehaviorSettings,
} from "./app-behavior-settings.js";
import { createLoginAutostart, type LoginAutostart } from "./login-autostart.js";
import { TrayManager } from "./tray.js";
import {
  registerSkillsIpc,
  registerAiIpc,
  registerAgentIpc,
  registerMailIpc,
  registerPluginsIpc,
  registerShellIpc,
  type IpcContext,
} from "./ipc/index.js";

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
      softRecoverAttempts: aiRuntimeConfig.softRecoverAttempts,
      maxConcurrentToolCalls: aiRuntimeConfig.maxConcurrentToolCalls,
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

function requireBackend(): BootstrapResult {
  if (!backend) throw new Error("Backend not ready");
  return backend;
}

function createIpcContext(): IpcContext {
  const b = requireBackend();
  const c = b.container;
  return {
    app,
    dialog,
    BrowserWindow,
    getBackend: () => requireBackend(),
    getAiSettingsStore: () => { if (!aiSettingsStore) throw new Error("AI settings are not ready"); return aiSettingsStore; },
    getAgentConversationStore: () => { if (!agentConversationStore) throw new Error("Agent conversations are not ready"); return agentConversationStore; },
    getAcpProviderStore: () => { if (!acpProviderStore) throw new Error("ACP provider store is not ready"); return acpProviderStore; },
    getMailSettingsStore: () => { if (!mailSettingsStore) throw new Error("Mail settings are not ready"); return mailSettingsStore; },
    getAppBehaviorStore: () => { if (!appBehaviorStore) throw new Error("App behavior settings are not ready"); return appBehaviorStore; },
    getLoginAutostart: () => { if (!loginAutostart) throw new Error("Login autostart is not ready"); return loginAutostart; },
    getUpdater: () => updater,
    logTail,
    shellLogLevels,
    commandBus: c.commandBus,
    queryBus: c.queryBus,
    skillRegistry: c.skillRegistry,
    skillProvenance: c.skillProvenance,
    skillUsage: c.skillUsage,
    skillApprovalStaging: c.skillApprovalStaging,
    skillCurator: c.skillCurator,
    skillCuratorScheduler: c.skillCuratorScheduler,
    backgroundReviewScheduler: c.backgroundReviewScheduler,
    learningGraph: c.learningGraph,
    configureBackgroundReview: (...args) => c.configureBackgroundReview(...args),
    configureCurator: (...args) => c.configureCurator(...args),
    configureCuratorScheduler: (...args) => c.configureCuratorScheduler(...args),
    getAppBehavior: () => appBehavior,
    setAppBehavior: (settings) => { appBehavior = settings; },
    redactLogMessage,
    isPluginWindowSender,
  };
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

  // Register all IPC handlers through focused modules (no container.* in IPC)
  const ctx = createIpcContext();
  registerSkillsIpc(ctx);
  registerAiIpc(ctx);
  registerAgentIpc(ctx);
  registerMailIpc(ctx);
  registerPluginsIpc(ctx);
  registerShellIpc(ctx);

  logTail.subscribe((entry) => {
    for (const window of BrowserWindow.getAllWindows()) {
      window.webContents.send("logs:entry", entry);
    }
  });

  trayManager = new TrayManager({
    isPackaged: app.isPackaged,
    moduleDir: __dirname,
    resourcesPath: process.resourcesPath,
    getStatusLabel: () => backend ? "NusaShell — running" : "NusaShell — starting",
    onOpen: () => { showLauncherWindow(); },
    onQuit: () => { isQuitting = true; app.quit(); },
    onToggle: () => { toggleLauncherWindow(); },
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

  app.on("activate", () => {
    showLauncherWindow();
  });
});

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
