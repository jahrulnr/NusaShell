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
  readonly model: string | undefined;
  readonly baseUrl: string | undefined;
  readonly apiKey: string | undefined;
  readonly maxToolRounds: number;
}

export function loadConfig(env: Record<string, string | undefined> = process.env): AppConfig {
  return {
    port: parseInt(env.NUSASHELL_PORT ?? "9130", 10),
    host: env.NUSASHELL_HOST ?? "0.0.0.0",
    pluginsRoot: env.NUSASHELL_PLUGINS_ROOT,
    dbPath: env.NUSASHELL_DB_PATH,
    logLevel: env.NUSASHELL_LOG_LEVEL ?? "info",
    ai: {
      providerId: env.NUSASHELL_AI_PROVIDER ?? "stub",
      model: env.NUSASHELL_AI_MODEL,
      baseUrl: env.NUSASHELL_AI_BASE_URL,
      apiKey: env.NUSASHELL_AI_API_KEY,
      maxToolRounds: parseMaxToolRounds(env.NUSASHELL_AI_MAX_TOOL_ROUNDS),
    },
  };
}

function parseMaxToolRounds(value: string | undefined): number {
  if (value === undefined) return 8;
  const parsed = Number.parseInt(value, 10);
  return Number.isInteger(parsed) && parsed >= 1 && parsed <= 32 ? parsed : 8;
}
