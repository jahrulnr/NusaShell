import { describe, expect, it } from "vitest";
import { StreamSeqRegistry } from "../src/agent/services/stream-seq-registry.js";

describe("StreamSeqRegistry", () => {
  it("assigns a monotonic streamSeq starting at 1 per traceId", () => {
    const registry = new StreamSeqRegistry();
    expect(registry.next("t1")).toBe(1);
    expect(registry.next("t1")).toBe(2);
    expect(registry.next("t1")).toBe(3);
    expect(registry.peek("t1")).toBe(3);
  });

  it("keeps counters independent per traceId", () => {
    const registry = new StreamSeqRegistry();
    registry.next("t1");
    registry.next("t1");
    expect(registry.next("t2")).toBe(1);
    expect(registry.next("t2")).toBe(2);
    expect(registry.peek("t1")).toBe(2);
    expect(registry.peek("t2")).toBe(2);
  });

  it("peeks 0 for an unknown traceId", () => {
    const registry = new StreamSeqRegistry();
    expect(registry.peek("unknown")).toBe(0);
  });

  it("clears the counter so the next call restarts at 1", () => {
    const registry = new StreamSeqRegistry();
    registry.next("t1");
    registry.next("t1");
    registry.clear("t1");
    expect(registry.peek("t1")).toBe(0);
    expect(registry.next("t1")).toBe(1);
  });

  it("clear is a no-op for an unknown traceId", () => {
    const registry = new StreamSeqRegistry();
    expect(() => registry.clear("unknown")).not.toThrow();
  });
});
