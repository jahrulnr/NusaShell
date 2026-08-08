import { describe, expect, it } from "vitest";
import { AgentTurnCoordinator } from "../src/index.js";

describe("AgentTurnCoordinator", () => {
  it("cancels an active turn and removes it after completion", async () => {
    const coordinator = new AgentTurnCoordinator();
    const running = coordinator.run("trace-1", async (signal) =>
      new Promise<string>((resolve) => {
        signal.addEventListener("abort", () => resolve("cancelled"), { once: true });
      }));

    expect(coordinator.cancel("trace-1")).toBe(true);
    await expect(running).resolves.toBe("cancelled");
    expect(coordinator.cancel("trace-1")).toBe(false);
  });

  it("rejects duplicate active trace IDs", async () => {
    const coordinator = new AgentTurnCoordinator();
    let release = () => {};
    const first = coordinator.run("trace-1", () => new Promise<void>((resolve) => { release = resolve; }));

    await expect(coordinator.run("trace-1", async () => undefined)).rejects.toMatchObject({
      code: "OPERATION_CONFLICT",
    });
    release();
    await first;
  });

  it("cancels every active turn and waits until their cleanup hooks finish", async () => {
    const coordinator = new AgentTurnCoordinator();
    const running = ["trace-a", "trace-b"].map((traceId) => coordinator.run(traceId, (signal) =>
      new Promise<void>((resolve) => signal.addEventListener("abort", () => resolve(), { once: true }))));

    expect(coordinator.cancelAll()).toBe(2);
    await coordinator.waitForIdle();
    await Promise.all(running);
    expect(coordinator.activeCount).toBe(0);
  });
});
