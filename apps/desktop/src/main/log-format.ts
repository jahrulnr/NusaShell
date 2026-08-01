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

export function redactLogMessage(message: string): string {
  return message
    .replace(/([?&](?:token|password|secret|api[_-]?key|authorization)=)[^&\s]+/gi, "$1[REDACTED]")
    .replace(/((?:token|password|secret|api[_-]?key|authorization)["']?\s*[:=]\s*["']?)[^,\s}"']+/gi, "$1[REDACTED]");
}

export function formatLogArguments(args: readonly unknown[]): string {
  const seen = new WeakSet<object>();
  const message = args.map((arg) => {
    if (arg instanceof Error) return arg.stack ?? arg.message;
    if (typeof arg === "string") return arg;
    try {
      return JSON.stringify(serializeLogValue(arg, seen));
    } catch {
      return String(arg);
    }
  }).join(" ");
  return redactLogMessage(message);
}
