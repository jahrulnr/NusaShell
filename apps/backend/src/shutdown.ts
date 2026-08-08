import type { Container } from "./container.js";

export class ShutdownCoordinator {
  private shuttingDown = false;

  constructor(private readonly container: Container) {}

  async shutdown(): Promise<void> {
    if (this.shuttingDown) return;
    this.shuttingDown = true;

    // 1. Stop accepting new connections and cancel room-bound agent turns.
    // The coordinator waits for RunAgentTurnHandler finally blocks, including
    // durable interrupted sealing, before the process exits.
    const cancelledTurns = this.container.agentTurnCoordinator.cancelAll();
    if (cancelledTurns > 0) {
      await this.container.agentTurnCoordinator.waitForIdle();
      if (this.container.agentTurnCoordinator.activeCount > 0) {
        this.container.logger.warn(
          { activeTurns: this.container.agentTurnCoordinator.activeCount },
          "Timed out waiting for agent turns during shutdown; durable recovery will run on next startup",
        );
      }
    }

    // 2. Stop accepting new connections
    await this.container.wsServer.stop();

    // 3. Reject new commands
    this.container.router.close();

    // 4. Close active sessions
    this.container.wsServer.sessionRegistry.clear();

    // 5. Cancel pending tool calls + gracefully stop plugin runtimes
    try {
      await this.container.runtimeManager.stopAll();
    } catch (err) {
      this.container.logger.warn({ err }, "Error during runtime shutdown");
    }

    // 6. Stop the job scheduler tick loop and event matcher
    try {
      this.container.eventJobMatcher.stop();
      this.container.pipelineTriggerCoordinator?.stop();
      this.container.jobScheduler.stop();
    } catch (err) {
      this.container.logger.warn({ err }, "Error stopping job scheduler");
    }

    // 7. Close database
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
