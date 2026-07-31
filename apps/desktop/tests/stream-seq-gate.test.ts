import { describe, expect, it } from "vitest";
import { createStreamSeqGate } from "../src/renderer/stream-seq-gate.js";

describe("createStreamSeqGate", () => {
  it("accepts events in order and tracks the last seen streamSeq per traceId", () => {
    const gate = createStreamSeqGate();
    expect(gate.check("t1", 1)).toEqual({ accept: true, gap: false });
    expect(gate.check("t1", 2)).toEqual({ accept: true, gap: false });
    expect(gate.lastSeen("t1")).toBe(2);
  });

  it("drops stale and duplicate streamSeq values", () => {
    const gate = createStreamSeqGate();
    gate.check("t1", 3);
    expect(gate.check("t1", 3)).toEqual({ accept: false, gap: false });
    expect(gate.check("t1", 2)).toEqual({ accept: false, gap: false });
    expect(gate.check("t1", 1)).toEqual({ accept: false, gap: false });
    expect(gate.lastSeen("t1")).toBe(3);
  });

  it("flags a gap when streamSeq jumps by more than one", () => {
    const gate = createStreamSeqGate();
    expect(gate.check("t1", 1)).toEqual({ accept: true, gap: false });
    expect(gate.check("t1", 4)).toEqual({ accept: true, gap: true });
    expect(gate.lastSeen("t1")).toBe(4);
  });

  it("keeps counters independent per traceId", () => {
    const gate = createStreamSeqGate();
    gate.check("t1", 1);
    gate.check("t1", 2);
    expect(gate.check("t2", 1)).toEqual({ accept: true, gap: false });
    expect(gate.check("t2", 2)).toEqual({ accept: true, gap: false });
    expect(gate.lastSeen("t1")).toBe(2);
    expect(gate.lastSeen("t2")).toBe(2);
  });

  it("accepts events without a streamSeq without advancing the counter", () => {
    const gate = createStreamSeqGate();
    expect(gate.check("t1", undefined)).toEqual({ accept: true, gap: false });
    expect(gate.check("t1", 1)).toEqual({ accept: true, gap: false });
    expect(gate.check("t1", undefined)).toEqual({ accept: true, gap: false });
    expect(gate.lastSeen("t1")).toBe(1);
  });

  it("clears the counter for a traceId", () => {
    const gate = createStreamSeqGate();
    gate.check("t1", 5);
    gate.clear("t1");
    expect(gate.lastSeen("t1")).toBe(0);
    // After clear, seq 1 is accepted again (new stream).
    expect(gate.check("t1", 1)).toEqual({ accept: true, gap: false });
  });

  it("treats NaN streamSeq as no-seq (accepted, no advance)", () => {
    const gate = createStreamSeqGate();
    expect(gate.check("t1", Number.NaN)).toEqual({ accept: true, gap: false });
    expect(gate.lastSeen("t1")).toBe(0);
  });
});
