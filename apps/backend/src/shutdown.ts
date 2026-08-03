import type { Container } from "./container.js";

export class ShutdownCoordinator {
  private shuttingDown = false;

  constructor(private readonly container: Container) {}

  async shutdown(): Promise<void> {
    if (this.shuttingDown) return;
    this.shuttingDown = true;

    // 1. Stop accepting new connections
    await this.container.wsServer.stop();

    // 2. Reject new commands
    this.container.router.close();

    // 3. Close active sessions
    this.container.wsServer.sessionRegistry.clear();

    // 4. Cancel pending tool calls + gracefully stop plugin runtimes
    try {
      await this.container.runtimeManager.stopAll();
    } catch (err) {
      this.container.logger.warn({ err }, "Error during runtime shutdown");
    }

    // 5. Stop the job scheduler tick loop and event matcher
    try {
      this.container.eventJobMatcher.stop();
      this.container.jobScheduler.stop();
    } catch (err) {
      this.container.logger.warn({ err }, "Error stopping job scheduler");
    }

    // 6. Close database
    try {
      this.container.db?.close();
    } catch (err) {
      this.container.logger.warn({ err }, "Error closing database");
    }

    process.exit(0);
  }

  get isShuttingDown(): boolean {
    return this.shuttingDown;
  }
}
