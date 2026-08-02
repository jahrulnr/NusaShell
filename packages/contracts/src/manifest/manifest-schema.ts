import { z } from "zod";

export const ManifestSchema = z.object({
  source: z.enum(["native-mcp", "package"]).optional(),
  id: z
    .string()
    .min(1)
    .regex(
      /^[a-z0-9][a-z0-9._-]*$/i,
      "id must start with alphanumeric and contain only letters, numbers, dots, dashes, or underscores",
    ),
  name: z.string().min(1),
  version: z.string().min(1),
  /**
   * Plugin icon. Accepts three formats:
   * 1. Text / emoji — e.g. "📝" or "N"
   * 2. File path — e.g. "file://icon.png" (relative to plugin dir) or "file:///abs/path/icon.png"
   * 3. URL — e.g. "https://example.com/icon.png"
   */
  icon: z.string().min(1),
  category: z.string().min(1).optional(),
  /**
   * Plugin UI surface. Omit for a headless MCP-only plugin (no window, not
   * shown on the Home launcher grid; managed from the Plugins view and via
   * agent `mcp_*` tools). When present, `entry` must be non-empty and point
   * to a file inside the plugin folder.
   */
  ui: z
    .object({
      entry: z.string().min(1),
      window: z
        .object({
          mode: z.enum(["panel", "fullscreen", "widget"]).optional(),
          defaultSize: z
            .object({
              width: z.number().int().positive(),
              height: z.number().int().positive(),
            })
            .optional(),
          resizable: z.boolean().optional(),
        })
        .optional(),
    })
    .optional(),
  mcp: z.object({
    transport: z.enum(["stdio", "sse", "http"]),
    command: z.string().min(1).optional(),
    args: z.array(z.string()).optional(),
    url: z.string().min(1).optional(),
    env: z.record(z.string(), z.string()).optional(),
    headers: z.record(z.string(), z.string()).optional(),
    autostart: z.boolean().optional(),
    keepAliveOnClose: z.boolean().optional(),
  }),
  dependencies: z
    .object({
      shell: z.string().optional(),
    })
    .optional(),
});

export type ManifestJson = z.infer<typeof ManifestSchema>;
