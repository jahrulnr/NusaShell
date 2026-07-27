import { createContainer, type Container } from "./container.js";
import { ShutdownCoordinator } from "./shutdown.js";

export interface BootstrapOptions {
  readonly port: number;
  readonly host?: string;
  readonly pluginsRoot?: string;
}

export interface BootstrapResult {
  readonly container: Container;
  readonly shutdown: ShutdownCoordinator;
}

export async function bootstrap(options: BootstrapOptions): Promise<BootstrapResult> {
  const container = createContainer(options);
  const shutdown = new ShutdownCoordinator(container);

  await container.wsServer.start();

  process.on("SIGTERM", () => void shutdown.shutdown());
  process.on("SIGINT", () => void shutdown.shutdown());

  return { container, shutdown };
}

export { createContainer, type Container, type ContainerOptions } from "./container.js";
export { ShutdownCoordinator } from "./shutdown.js";
