import { describe, expect, it } from "vitest";
import { SystemClock } from "../src/system/system-clock.adapter.js";

describe("SystemClock", () => {
  it("returns a Date close to now", () => {
    const clock = new SystemClock();
    const before = Date.now();
    const result = clock.now();
    const after = Date.now();

    expect(result).toBeInstanceOf(Date);
    expect(result.getTime()).toBeGreaterThanOrEqual(before);
    expect(result.getTime()).toBeLessThanOrEqual(after);
  });
});
