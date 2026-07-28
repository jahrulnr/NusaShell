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
    expect(config.ai).toEqual({
      providerId: "",
      stubEnabled: false,
      api: undefined,
      model: undefined,
      baseUrl: undefined,
      apiKey: undefined,
      maxToolRounds: 8,
      strategy: "failover",
      totalAttemptBudget: 4,
      stream: true,
      vision: "auto",
      timeoutMs: 60000,
      retry: { attemptBudget: 4, baseDelayMs: 250, maxDelayMs: 5000, jitter: 0.2 },
      context: { compactionEnabled: true, maxInputTokens: 12000, reserveTokens: 3000, recentTurns: 4, summaryMaxChars: 12000 },
    });
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
      NUSASHELL_AI_PROVIDER: "openai-compatible",
      NUSASHELL_AI_STUB: "true",
      NUSASHELL_AI_API: "responses",
      NUSASHELL_AI_MODEL: "gpt-test",
      NUSASHELL_AI_BASE_URL: "https://example.test/v1",
      NUSASHELL_AI_API_KEY: "not-a-real-key",
      NUSASHELL_AI_MAX_TOOL_ROUNDS: "4",
      NUSASHELL_AI_RETRY_ATTEMPTS: "3",
      NUSASHELL_AI_RETRY_BASE_DELAY_MS: "100",
      NUSASHELL_AI_RETRY_MAX_DELAY_MS: "900",
      NUSASHELL_AI_RETRY_JITTER: "0.1",
      NUSASHELL_AI_CONTEXT_COMPACTION: "false",
      NUSASHELL_AI_CONTEXT_MAX_INPUT_TOKENS: "8000",
      NUSASHELL_AI_CONTEXT_RESERVE_TOKENS: "2000",
      NUSASHELL_AI_CONTEXT_RECENT_TURNS: "2",
      NUSASHELL_AI_CONTEXT_SUMMARY_MAX_CHARS: "6000",
    });
    expect(config).toEqual({
      port: 3000,
      host: "localhost",
      pluginsRoot: "/app/plugins",
      dbPath: "/app/data.db",
      logLevel: "warn",
      ai: {
        providerId: "openai-compatible",
        stubEnabled: true,
        api: "responses",
        model: "gpt-test",
        baseUrl: "https://example.test/v1",
        apiKey: "not-a-real-key",
        maxToolRounds: 4,
        strategy: "failover",
        totalAttemptBudget: 4,
        stream: true,
        vision: "auto",
        timeoutMs: 60000,
        retry: { attemptBudget: 3, baseDelayMs: 100, maxDelayMs: 900, jitter: 0.1 },
        context: { compactionEnabled: false, maxInputTokens: 8000, reserveTokens: 2000, recentTurns: 2, summaryMaxChars: 6000 },
      },
    });
  });
});
