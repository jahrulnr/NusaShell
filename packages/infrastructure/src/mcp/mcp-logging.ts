import type { Client } from "@modelcontextprotocol/sdk/client/index.js";
import { LoggingMessageNotificationSchema } from "@modelcontextprotocol/sdk/types.js";
import type { Logger } from "pino";

export function registerMcpLogging(client: Client, logger: Logger | undefined, source: string): void {
  client.setNotificationHandler(LoggingMessageNotificationSchema, (notification) => {
    if (!logger) return;
    const fields = {
      source: redactMcpText(source),
      logger: notification.params.logger ? redactMcpText(notification.params.logger).slice(0, 200) : undefined,
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

export function redactMcpText(value: string): string {
  return value
    .replace(/([?&](?:token|password|secret|api[_-]?key|authorization)=)[^&\s]+/gi, "$1[REDACTED]")
    .replace(/((?:token|password|secret|api[_-]?key|authorization)["']?\s*[:=]\s*["']?)[^,\s}"']+/gi, "$1[REDACTED]")
    .slice(0, 4000);
}

function formatMcpLogData(value: unknown): string {
  if (typeof value === "string") return redactMcpText(value);
  try {
    return redactMcpText(JSON.stringify(value));
  } catch {
    return "[unserializable MCP log data]";
  }
}
