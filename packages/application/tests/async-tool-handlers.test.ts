import { describe, expect, it } from "vitest";
import { AsyncToolRuntime } from "../src/index.js";
import { execAsyncRun, execAsyncWait, execAsyncPeek, execAsyncKill } from "../src/index.js";

function makeManualCall() {
  let resolve!: (value: unknown) => void;
  let reject!: (error: Error) => void;
  const promise = new Promise<unknown>((res, rej) => {
    resolve = res;
    reject = rej;
  });
  return { promise, resolve, reject };
}

describe("async tool handlers", () => {
  it("execAsyncRun spawns a background handle and returns handleId + running status", async () => {
    const runtime = new AsyncToolRuntime();
    const work = makeManualCall();
    const result = await execAsyncRun(runtime, {
      tool: "run_command",
      args: { command: "docker logs -f" },
    }, {
      conversationId: "conv-1",
      traceId: "trace-1",
      kind: "mcp",
      pluginId: "nusashell.terminal",
      spawnWork: () => work.promise,
    });
    expect(result.handleId).toBeTruthy();
    expect(result.status).toBe("running");
    runtime.dispose();
  });

  it("execAsyncRun rejects when tool name is missing", async () => {
    const runtime = new AsyncToolRuntime();
    await expect(execAsyncRun(runtime, {}, { conversationId: "conv-1", spawnWork: () => Promise.resolve(), kind: "mcp" }))
      .rejects.toThrow();
    runtime.dispose();
  });

  it("execAsyncRun rejects when conversationId is missing", async () => {
    const runtime = new AsyncToolRuntime();
    await expect(execAsyncRun(runtime, { tool: "t" }, { spawnWork: () => Promise.resolve() } as any))
      .rejects.toThrow();
    runtime.dispose();
  });

  it("execAsyncPeek returns status + tail for a running handle", async () => {
    const runtime = new AsyncToolRuntime();
    const work = makeManualCall();
    const spawned = await execAsyncRun(runtime, { tool: "t", args: {} }, {
      conversationId: "conv-1",
      spawnWork: () => work.promise,
      kind: "mcp",
    });
    const peek = await execAsyncPeek(runtime, { handleId: spawned.handleId });
    expect(peek.status).toBe("running");
    expect(peek.tail).toBe("");
    runtime.dispose();
  });

  it("execAsyncPeek rejects on unknown handleId", async () => {
    const runtime = new AsyncToolRuntime();
    await expect(execAsyncPeek(runtime, { handleId: "nonexistent" })).rejects.toThrow();
    runtime.dispose();
  });

  it("execAsyncWait returns the final status when the handle completes", async () => {
    const runtime = new AsyncToolRuntime();
    const work = makeManualCall();
    const spawned = await execAsyncRun(runtime, { tool: "t", args: {} }, {
      conversationId: "conv-1",
      spawnWork: () => work.promise,
      kind: "mcp",
    });
    setTimeout(() => work.resolve({ ok: true }), 5);
    const result = await execAsyncWait(runtime, { handleId: spawned.handleId, timeoutMs: 5000 });
    expect(result.status).toBe("ok");
    runtime.dispose();
  });

  it("execAsyncWait returns running when timeout elapses first", async () => {
    const runtime = new AsyncToolRuntime();
    const work = makeManualCall();
    const spawned = await execAsyncRun(runtime, { tool: "t", args: {} }, {
      conversationId: "conv-1",
      spawnWork: () => work.promise,
      kind: "mcp",
    });
    const result = await execAsyncWait(runtime, { handleId: spawned.handleId, timeoutMs: 1000 });
    expect(result.status).toBe("running");
    runtime.dispose();
  });

  it("execAsyncWait rejects on unknown handleId", async () => {
    const runtime = new AsyncToolRuntime();
    await expect(execAsyncWait(runtime, { handleId: "nonexistent", timeoutMs: 2000 })).rejects.toThrow();
    runtime.dispose();
  });

  it("execAsyncWait rejects when timeoutMs is out of bounds", async () => {
    const runtime = new AsyncToolRuntime();
    const work = makeManualCall();
    const spawned = await execAsyncRun(runtime, { tool: "t", args: {} }, {
      conversationId: "conv-1",
      spawnWork: () => work.promise,
      kind: "mcp",
    });
    await expect(execAsyncWait(runtime, { handleId: spawned.handleId, timeoutMs: 0 })).rejects.toThrow();
    await expect(execAsyncWait(runtime, { handleId: spawned.handleId, timeoutMs: 600_000 })).rejects.toThrow();
    runtime.dispose();
  });

  it("execAsyncKill kills a running handle", async () => {
    const runtime = new AsyncToolRuntime();
    const work = makeManualCall();
    const spawned = await execAsyncRun(runtime, { tool: "t", args: {} }, {
      conversationId: "conv-1",
      spawnWork: () => work.promise,
      kind: "mcp",
    });
    const result = await execAsyncKill(runtime, { handleId: spawned.handleId });
    expect(result.status).toBe("killed");
    runtime.dispose();
  });

  it("execAsyncKill rejects on unknown handleId", async () => {
    const runtime = new AsyncToolRuntime();
    await expect(execAsyncKill(runtime, { handleId: "nonexistent" })).rejects.toThrow();
    runtime.dispose();
  });

  it("execAsyncWait is interrupted by abort signal", async () => {
    const runtime = new AsyncToolRuntime();
    const work = makeManualCall();
    const handle = await execAsyncRun(runtime, { tool: "t" }, {
      conversationId: "conv-1",
      kind: "mcp",
      spawnWork: () => work.promise,
    });
    const controller = new AbortController();
    const waitPromise = execAsyncWait(runtime, { handleId: handle.handleId, timeoutMs: 10000 }, controller.signal);
    // Abort after 50ms (simulating user steer / turn cancel).
    setTimeout(() => controller.abort(), 50);
    const result = await waitPromise;
    expect(result.status).toBe("running");
    expect(result.interrupted).toBe(true);
    runtime.dispose();
  });

  it("execAsyncWait returns final status when handle settles before abort", async () => {
    const runtime = new AsyncToolRuntime();
    const work = makeManualCall();
    const handle = await execAsyncRun(runtime, { tool: "t" }, {
      conversationId: "conv-1",
      kind: "mcp",
      spawnWork: () => work.promise,
    });
    const controller = new AbortController();
    // Settle the handle immediately.
    work.resolve({ ok: true });
    const result = await execAsyncWait(runtime, { handleId: handle.handleId, timeoutMs: 5000 }, controller.signal);
    expect(result.status).toBe("ok");
    expect(result.interrupted).toBeUndefined();
    runtime.dispose();
  });
});
