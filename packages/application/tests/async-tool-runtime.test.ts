import { describe, expect, it } from "vitest";
import { AsyncToolRuntime } from "../src/index.js";

/**
 * Fake MCP-style work: returns a promise we can settle manually.
 */
function makeManualCall() {
  let resolve!: (value: unknown) => void;
  let reject!: (error: Error) => void;
  const promise = new Promise<unknown>((res, rej) => {
    resolve = res;
    reject = rej;
  });
  return { promise, resolve, reject };
}

describe("AsyncToolRuntime", () => {
  it("spawn returns a running handle with a unique handleId", async () => {
    const runtime = new AsyncToolRuntime();
    const work = makeManualCall();
    const handle = await runtime.spawn({
      conversationId: "conv-1",
      kind: "mcp",
      pluginId: "nusashell.terminal",
      toolName: "run_command",
      args: { command: "docker logs -f" },
      traceId: "trace-1",
      work: () => work.promise,
    });
    expect(handle.handleId).toBeTruthy();
    expect(handle.status).toBe("running");
    expect(handle.kind).toBe("mcp");
    expect(handle.toolName).toBe("run_command");
    runtime.dispose();
  });

  it("peek on a running handle returns running status with empty tail", async () => {
    const runtime = new AsyncToolRuntime();
    const work = makeManualCall();
    const handle = await runtime.spawn({
      conversationId: "conv-1",
      kind: "mcp",
      pluginId: "p",
      toolName: "t",
      args: {},
      work: () => work.promise,
    });
    const peek = runtime.peek(handle.handleId);
    expect(peek?.status).toBe("running");
    expect(peek?.tail).toBe("");
    runtime.dispose();
  });

  it("settle completes the handle and peek returns the final result", async () => {
    const runtime = new AsyncToolRuntime();
    const work = makeManualCall();
    const handle = await runtime.spawn({
      conversationId: "conv-1",
      kind: "mcp",
      pluginId: "p",
      toolName: "t",
      args: {},
      work: () => work.promise,
    });
    work.resolve({ ok: true, output: "done" });
    // Let the microtask settle.
    await new Promise((r) => setTimeout(r, 0));
    const peek = runtime.peek(handle.handleId);
    expect(peek?.status).toBe("ok");
    expect(peek?.result).toEqual({ ok: true, output: "done" });
    runtime.dispose();
  });

  it("fail settles the handle with fail status and error", async () => {
    const runtime = new AsyncToolRuntime();
    const work = makeManualCall();
    const handle = await runtime.spawn({
      conversationId: "conv-1",
      kind: "mcp",
      pluginId: "p",
      toolName: "t",
      args: {},
      work: () => work.promise,
    });
    work.reject(new Error("boom"));
    await new Promise((r) => setTimeout(r, 0));
    const peek = runtime.peek(handle.handleId);
    expect(peek?.status).toBe("fail");
    expect(peek?.error).toBe("boom");
    runtime.dispose();
  });

  it("wait returns early when the handle completes before timeout", async () => {
    const runtime = new AsyncToolRuntime();
    const work = makeManualCall();
    const handle = await runtime.spawn({
      conversationId: "conv-1",
      kind: "mcp",
      pluginId: "p",
      toolName: "t",
      args: {},
      work: () => work.promise,
    });
    // Settle shortly.
    setTimeout(() => work.resolve({ ok: true }), 5);
    const result = await runtime.wait(handle.handleId, 5000);
    expect(result?.status).toBe("ok");
    expect(result?.result).toEqual({ ok: true });
    runtime.dispose();
  });

  it("wait returns still_running when the handle does not settle before timeout", async () => {
    const runtime = new AsyncToolRuntime();
    const work = makeManualCall();
    const handle = await runtime.spawn({
      conversationId: "conv-1",
      kind: "mcp",
      pluginId: "p",
      toolName: "t",
      args: {},
      work: () => work.promise,
    });
    const result = await runtime.wait(handle.handleId, 20);
    expect(result?.status).toBe("running");
    runtime.dispose();
  });

  it("kill mid-flight settles the handle with killed status", async () => {
    const runtime = new AsyncToolRuntime();
    const work = makeManualCall();
    const handle = await runtime.spawn({
      conversationId: "conv-1",
      kind: "mcp",
      pluginId: "p",
      toolName: "t",
      args: {},
      work: () => work.promise,
    });
    const result = runtime.kill(handle.handleId);
    expect(result?.status).toBe("killed");
    const peek = runtime.peek(handle.handleId);
    expect(peek?.status).toBe("killed");
    runtime.dispose();
  });

  it("kill on an already-settled handle returns the final status", async () => {
    const runtime = new AsyncToolRuntime();
    const work = makeManualCall();
    const handle = await runtime.spawn({
      conversationId: "conv-1",
      kind: "mcp",
      pluginId: "p",
      toolName: "t",
      args: {},
      work: () => work.promise,
    });
    work.resolve({ ok: true });
    await new Promise((r) => setTimeout(r, 0));
    const result = runtime.kill(handle.handleId);
    expect(result?.status).toBe("ok");
    runtime.dispose();
  });

  it("list returns only handles for the given conversationId", async () => {
    const runtime = new AsyncToolRuntime();
    const work1 = makeManualCall();
    const work2 = makeManualCall();
    await runtime.spawn({
      conversationId: "conv-1",
      kind: "mcp",
      pluginId: "p",
      toolName: "t",
      args: {},
      work: () => work1.promise,
    });
    await runtime.spawn({
      conversationId: "conv-2",
      kind: "mcp",
      pluginId: "p",
      toolName: "t",
      args: {},
      work: () => work2.promise,
    });
    const list1 = runtime.list("conv-1");
    const list2 = runtime.list("conv-2");
    expect(list1).toHaveLength(1);
    expect(list2).toHaveLength(1);
    runtime.dispose();
  });

  it("publishes started/update/ended events via the event publisher", async () => {
    const events: Array<{ type: string; handleId: string }> = [];
    const runtime = new AsyncToolRuntime({
      onStarted: (h) => events.push({ type: "started", handleId: h.handleId }),
      onEnded: (h) => events.push({ type: "ended", handleId: h.handleId }),
    });
    const work = makeManualCall();
    const handle = await runtime.spawn({
      conversationId: "conv-1",
      kind: "mcp",
      pluginId: "p",
      toolName: "t",
      args: {},
      work: () => work.promise,
    });
    work.resolve({ ok: true });
    await new Promise((r) => setTimeout(r, 0));
    expect(events.map((e) => e.type)).toEqual(["started", "ended"]);
    expect(events[0]!.handleId).toBe(handle.handleId);
    runtime.dispose();
  });

  it("killAllForConversation kills all running handles for that conversation", async () => {
    const runtime = new AsyncToolRuntime();
    const work1 = makeManualCall();
    const work2 = makeManualCall();
    const h1 = await runtime.spawn({
      conversationId: "conv-1",
      kind: "mcp",
      pluginId: "p",
      toolName: "t",
      args: {},
      work: () => work1.promise,
    });
    const h2 = await runtime.spawn({
      conversationId: "conv-1",
      kind: "mcp",
      pluginId: "p",
      toolName: "t",
      args: {},
      work: () => work2.promise,
    });
    const killed = runtime.killAllForConversation("conv-1");
    expect(killed).toBe(2);
    expect(runtime.peek(h1.handleId)?.status).toBe("killed");
    expect(runtime.peek(h2.handleId)?.status).toBe("killed");
    runtime.dispose();
  });

  it("peek on an unknown handleId returns undefined", () => {
    const runtime = new AsyncToolRuntime();
    expect(runtime.peek("nonexistent")).toBeUndefined();
    runtime.dispose();
  });

  it("appendTail adds text to the ring buffer and peek returns it", async () => {
    const runtime = new AsyncToolRuntime();
    const work = makeManualCall();
    const handle = await runtime.spawn({
      conversationId: "conv-1",
      kind: "mcp",
      pluginId: "p",
      toolName: "t",
      args: {},
      work: () => work.promise,
    });
    runtime.appendTail(handle.handleId, "line 1\n");
    runtime.appendTail(handle.handleId, "line 2\n");
    const peek = runtime.peek(handle.handleId);
    expect(peek?.tail).toBe("line 1\nline 2\n");
    runtime.dispose();
  });

  it("ring buffer caps tail at maxBytes", async () => {
    const runtime = new AsyncToolRuntime({ tailMaxBytes: 10 });
    const work = makeManualCall();
    const handle = await runtime.spawn({
      conversationId: "conv-1",
      kind: "mcp",
      pluginId: "p",
      toolName: "t",
      args: {},
      work: () => work.promise,
    });
    runtime.appendTail(handle.handleId, "0123456789A");
    const peek = runtime.peek(handle.handleId);
    // Keeps the last 10 bytes.
    expect(peek?.tail.length).toBe(10);
    expect(peek?.tail).toBe("123456789A");
    runtime.dispose();
  });

  it("kill aborts the work signal", async () => {
    const runtime = new AsyncToolRuntime();
    let aborted = false;
    const work = makeManualCall();
    const handle = await runtime.spawn({
      conversationId: "conv-1",
      kind: "mcp",
      toolName: "t",
      args: {},
      work: (signal) => {
        signal.addEventListener("abort", () => { aborted = true; });
        return work.promise;
      },
    });
    runtime.kill(handle.handleId);
    expect(aborted).toBe(true);
    expect(runtime.peek(handle.handleId)?.status).toBe("killed");
    runtime.dispose();
  });

  it("killAllForConversation aborts all running handles", async () => {
    const runtime = new AsyncToolRuntime();
    const aborted: string[] = [];
    const work1 = makeManualCall();
    const work2 = makeManualCall();
    const h1 = await runtime.spawn({
      conversationId: "conv-1",
      kind: "mcp",
      toolName: "t1",
      args: {},
      work: (signal) => {
        signal.addEventListener("abort", () => aborted.push("h1"));
        return work1.promise;
      },
    });
    const h2 = await runtime.spawn({
      conversationId: "conv-1",
      kind: "mcp",
      toolName: "t2",
      args: {},
      work: (signal) => {
        signal.addEventListener("abort", () => aborted.push("h2"));
        return work2.promise;
      },
    });
    const count = runtime.killAllForConversation("conv-1");
    expect(count).toBe(2);
    expect(aborted).toContain("h1");
    expect(aborted).toContain("h2");
    expect(runtime.peek(h1.handleId)?.status).toBe("killed");
    expect(runtime.peek(h2.handleId)?.status).toBe("killed");
    runtime.dispose();
  });

  it("onProgress callback fires on appendTail", async () => {
    const progressCalls: Array<{ handleId: string; bytes: number }> = [];
    const runtime = new AsyncToolRuntime({
      onProgress: (handle, _tail, bytes) => progressCalls.push({ handleId: handle.handleId, bytes }),
    });
    const work = makeManualCall();
    const handle = await runtime.spawn({
      conversationId: "conv-1",
      kind: "mcp",
      toolName: "t",
      args: {},
      work: () => work.promise,
    });
    runtime.appendTail(handle.handleId, "hello\n");
    expect(progressCalls).toHaveLength(1);
    expect(progressCalls[0]!.handleId).toBe(handle.handleId);
    expect(progressCalls[0]!.bytes).toBe(6);
    runtime.dispose();
  });

  it("appendTail is ignored after handle settles", async () => {
    const progressCalls: number[] = [];
    const runtime = new AsyncToolRuntime({
      onProgress: (_h, _tail, bytes) => progressCalls.push(bytes),
    });
    const work = makeManualCall();
    const handle = await runtime.spawn({
      conversationId: "conv-1",
      kind: "mcp",
      toolName: "t",
      args: {},
      work: () => work.promise,
    });
    runtime.kill(handle.handleId);
    runtime.appendTail(handle.handleId, "should be ignored");
    expect(progressCalls).toHaveLength(0);
    runtime.dispose();
  });

  it("work rejection after abort is treated as killed, not failed", async () => {
    const runtime = new AsyncToolRuntime();
    let rejectFn: (err: Error) => void = () => {};
    const handle = await runtime.spawn({
      conversationId: "conv-1",
      kind: "mcp",
      toolName: "t",
      args: {},
      work: (_signal) => new Promise<unknown>((_resolve, reject) => { rejectFn = reject; }),
    });
    runtime.kill(handle.handleId);
    // Simulate the MCP call rejecting after abort.
    rejectFn(new Error("Cancelled by abort signal"));
    await new Promise((r) => setTimeout(r, 10));
    expect(runtime.peek(handle.handleId)?.status).toBe("killed");
    expect(runtime.peek(handle.handleId)?.endReason).toBe("killed");
    runtime.dispose();
  });
});
