import { createContainer, type Container } from "./container.js";
import { ShutdownCoordinator } from "./shutdown.js";
import { loadConfig, type AppConfig } from "@nusashell/application";
import type { LogObserver } from "@nusashell/infrastructure";

export interface BootstrapOptions {
  readonly config?: Partial<AppConfig>;
  readonly loggerObserver?: LogObserver;
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
    pluginsRoot: config.pluginsRoot,
    dbPath: config.dbPath,
    logLevel: config.logLevel,
    ...(options.loggerObserver ? { loggerObserver: options.loggerObserver } : {}),
  });
  const shutdown = new ShutdownCoordinator(container);

  await container.wsServer.start();

  process.on("SIGTERM", () => void shutdown.shutdown());
  process.on("SIGINT", () => void shutdown.shutdown());

  return { container, shutdown, config };
}

export { createContainer, type Container, type ContainerOptions } from "./container.js";
export { ShutdownCoordinator } from "./shutdown.js";
export { type AppConfig, loadConfig } from "@nusashell/application";
