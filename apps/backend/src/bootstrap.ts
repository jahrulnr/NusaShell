import { createContainer, type Container } from "./container.js";
import { ShutdownCoordinator } from "./shutdown.js";
import { loadConfig, type AppConfig, type BackgroundReviewSettings, type AcpProviderResolverPort, type AgentTurnResult, type AgentTurnPartial } from "@nusashell/application";
import type { LogObserver } from "@nusashell/infrastructure";

export interface BootstrapOptions {
  readonly appVersion?: string;
  readonly config?: Partial<AppConfig>;
  readonly loggerObserver?: LogObserver;
  readonly logFile?: string;
  readonly promptsRoot?: string;
  readonly docsRoot?: string;
  readonly docsIndexStorageRoot?: string;
  readonly skillsRoot?: string;
  readonly memoryRoot?: string;
  readonly jobsRoot?: string;
  readonly backgroundReview?: Partial<BackgroundReviewSettings>;
  readonly resolvePluginRuntimeEnvironment?: (
    pluginId: string,
  ) => Promise<Readonly<Record<string, string>>> | Readonly<Record<string, string>>;
  readonly acpProviderResolver?: AcpProviderResolverPort;
  /**
   * Durable seal callback for successful agent turns. Desktop main writes the
   * assistant message off the renderer path.
   */
  readonly sealAgentTurn?: (conversationId: string, result: AgentTurnResult, options: { resume: boolean }) => Promise<void>;
  /**
   * Durable seal when a turn is interrupted with partial progress. Desktop
   * main writes interrupted + resumeMessages so Retry can continue tools.
   */
  readonly sealAgentInterrupted?: (
    conversationId: string,
    partial: AgentTurnPartial,
    options: { resume: boolean; interruptReason: "cancel" | "provider" | "max_rounds" },
  ) => Promise<void>;
  /**
   * When false, do not start the WebSocket server. Desktop sets this to false
   * since the renderer uses IPC. Default: true.
   */
  readonly startWsServer?: boolean;
}

export interface BootstrapResult {
  readonly container: Container;
  readonly shutdown: ShutdownCoordinator;
  readonly config: AppConfig;
}

export async function bootstrap(options: BootstrapOptions = {}): Promise<BootstrapResult> {
  const envConfig = loadConfig();
  const config: AppConfig = { ...envConfig, ...options.config };

  const container = createContainer({
    port: config.port,
    host: config.host,
    ...(config.pluginsRoot !== undefined ? { pluginsRoot: config.pluginsRoot } : {}),
    ...(config.bundledPluginsRoot !== undefined ? { bundledPluginsRoot: config.bundledPluginsRoot } : {}),
    ...(config.userPluginsRoot !== undefined ? { userPluginsRoot: config.userPluginsRoot } : {}),
    ...(config.builtinSkillsRoot !== undefined ? { builtinSkillsRoot: config.builtinSkillsRoot } : {}),
    ...(config.dbPath !== undefined ? { dbPath: config.dbPath } : {}),
    ...(options.promptsRoot !== undefined ? { promptsRoot: options.promptsRoot } : {}),
    ...(options.docsRoot !== undefined ? { docsRoot: options.docsRoot } : {}),
    ...(options.docsIndexStorageRoot !== undefined ? { docsIndexStorageRoot: options.docsIndexStorageRoot } : {}),
    ...(options.skillsRoot !== undefined ? { skillsRoot: options.skillsRoot } : {}),
    ...(options.memoryRoot !== undefined ? { memoryRoot: options.memoryRoot } : {}),
    ...(options.jobsRoot !== undefined ? { jobsRoot: options.jobsRoot } : {}),
    ...(options.appVersion !== undefined ? { appVersion: options.appVersion } : {}),
    ...(options.resolvePluginRuntimeEnvironment !== undefined
      ? { resolvePluginRuntimeEnvironment: options.resolvePluginRuntimeEnvironment }
      : {}),
    logLevel: config.logLevel,
    ...(options.logFile !== undefined ? { logFile: options.logFile } : {}),
    ai: {
      providerId: config.ai.providerId,
      stubEnabled: config.ai.stubEnabled,
      ...(config.ai.api !== undefined ? { api: config.ai.api } : {}),
      ...(config.ai.model !== undefined ? { model: config.ai.model } : {}),
      ...(config.ai.baseUrl !== undefined ? { baseUrl: config.ai.baseUrl } : {}),
      ...(config.ai.apiKey !== undefined ? { apiKey: config.ai.apiKey } : {}),
      maxToolRounds: config.ai.maxToolRounds,
      ...(config.ai.jobMaxToolRounds !== undefined ? { jobMaxToolRounds: config.ai.jobMaxToolRounds } : {}),
      softRecoverAttempts: config.ai.softRecoverAttempts,
      maxConcurrentToolCalls: config.ai.maxConcurrentToolCalls,
      maxAutoContinues: config.ai.maxAutoContinues,
      strategy: config.ai.strategy,
      totalAttemptBudget: config.ai.totalAttemptBudget,
      stream: config.ai.stream,
      vision: config.ai.vision,
      userPrompt: config.ai.userPrompt,
      timeoutMs: config.ai.timeoutMs,
      retry: config.ai.retry,
      context: config.ai.context,
    },
    ...(options.loggerObserver ? { loggerObserver: options.loggerObserver } : {}),
    ...(options.backgroundReview ? { backgroundReview: options.backgroundReview } : {}),
    ...(options.acpProviderResolver ? { acpProviderResolver: options.acpProviderResolver } : {}),
    ...(options.sealAgentTurn ? { sealAgentTurn: options.sealAgentTurn } : {}),
    ...(options.sealAgentInterrupted ? { sealAgentInterrupted: options.sealAgentInterrupted } : {}),
    ...(options.startWsServer === false ? { startWsServer: false } : {}),
  });
  const shutdown = new ShutdownCoordinator(container);

  await container.runtimeManager.startAutostartPlugins();

  container.jobScheduler.start();
  container.pipelineTriggerCoordinator?.start();

  if (options.startWsServer !== false) {
    await container.wsServer.start();
  }

  process.on("SIGTERM", () => void shutdown.shutdown());
  process.on("SIGINT", () => void shutdown.shutdown());

  return { container, shutdown, config };
}

export { createContainer, type Container, type ContainerOptions } from "./container.js";
export { ShutdownCoordinator } from "./shutdown.js";
export { type AppConfig, loadConfig } from "@nusashell/application";
