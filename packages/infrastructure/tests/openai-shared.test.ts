import { describe, expect, it } from "vitest";
import { setMaxListeners } from "node:events";
import { abortableSleep } from "../src/ai/openai-shared.js";

describe("abortableSleep", () => {
  it("does not leak abort listeners on the parent signal after normal completion", async () => {
    const controller = new AbortController();
    // Cap at 2 listeners. abortableSleep is called 5 times sequentially;
    // if each call leaks its listener, the 3rd call would trip the
    // MaxListenersExceededWarning. We capture warnings to detect the leak.
    setMaxListeners(2, controller.signal);
    const warnings: string[] = [];
    const onWarning = (warning: Error) => {
      if (warning.name === "MaxListenersExceededWarning") warnings.push(warning.message);
    };
    process.on("warning", onWarning);
    try {
      for (let i = 0; i < 5; i += 1) {
        // eslint-disable-next-line no-await-in-loop
        await abortableSleep(1, controller.signal);
      }
    } finally {
      process.off("warning", onWarning);
    }
    expect(warnings).toEqual([]);
  });

  it("still resolves immediately when delayMs <= 0 without touching the signal", async () => {
    const controller = new AbortController();
    await expect(abortableSleep(0, controller.signal)).resolves.toBeUndefined();
    await expect(abortableSleep(-10, controller.signal)).resolves.toBeUndefined();
  });

  it("still aborts promptly when the parent signal fires during the sleep", async () => {
    const controller = new AbortController();
    const promise = abortableSleep(1000, controller.signal);
    controller.abort(new Error("cancelled"));
    await expect(promise).resolves.toBeUndefined();
  });
});
