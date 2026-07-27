export interface AppConfig {
  readonly port: number;
  readonly host: string;
  readonly pluginsRoot: string | undefined;
  readonly dbPath: string | undefined;
  readonly logLevel: string;
}

export function loadConfig(env: Record<string, string | undefined> = process.env): AppConfig {
  return {
    port: parseInt(env.NUSASHELL_PORT ?? "9130", 10),
    host: env.NUSASHELL_HOST ?? "0.0.0.0",
    pluginsRoot: env.NUSASHELL_PLUGINS_ROOT,
    dbPath: env.NUSASHELL_DB_PATH,
    logLevel: env.NUSASHELL_LOG_LEVEL ?? "info",
  };
}
