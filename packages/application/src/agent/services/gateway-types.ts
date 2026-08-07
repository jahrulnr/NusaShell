/**
 * Shared types for the agent tool gateway modules. Kept in a leaf module so
 * `gateway-route-store.ts` and `gateway-live-snapshot.ts` can import them
 * without creating an import cycle with the main `McpAgentToolGateway` class.
 */

export type WriteOrigin = "foreground" | "background_review";

export interface SkillApprovalStagingPort {
  stage(
    skillId: string,
    action: "create" | "edit" | "write_file" | "delete",
    path: string,
    content: string,
  ): Promise<{ id: string }>;
}

/** A grant of a single MCP tool for a turn / conversation. */
export interface McpToolRoute {
  readonly pluginId: string;
  readonly toolName: string;
  readonly inputSchema: Readonly<Record<string, unknown>>;
  readonly description?: string;
}

export const emptySchema = { type: "object", properties: {} } as const;

export const MAX_ASK_OPTIONS = 8;
