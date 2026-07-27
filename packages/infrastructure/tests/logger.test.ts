import { describe, expect, it } from "vitest";
import { createLogger } from "../src/logging/logger.js";

describe("createLogger", () => {
  it("creates a pino logger with default level", () => {
    const logger = createLogger();
    expect(logger).toBeDefined();
    expect(logger.level).toBe("info");
  });

  it("creates a pino logger with custom level", () => {
    const logger = createLogger("debug");
    expect(logger.level).toBe("debug");
  });

  it("logger has standard methods", () => {
    const logger = createLogger("silent");
    expect(typeof logger.info).toBe("function");
    expect(typeof logger.warn).toBe("function");
    expect(typeof logger.error).toBe("function");
    expect(typeof logger.debug).toBe("function");
  });
});
