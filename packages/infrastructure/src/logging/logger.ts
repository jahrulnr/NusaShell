import { createWriteStream, mkdirSync } from "node:fs";
import { dirname } from "node:path";
import pino, { type Logger, type StreamEntry } from "pino";

export interface LogRecord {
  readonly level: string;
  readonly args: readonly unknown[];
}

export type LogObserver = (record: LogRecord) => void;

const levelNames: Readonly<Record<number, string>> = {
  10: "trace",
  20: "debug",
  30: "info",
  40: "warn",
  50: "error",
  60: "fatal",
};

export interface CreateLoggerOptions {
  readonly level?: string;
  readonly observer?: LogObserver;
  readonly logFile?: string;
}

export function createLogger(
  levelOrOptions: string | CreateLoggerOptions = "info",
  observer?: LogObserver,
): Logger {
  const opts: CreateLoggerOptions = typeof levelOrOptions === "string"
    ? { level: levelOrOptions, ...(observer ? { observer } : {}) }
    : levelOrOptions;
  const level = opts.level ?? "info";

  const streams: StreamEntry[] = [{ level: level as pino.Level, stream: process.stdout }];

  if (opts.logFile) {
    try {
      mkdirSync(dirname(opts.logFile), { recursive: true });
      streams.push({ level: level as pino.Level, stream: createWriteStream(opts.logFile, { flags: "a" }) });
    } catch {
      // File logging is best-effort; don't crash startup if the path is bad.
    }
  }

  const baseConfig = {
    level,
    ...(opts.observer
      ? {
          hooks: {
            logMethod(args: unknown[], method: (...a: unknown[]) => void, logLevel: number) {
              opts.observer!({ level: levelNames[logLevel] ?? "info", args });
              method.apply(this, args);
            },
          },
        }
      : {}),
  };

  if (streams.length === 1) {
    return pino(baseConfig);
  }
  return pino(baseConfig, pino.multistream(streams));
}

export type { Logger } from "pino";
