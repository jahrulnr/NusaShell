/**
 * Redacts likely-sensitive values from tool call args, output, and error
 * strings before they cross the WS edge to the client.
 *
 * This is a defense-in-depth layer — the application should also avoid
 * logging secrets — but the WS mapper is the last choke point before data
 * reaches the renderer, so we scrub here as well.
 *
 * Redaction targets:
 * - Object keys matching secret-like names (password, token, apiKey, secret, …)
 * - Long hex/base64-like strings that look like API keys or bearer tokens
 * - `Bearer <token>` and `Authorization: <scheme> <value>` headers
 * - `sk-` prefixed strings (OpenAI-style API keys)
 */

const SECRET_KEY_PATTERNS = [
  /password/i,
  /passwd/i,
  /secret/i,
  /token/i,
  /api[_-]?key/i,
  /auth/i,
  /bearer/i,
  /credential/i,
  /private[_-]?key/i,
  /access[_-]?key/i,
  /client[_-]?secret/i,
  /refresh[_-]?token/i,
  /session[_-]?id/i,
  /cookie/i,
];

const REDACTED = "[REDACTED]";

const BEARER_PATTERN = /\bBearer\s+[A-Za-z0-9\-._~+\/=]{16,}/g;
const AUTH_HEADER_PATTERN = /\b(Authorization)\s*[:=]\s*"?[A-Za-z][A-Za-z0-9\-]*\s+[A-Za-z0-9\-._~+\/=]{16,}/gi;
const SK_KEY_PATTERN = /\bsk-[A-Za-z0-9\-_]{20,}/g;
const LONG_TOKEN_PATTERN = /\b[A-Za-z0-9+\/]{40,}={0,2}\b/g;

/**
 * Redacts sensitive values in a plain object (tool args). Mutates a copy.
 */
export function redactArgs<T extends Record<string, unknown>>(args: T): T {
  const out: Record<string, unknown> = {};
  for (const [key, value] of Object.entries(args)) {
    if (SECRET_KEY_PATTERNS.some((p) => p.test(key))) {
      out[key] = REDACTED;
    } else if (value && typeof value === "object" && !Array.isArray(value)) {
      out[key] = redactArgs(value as Record<string, unknown>);
    } else if (typeof value === "string") {
      out[key] = redactString(value);
    } else {
      out[key] = value;
    }
  }
  return out as T;
}

/**
 * Redacts sensitive patterns from a string (tool output, error messages).
 */
export function redactString(value: string): string {
  if (typeof value !== "string") return value;
  let out = value
    .replace(BEARER_PATTERN, "Bearer [REDACTED]")
    .replace(AUTH_HEADER_PATTERN, "Authorization: [REDACTED]")
    .replace(SK_KEY_PATTERN, "sk-[REDACTED]");
  // Only apply the long-token pattern if the string is short enough to be a
  // credential fragment, not a large base64 payload like an image.
  if (out.length < 2000) {
    out = out.replace(LONG_TOKEN_PATTERN, "[REDACTED]");
  }
  return out;
}

/**
 * Redacts an unknown value: recurses into objects, redacts strings, passes
 * through primitives. Returns the redacted copy.
 */
export function redactValue<T>(value: T): T {
  if (value === null || value === undefined) return value;
  if (typeof value === "string") return redactString(value) as unknown as T;
  if (Array.isArray(value)) return value.map(redactValue) as unknown as T;
  if (typeof value === "object") return redactArgs(value as Record<string, unknown>) as unknown as T;
  return value;
}
