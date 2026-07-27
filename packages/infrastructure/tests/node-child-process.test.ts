import { describe, expect, it } from "vitest";
import { NodeChildProcessAdapter } from "../src/process/node-child-process.adapter.js";

describe("NodeChildProcessAdapter", () => {
  it("spawns a process and resolves exited with code 0", async () => {
    const adapter = new NodeChildProcessAdapter();
    const handle = await adapter.spawn("node", ["-e", "process.exit(0)"], {});

    expect(handle.pid).toBeGreaterThan(0);

    const code = await handle.exited;
    expect(code).toBe(0);
  });

  it("resolves exited with non-zero code on failure", async () => {
    const adapter = new NodeChildProcessAdapter();
    const handle = await adapter.spawn("node", ["-e", "process.exit(1)"], {});

    const code = await handle.exited;
    expect(code).toBe(1);
  });

  it("kill terminates a long-running process", async () => {
    const adapter = new NodeChildProcessAdapter();
    const handle = await adapter.spawn(
      "node",
      ["-e", "setInterval(() => {}, 1000)"],
      {},
    );

    expect(handle.pid).toBeGreaterThan(0);

    await handle.kill("SIGTERM");

    const code = await handle.exited;
    expect(code).toBe(-1);
  });
});
