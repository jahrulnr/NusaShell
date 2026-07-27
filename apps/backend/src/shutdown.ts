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
    } catch {
      // best effort
    }

    // 5. Close database
    try {
      this.container.db?.close();
    } catch {
      // best effort
    }

    process.exit(0);
  }

  get isShuttingDown(): boolean {
    return this.shuttingDown;
  }
}
