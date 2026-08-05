import { afterEach, describe, expect, it, vi } from "vitest";
import {
  IpcRequestTimeoutError,
  mapIpcFailureToResponse,
  raceWithTimeout,
} from "../src/main/ipc-bridge.js";

describe("IPC timeout mapping (no [object Object])", () => {
  afterEach(() => {
    vi.useRealTimers();
  });

  it("raceWithTimeout rejects with IpcRequestTimeoutError", async () => {
    vi.useFakeTimers();
    const pending = raceWithTimeout(new Promise(() => {}), 100, "req-1");
    const assertion = expect(pending).rejects.toMatchObject({
      name: "IpcRequestTimeoutError",
      code: "TIMEOUT",
      message: "IPC request timed out after 100ms",
      timeoutMs: 100,
    });
    await vi.advanceTimersByTimeAsync(100);
    await assertion;
  });

  it("mapIpcFailureToResponse keeps TIMEOUT message for IpcRequestTimeoutError", () => {
    const response = mapIpcFailureToResponse("req-2", new IpcRequestTimeoutError(1_800_000));
    expect(response).toEqual({
      kind: "response",
      id: "req-2",
      ok: false,
      error: {
        code: "TIMEOUT",
        message: "IPC request timed out after 1800000ms",
        details: { timeoutMs: 1_800_000 },
      },
    });
    expect(response.error.message).not.toContain("[object Object]");
  });

  it("mapIpcFailureToResponse upgrades legacy plain-object timeout envelopes", () => {
    // Pre-fix raceWithTimeout rejected a plain envelope; catch must not
    // stringify it as [object Object].
    const legacy = {
      kind: "response" as const,
      id: "req-x",
      ok: false as const,
      error: {
        code: "TIMEOUT",
        message: "IPC request timed out after 1800000ms",
      },
    };
    const response = mapIpcFailureToResponse("req-3", legacy);
    expect(response.error.code).toBe("TIMEOUT");
    expect(response.error.message).toBe("IPC request timed out after 1800000ms");
  });

  it("mapIpcFailureToResponse never emits String(object) garbage", () => {
    const response = mapIpcFailureToResponse("req-4", { weird: true });
    expect(response.error.message).toBe("IPC request failed");
    expect(response.error.message).not.toBe("[object Object]");
  });
});
