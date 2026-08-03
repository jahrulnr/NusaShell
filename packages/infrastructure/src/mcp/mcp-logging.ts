import type { Client } from "@modelcontextprotocol/sdk/client/index.js";
import { LoggingMessageNotificationSchema } from "@modelcontextprotocol/sdk/types.js";
import type { Logger } from "pino";

export function registerMcpLogging(client: Client, logger: Logger | undefined, source: string): void {
  client.setNotificationHandler(LoggingMessageNotificationSchema, (notification) => {
    if (!logger) return;
    const fields = {
      source: boundMcpText(source),
      logger: notification.params.logger ? boundMcpText(notification.params.logger).slice(0, 200) : undefined,
      mcpLevel: notification.params.level,
      message: formatMcpLogData(notification.params.data),
    };
    switch (notification.params.level) {
      case "debug":
        logger.debug(fields, "MCP protocol log");
        break;
      case "info":
      case "notice":
        logger.info(fields, "MCP protocol log");
        break;
      case "warning":
        logger.warn(fields, "MCP protocol log");
        break;
      default:
        logger.error(fields, "MCP protocol log");
    }
  });
}

/**
 * Truncate MCP protocol log notifications to a bounded size for flood control.
 * No pattern-based secret scrubbing — NusaShell is not a secret-filter product
 * (see docs/architecture/security-boundary.md). Users may intentionally paste
 * credentials; false positives on base64/hash/MD5/SHA are unacceptable.
 */
export function boundMcpText(value: string): string {
  return value.slice(0, 4000);
}

/**
 * Truncate child-process stderr to a bounded size for flood control. No
 * pattern-based secret scrubbing — stderr is diagnostic output (crash dumps,
 * stack traces, JSON-RPC frames) and must pass through verbatim for debugging.
 */
export function boundMcpStderr(value: string): string {
  return value.slice(0, 8192);
}

function formatMcpLogData(value: unknown): string {
  if (typeof value === "string") return boundMcpText(value);
  try {
    return boundMcpText(JSON.stringify(value));
  } catch {
    return "[unserializable MCP log data]";
  }
}
