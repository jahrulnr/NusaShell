import type { App, BrowserWindow } from "electron";
import type * as Electron from "electron";
import type { BootstrapResult } from "@nusashell/backend";
import type { AiSettingsStore } from "../ai-settings.js";
import type { AgentConversationStore } from "../agent-conversation-store.js";
import type { AcpProviderStore } from "../acp-provider-store.js";
import type { MailSettingsStore } from "../mail-settings.js";
import type { AppBehaviorStore, AppBehaviorPatch, AppBehaviorSettings } from "../app-behavior-settings.js";
import type { LoginAutostart } from "../login-autostart.js";
import type { LogTail, ShellLogLevel } from "../log-tail.js";
import type { AppUpdater } from "../updater.js";

/**
 * Shared context passed to every IPC module so handlers never reach into the
 * container directly. The context is the thin facade mandated by the refactor
 * plan: IPC modules call `ctx.skillRegistry.list()`, not
 * `container.skillRegistry.list()`.
 */
export interface IpcContext {
  readonly app: App;
  readonly dialog: typeof Electron.dialog;
  readonly BrowserWindow: typeof BrowserWindow;

  getBackend(): BootstrapResult;
  getAiSettingsStore(): AiSettingsStore;
  getAgentConversationStore(): AgentConversationStore;
  getAcpProviderStore(): AcpProviderStore;
  getMailSettingsStore(): MailSettingsStore;
  getAppBehaviorStore(): AppBehaviorStore;
  getLoginAutostart(): LoginAutostart;
  getUpdater(): AppUpdater | null;

  readonly logTail: LogTail;
  readonly shellLogLevels: Set<ShellLogLevel>;

  // Container service shortcuts (facade — avoids container.* in IPC modules)
  readonly commandBus: BootstrapResult["container"]["commandBus"];
  readonly queryBus: BootstrapResult["container"]["queryBus"];
  readonly skillRegistry: BootstrapResult["container"]["skillRegistry"];
  readonly skillProvenance: BootstrapResult["container"]["skillProvenance"];
  readonly skillUsage: BootstrapResult["container"]["skillUsage"];
  readonly skillApprovalStaging: BootstrapResult["container"]["skillApprovalStaging"];
  readonly skillCurator: BootstrapResult["container"]["skillCurator"];
  readonly skillCuratorScheduler: BootstrapResult["container"]["skillCuratorScheduler"];
  readonly backgroundReviewScheduler: BootstrapResult["container"]["backgroundReviewScheduler"];
  readonly learningGraph: BootstrapResult["container"]["learningGraph"];
  configureBackgroundReview: BootstrapResult["container"]["configureBackgroundReview"];
  configureCurator: BootstrapResult["container"]["configureCurator"];
  configureCuratorScheduler: BootstrapResult["container"]["configureCuratorScheduler"];

  // App behavior state (shared with main for close policy)
  getAppBehavior(): AppBehaviorSettings | null;
  setAppBehavior(settings: AppBehaviorSettings): void;

  // Misc helpers
  redactLogMessage(message: string): string;
  isPluginWindowSender(sender: Electron.WebContents, pluginId: string): boolean;
}

export type { AppBehaviorPatch };
