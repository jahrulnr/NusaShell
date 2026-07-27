import { describe, expect, it } from "vitest";
import { RequestManager } from "../src/client/request-manager.js";
import { RequestTimeoutError } from "../src/errors/request-timeout.error.js";
import { ConnectionClosedError } from "../src/errors/connection-closed.error.js";

describe("RequestManager", () => {
  it("resolves a registered request", async () => {
    const manager = new RequestManager(5000);
    const promise = manager.register("req_001");
    manager.resolve("req_001", { data: "test" });

    const result = await promise;
    expect(result).toEqual({ data: "test" });
  });

  it("rejects a registered request", async () => {
    const manager = new RequestManager(5000);
    const promise = manager.register("req_002");
    manager.reject("req_002", new Error("fail"));

    await expect(promise).rejects.toThrow("fail");
  });

  it("times out after the specified duration", async () => {
    const manager = new RequestManager(5000);
    const promise = manager.register("req_003", 50);

    await expect(promise).rejects.toThrow(RequestTimeoutError);
    expect(manager.has("req_003")).toBe(false);
  });

  it("rejects all pending on close", async () => {
    const manager = new RequestManager(5000);
    const p1 = manager.register("req_004");
    const p2 = manager.register("req_005");

    manager.close();

    await expect(p1).rejects.toThrow(ConnectionClosedError);
    await expect(p2).rejects.toThrow(ConnectionClosedError);
    expect(manager.size).toBe(0);
  });

  it("returns false when resolving unknown request", () => {
    const manager = new RequestManager(5000);
    expect(manager.resolve("unknown", null)).toBe(false);
  });

  it("returns false when rejecting unknown request", () => {
    const manager = new RequestManager(5000);
    expect(manager.reject("unknown", new Error("x"))).toBe(false);
  });

  it("tracks pending size", () => {
    const manager = new RequestManager(5000);
    manager.register("req_006");
    manager.register("req_007");
    expect(manager.size).toBe(2);
    manager.resolve("req_006", null);
    expect(manager.size).toBe(1);
  });
});
