/**
 * Shared MCP connect plumbing for network transports (HTTP streamable and SSE).
 *
 * Unlike the stdio adapter (which races connect against transport close and a
 * spawn-timeout then enriches with a stderr tail), the remote transports have no
 * subprocess stderr to harvest. Instead:
 *
 * - `connect` is raced against an overall timeout (default 5 min, override
 *   `NUSASHELL_MCP_CONNECT_TIMEOUT` in ms) so a hung/never-answering remote
 *   server fails loudly instead of parking the plugin start.
 * - Any failure is enriched with the redacted endpoint URL + a hint for the
 *   common remote failure classes (DNS / connect-refused / HTTP status).
 *
 * The redaction strips userinfo (credentials) from the URL before it appears in
 * error text or logs, matching the stdio adapter's discipline of never leaking
 * spawn env into connect errors.
 */

/**
 * Read the connect timeout from env on every call (default 5 min). Reading it
 * per-call (rather than freezing at module load) mirrors stdio's behavior and
 * lets tests / runtime observers change it without re-importing.
 */
export function remoteConnectTimeoutMs(): number {
  return Number(process.env.NUSASHELL_MCP_CONNECT_TIMEOUT) || 300_000;
}

/** Extract http(s) status from a transport error when the SDK surfaces it as `code`. */
function statusCodeOf(error: unknown): number | null {
  if (typeof error !== "object" || error === null) return null;
  const code = (error as { code?: unknown }).code;
  return typeof code === "number" && Number.isInteger(code) ? code : null;
}

/** Strip userinfo (user:password@) from a URL so credentials never leak. */
export function redactUrl(raw: string): string {
  try {
    const url = new URL(raw);
    if (url.username || url.password) {
      url.username = "";
      url.password = "";
    }
    return url.toString();
  } catch {
    // Not parseable as a URL (shouldn't happen, adapters already did new URL).
    return raw;
  }
}

function hintForCode(code: string): string {
  switch (code) {
    case "ECONNREFUSED":
      return "Connection refused — is the MCP server running and listening on that host/port?";
    case "ENOTFOUND":
    case "EAI_AGAIN":
      return "Could not resolve the server hostname (DNS). Check the URL host and network.";
    case "ECONNRESET":
    case "EPIPE":
      return "Connection reset by the server during handshake — the server may have crashed or rejected the protocol.";
    case "ETIMEDOUT":
      return "Network request timed out — host unreachable or firewall dropping packets.";
    case "CERT_HAS_EXPIRED":
    case "DEPTH_ZERO_SELF_SIGNED_CERT":
    case "UNABLE_TO_VERIFY_LEAF_SIGNATURE":
    case "SELF_SIGNED_CERT_IN_CHAIN":
      return "TLS certificate verification failed — check the server certificate or any CA trust configuration.";
    default:
      return "";
  }
}

function hintForStatus(status: number): string {
  switch (status) {
    case 401:
      return "Server returned 401 Unauthorized — check the auth header / token configured for this plugin.";
    case 403:
      return "Server returned 403 Forbidden — the credentials are valid but lack permission.";
    case 404:
      return "Server returned 404 Not Found — the endpoint URL may be wrong.";
    case 405:
      return "Server returned 405 Method Not Allowed — the URL is not an MCP endpoint (wrong path?).";
    default:
      return status >= 400
        ? `Server returned HTTP ${status} — check the endpoint URL and server state.`
        : "";
  }
}

export interface RemoteConnectOptions {
  /** Called when the timeout fires, before the error is thrown — lets the caller abort/close the in-flight transport. */
  onTimeout?: () => void;
  logger?: { debug: (obj: unknown, msg: string) => void };
}

/**
 * Race a remote MCP connect against a timeout and enrich failures with the
 * redacted URL + a transport hint. When the timeout fires, `onTimeout` runs so
 * the caller can close the SDK transport (which would otherwise keep trying to
 * reconnect in the background and could surface a late unhandled rejection).
 */
export async function connectWithTimeout(
  url: string,
  connect: () => Promise<void>,
  options: RemoteConnectOptions = {},
): Promise<void> {
  let timeoutFired = false;
  let timeoutHandle: ReturnType<typeof setTimeout> | null = null;
  let connectPromise: Promise<void>;
  const guardedConnect = Promise.resolve()
    .then(connect)
    .catch((error) => {
      if (!timeoutFired) throw error;
      // The connect that lost the race (timeout already threw) is swallowed so
      // it never becomes an unhandled rejection.
    });
  connectPromise = guardedConnect;
  try {
    await Promise.race([
      connectPromise,
      new Promise<never>((_, reject) => {
        timeoutHandle = setTimeout(() => {
          timeoutFired = true;
          options.onTimeout?.();
          reject(
            new Error(
              `MCP connect timed out after ${remoteConnectTimeoutMs()}ms`,
            ),
          );
        }, remoteConnectTimeoutMs());
      }),
    ]);
  } catch (error) {
    const base = error instanceof Error ? error.message : String(error);
    const parts = [base];
    const status = statusCodeOf(error);
    const code =
      typeof error === "object" && error !== null && "code" in error
        ? String((error as { code?: unknown }).code ?? "")
        : "";
    const hint = status !== null ? hintForStatus(status) : hintForCode(code);
    if (hint && !base.includes(hint)) parts.push(hint);
    const redacted = redactUrl(url);
    if (redacted && !base.includes("MCP connect timed out")) {
      parts.push(redacted);
    }
    if (!base.includes("MCP connect timed out")) {
      options.logger?.debug({ url: redacted, err: base }, "MCP connect failed");
    }
    throw new Error(parts.join("\n"), { cause: error });
  } finally {
    if (timeoutHandle) clearTimeout(timeoutHandle);
  }
}
