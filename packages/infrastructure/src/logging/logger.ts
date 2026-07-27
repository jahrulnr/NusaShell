import pino, { type Logger } from "pino";

export function createLogger(level: string = "info"): Logger {
  return pino({ level });
}

export type { Logger } from "pino";
