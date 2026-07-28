export interface AppConfig {
  readonly port: number;
  readonly host: string;
  readonly pluginsRoot: string | undefined;
  readonly dbPath: string | undefined;
  readonly logLevel: string;
  readonly ai: AiConfig;
}

export interface AiConfig {
  readonly providerId: string;
  readonly stubEnabled: boolean;
  readonly api: "chat" | "responses" | "messages" | undefined;
  readonly model: string | undefined;
  readonly baseUrl: string | undefined;
  readonly apiKey: string | undefined;
  readonly maxToolRounds: number;
  readonly strategy: "failover" | "round-robin" | "switch";
  readonly totalAttemptBudget: number;
  readonly stream: boolean;
  readonly vision: "auto" | "on" | "off";
  readonly timeoutMs: number;
  readonly retry: {
    readonly attemptBudget: number;
    readonly baseDelayMs: number;
    readonly maxDelayMs: number;
    readonly jitter: number;
  };
  readonly context: {
    readonly compactionEnabled: boolean;
    readonly maxInputTokens: number;
    readonly reserveTokens: number;
    readonly recentTurns: number;
    readonly summaryMaxChars: number;
  };
}

export function loadConfig(env: Record<string, string | undefined> = process.env): AppConfig {
  return {
    port: parseInt(env.NUSASHELL_PORT ?? "9130", 10),
    host: env.NUSASHELL_HOST ?? "0.0.0.0",
    pluginsRoot: env.NUSASHELL_PLUGINS_ROOT,
    dbPath: env.NUSASHELL_DB_PATH,
    logLevel: env.NUSASHELL_LOG_LEVEL ?? "info",
    ai: {
      providerId: env.NUSASHELL_AI_PROVIDER ?? "",
      stubEnabled: env.NUSASHELL_AI_STUB === "true",
      api: parseAiApi(env.NUSASHELL_AI_API),
      model: env.NUSASHELL_AI_MODEL,
      baseUrl: env.NUSASHELL_AI_BASE_URL,
      apiKey: env.NUSASHELL_AI_API_KEY,
      maxToolRounds: parseMaxToolRounds(env.NUSASHELL_AI_MAX_TOOL_ROUNDS),
      strategy: parseAiStrategy(env.NUSASHELL_AI_STRATEGY),
      totalAttemptBudget: integerInRange(env.NUSASHELL_AI_TOTAL_ATTEMPT_BUDGET, 1, 32, 4),
      stream: env.NUSASHELL_AI_STREAM !== "false",
      vision: env.NUSASHELL_AI_VISION === "on" || env.NUSASHELL_AI_VISION === "off"
        ? env.NUSASHELL_AI_VISION
        : "auto",
      timeoutMs: integerInRange(env.NUSASHELL_AI_TIMEOUT_MS, 1000, 600_000, 60_000),
      retry: {
        attemptBudget: integerInRange(env.NUSASHELL_AI_RETRY_ATTEMPTS, 1, 10, 4),
        baseDelayMs: integerInRange(env.NUSASHELL_AI_RETRY_BASE_DELAY_MS, 0, 60_000, 250),
        maxDelayMs: integerInRange(env.NUSASHELL_AI_RETRY_MAX_DELAY_MS, 1, 120_000, 5000),
        jitter: floatInRange(env.NUSASHELL_AI_RETRY_JITTER, 0, 1, 0.2),
      },
      context: {
        compactionEnabled: env.NUSASHELL_AI_CONTEXT_COMPACTION !== "false",
        maxInputTokens: integerInRange(env.NUSASHELL_AI_CONTEXT_MAX_INPUT_TOKENS, 1000, 2_000_000, 12000),
        reserveTokens: integerInRange(env.NUSASHELL_AI_CONTEXT_RESERVE_TOKENS, 0, 1_000_000, 3000),
        recentTurns: integerInRange(env.NUSASHELL_AI_CONTEXT_RECENT_TURNS, 1, 100, 4),
        summaryMaxChars: integerInRange(env.NUSASHELL_AI_CONTEXT_SUMMARY_MAX_CHARS, 100, 1_000_000, 12000),
      },
    },
  };
}

function parseAiStrategy(value: string | undefined): AiConfig["strategy"] {
  return value === "round-robin" || value === "switch" || value === "failover"
    ? value
    : "failover";
}

function parseAiApi(value: string | undefined): AiConfig["api"] {
  return value === "chat" || value === "responses" || value === "messages" ? value : undefined;
}

function parseMaxToolRounds(value: string | undefined): number {
  return integerInRange(value, 1, 100, 50);
}

function integerInRange(value: string | undefined, min: number, max: number, fallback: number): number {
  if (value === undefined) return fallback;
  const parsed = Number.parseInt(value, 10);
  return Number.isInteger(parsed) && parsed >= min && parsed <= max ? parsed : fallback;
}

function floatInRange(value: string | undefined, min: number, max: number, fallback: number): number {
  if (value === undefined) return fallback;
  const parsed = Number(value);
  return Number.isFinite(parsed) && parsed >= min && parsed <= max ? parsed : fallback;
}
