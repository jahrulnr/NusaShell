import { describe, expect, it } from "vitest";
import { formatLogArguments } from "../src/main/log-format.js";

describe("main-process log formatting", () => {
  it("preserves nested Error details received from the backend logger", () => {
    const error = Object.assign(new Error("spawn node ENOENT"), { code: "ENOENT" });

    const message = formatLogArguments([{ err: error }, "Plugin startup failed"]);

    expect(message).toContain("spawn node ENOENT");
    expect(message).toContain("ENOENT");
    expect(message).toContain("Plugin startup failed");
  });

  it("keeps error details when an Error cause is circular", () => {
    const error = new Error("plugin process crashed");
    error.cause = error;

    const message = formatLogArguments([{ err: error }]);

    expect(message).toContain("plugin process crashed");
    expect(message).toContain("[Circular]");
  });
});
