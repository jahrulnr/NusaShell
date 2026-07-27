import type { Container } from "./container.js";

export class ShutdownCoordinator {
  private shuttingDown = false;

  constructor(private readonly container: Container) {}

  async shutdown(): Promise<void> {
    if (this.shuttingDown) return;
    this.shuttingDown = true;

    await this.container.wsServer.stop();

    try {
      await this.container.runtimeManager.stopAll();
    } catch {
      // best effort
    }

    process.exit(0);
  }

  get isShuttingDown(): boolean {
    return this.shuttingDown;
  }
}
