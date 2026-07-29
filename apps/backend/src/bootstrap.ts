import { createContainer, type Container } from "./container.js";
import { ShutdownCoordinator } from "./shutdown.js";
import { loadConfig, type AppConfig } from "@nusashell/application";
import type { LogObserver } from "@nusashell/infrastructure";

export interface BootstrapOptions {
  readonly config?: Partial<AppConfig>;
  readonly loggerObserver?: LogObserver;
  readonly promptsRoot?: string;
  readonly docsRoot?: string;
  readonly docsIndexStorageRoot?: string;
  readonly skillsRoot?: string;
  readonly resolvePluginRuntimeEnvironment?: (
    pluginId: string,
  ) => Promise<Readonly<Record<string, string>>> | Readonly<Record<string, string>>;
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
    ...(config.dbPath !== undefined ? { dbPath: config.dbPath } : {}),
    ...(options.promptsRoot !== undefined ? { promptsRoot: options.promptsRoot } : {}),
    ...(options.docsRoot !== undefined ? { docsRoot: options.docsRoot } : {}),
    ...(options.docsIndexStorageRoot !== undefined ? { docsIndexStorageRoot: options.docsIndexStorageRoot } : {}),
    ...(options.skillsRoot !== undefined ? { skillsRoot: options.skillsRoot } : {}),
    ...(options.resolvePluginRuntimeEnvironment !== undefined
      ? { resolvePluginRuntimeEnvironment: options.resolvePluginRuntimeEnvironment }
      : {}),
    logLevel: config.logLevel,
    ai: {
      providerId: config.ai.providerId,
      stubEnabled: config.ai.stubEnabled,
      ...(config.ai.api !== undefined ? { api: config.ai.api } : {}),
      ...(config.ai.model !== undefined ? { model: config.ai.model } : {}),
      ...(config.ai.baseUrl !== undefined ? { baseUrl: config.ai.baseUrl } : {}),
      ...(config.ai.apiKey !== undefined ? { apiKey: config.ai.apiKey } : {}),
      maxToolRounds: config.ai.maxToolRounds,
      strategy: config.ai.strategy,
      totalAttemptBudget: config.ai.totalAttemptBudget,
      stream: config.ai.stream,
      vision: config.ai.vision,
      timeoutMs: config.ai.timeoutMs,
      retry: config.ai.retry,
      context: config.ai.context,
    },
    ...(options.loggerObserver ? { loggerObserver: options.loggerObserver } : {}),
  });
  const shutdown = new ShutdownCoordinator(container);

  await container.runtimeManager.startAutostartPlugins();

  await container.wsServer.start();

  process.on("SIGTERM", () => void shutdown.shutdown());
  process.on("SIGINT", () => void shutdown.shutdown());

  return { container, shutdown, config };
}

export { createContainer, type Container, type ContainerOptions } from "./container.js";
export { ShutdownCoordinator } from "./shutdown.js";
export { type AppConfig, loadConfig } from "@nusashell/application";
