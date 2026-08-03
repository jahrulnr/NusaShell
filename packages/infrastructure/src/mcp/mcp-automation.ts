import type { Client } from "@modelcontextprotocol/sdk/client/index.js";
import { ResourceUpdatedNotificationSchema } from "@modelcontextprotocol/sdk/types.js";
import { z } from "zod";
import type { EventDispatcher, AutomationEvent, AutomationEmitRegistry, AutomationRateLimiterPort } from "@nusashell/application";
import { createAutomationEvent } from "@nusashell/application";
import type { Logger } from "pino";

/**
 * Zod schema for the NusaShell-namespaced automation notification.
 *
 * This is NOT an MCP core method — it is a convention namespaced under
 * `notifications/nusashell/` to avoid collision with future MCP standards.
 * The SDK's `setNotificationHandler` requires a Zod schema, so we define one
 * here with the same shape the SDK uses for core notifications.
 */
const NusashellAutomationNotificationSchema = z.object({
  method: z.literal("notifications/nusashell/automation"),
  params: z.object({
    type: z.string().min(1).max(200),
    payload: z.unknown(),
  }),
});

export interface RegisterMcpAutomationDeps {
  readonly eventDispatcher: EventDispatcher;
  readonly emitRegistry: AutomationEmitRegistry;
  readonly rateLimiter: AutomationRateLimiterPort;
  readonly logger?: Logger;
}

/**
 * Register MCP notification handlers for automation intake.
 *
 * Two paths:
 * 1. `notifications/resources/updated` (standard MCP) → `resource.updated` event
 * 2. `notifications/nusashell/automation` (NusaShell convention) → `AutomationEvent`
 *
 * Both paths:
 * - Bind `pluginId` from the connection identity (never from params).
 * - Enforce per-plugin rate limits (token bucket) before publishing.
 * - Bound payload to 64 KB (truncate + log, do not drop).
 * - Enforce emit ownership: reject types not declared in the plugin's manifest.
 *
 * See tmp/plan/watch-to-agent/04-mcp-automation-contract.md.
 */
export function registerMcpAutomation(
  client: Client,
  pluginId: string,
  deps: RegisterMcpAutomationDeps,
): void {
  const { eventDispatcher, emitRegistry, rateLimiter, logger } = deps;

  // Path 1: standard MCP resource updates
  client.setNotificationHandler(ResourceUpdatedNotificationSchema, (notification) => {
    const uri = String(notification.params.uri ?? "");
    if (!rateLimiter.allow(pluginId)) {
      logger?.warn({ pluginId, type: "resource.updated", dropped: true }, "automation rate limited");
      return;
    }
    const event = createAutomationEvent("resource.updated", pluginId, { uri });
    void eventDispatcher.publish(event);
  });

  // Path 2: NusaShell-namespaced automation
  client.setNotificationHandler(NusashellAutomationNotificationSchema, (notification) => {
    const { type, payload } = notification.params;
    if (!type || typeof type !== "string") {
      logger?.warn({ pluginId }, "automation notification missing type — dropping");
      return;
    }

    // Ownership check: reject undeclared types
    if (!emitRegistry.isOwnedBy(pluginId, type)) {
      logger?.warn(
        { pluginId, type, dropped: true, reason: "undeclared_emit" },
        "automation emit rejected: type not declared in plugin manifest",
      );
      return;
    }

    // Rate limit
    if (!rateLimiter.allow(pluginId)) {
      logger?.warn({ pluginId, type, dropped: true }, "automation rate limited");
      return;
    }

    // Payload cap
    const { truncated, text } = rateLimiter.boundPayload(payload);
    if (truncated) {
      logger?.warn({ pluginId, type, truncated: true }, "automation payload truncated to 64KB");
    }

    const boundedPayload = parseBoundedPayload(text);
    const event: AutomationEvent = createAutomationEvent(type, pluginId, boundedPayload);
    void eventDispatcher.publish(event);
  });
}

function parseBoundedPayload(text: string): Readonly<Record<string, unknown>> {
  try {
    const parsed = JSON.parse(text);
    if (parsed !== null && typeof parsed === "object" && !Array.isArray(parsed)) {
      return parsed as Record<string, unknown>;
    }
  } catch {
    // fall through
  }
  return { value: text };
}
