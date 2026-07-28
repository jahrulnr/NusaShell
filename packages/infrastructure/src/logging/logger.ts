import pino, { type Logger } from "pino";

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

export function createLogger(level: string = "info", observer?: LogObserver): Logger {
  if (!observer) return pino({ level });

  return pino({
    level,
    hooks: {
        logMethod(args, method, logLevel) {
          observer({ level: levelNames[logLevel] ?? "info", args });
          method.apply(this, args);
        },
      },
  });
}

export type { Logger } from "pino";
