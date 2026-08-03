import { format } from "node:util";

function serializeLogValue(value: unknown, seen: WeakSet<object>): unknown {
  if (value && typeof value === "object") {
    if (seen.has(value)) return "[Circular]";
    seen.add(value);
    try {
      if (value instanceof Error) {
        const details: Record<string, unknown> = {
          name: value.name,
          message: value.message,
          ...(value.stack ? { stack: value.stack } : {}),
          ...(value.cause !== undefined
            ? { cause: serializeLogValue(value.cause, seen) }
            : {}),
        };
        for (const [key, nestedValue] of Object.entries(value)) {
          if (key !== "cause") details[key] = serializeLogValue(nestedValue, seen);
        }
        return details;
      }

      if (Array.isArray(value)) {
        return value.map((entry) => serializeLogValue(entry, seen));
      }

      return Object.fromEntries(
        Object.entries(value).map(([key, nestedValue]) => [
          key,
          serializeLogValue(nestedValue, seen),
        ]),
      );
    } finally {
      seen.delete(value);
    }
  }

  return value;
}

function serializeArg(arg: unknown, seen: WeakSet<object>): unknown {
  if (arg instanceof Error) return arg.stack ?? arg.message;
  if (typeof arg === "string") return arg;
  try {
    return JSON.stringify(serializeLogValue(arg, seen));
  } catch {
    return String(arg);
  }
}

export function formatLogArguments(args: readonly unknown[]): string {
  const seen = new WeakSet<object>();

  // Message-first pino shape: logger.warn("msg %s", value)
  // Apply util.format so printf placeholders interpolate instead of leaving
  // literal %s/%d with values appended.
  if (typeof args[0] === "string") {
    return format(args[0], ...args.slice(1).map((arg) => serializeArg(arg, seen)));
  }

  // Bindings-first pino shape: logger.warn({ err }, "msg %s", value)
  // Serialize the bindings object, then interpolate the message.
  if (args[0] && typeof args[0] === "object" && !(args[0] instanceof Error) && typeof args[1] === "string") {
    const bindings = serializeArg(args[0], seen);
    const message = format(args[1], ...args.slice(2).map((arg) => serializeArg(arg, seen)));
    return `${bindings} ${message}`;
  }

  // Fallback: no string message — join serialized args (e.g. formatLogArguments([{ err }])).
  return args.map((arg) => serializeArg(arg, seen)).join(" ");
}
