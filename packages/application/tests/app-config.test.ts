import { describe, expect, it } from "vitest";
import { loadConfig } from "../src/config/app-config.js";

describe("loadConfig", () => {
  it("returns defaults when no env vars set", () => {
    const config = loadConfig({});
    expect(config.port).toBe(9130);
    expect(config.host).toBe("0.0.0.0");
    expect(config.pluginsRoot).toBeUndefined();
    expect(config.dbPath).toBeUndefined();
    expect(config.logLevel).toBe("info");
  });

  it("reads port from NUSASHELL_PORT", () => {
    const config = loadConfig({ NUSASHELL_PORT: "8080" });
    expect(config.port).toBe(8080);
  });

  it("reads host from NUSASHELL_HOST", () => {
    const config = loadConfig({ NUSASHELL_HOST: "127.0.0.1" });
    expect(config.host).toBe("127.0.0.1");
  });

  it("reads pluginsRoot from NUSASHELL_PLUGINS_ROOT", () => {
    const config = loadConfig({ NUSASHELL_PLUGINS_ROOT: "/plugins" });
    expect(config.pluginsRoot).toBe("/plugins");
  });

  it("reads dbPath from NUSASHELL_DB_PATH", () => {
    const config = loadConfig({ NUSASHELL_DB_PATH: "/data/nusa.db" });
    expect(config.dbPath).toBe("/data/nusa.db");
  });

  it("reads logLevel from NUSASHELL_LOG_LEVEL", () => {
    const config = loadConfig({ NUSASHELL_LOG_LEVEL: "debug" });
    expect(config.logLevel).toBe("debug");
  });

  it("reads all env vars together", () => {
    const config = loadConfig({
      NUSASHELL_PORT: "3000",
      NUSASHELL_HOST: "localhost",
      NUSASHELL_PLUGINS_ROOT: "/app/plugins",
      NUSASHELL_DB_PATH: "/app/data.db",
      NUSASHELL_LOG_LEVEL: "warn",
    });
    expect(config).toEqual({
      port: 3000,
      host: "localhost",
      pluginsRoot: "/app/plugins",
      dbPath: "/app/data.db",
      logLevel: "warn",
    });
  });
});
