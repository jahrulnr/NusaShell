import { z } from "zod";

const PluginStateSchema = z.enum([
  "idle",
  "starting",
  "running",
  "stopping",
  "crashed",
]);

export const PluginStartResultSchema = z.object({
  pluginId: z.string(),
  state: PluginStateSchema,
});

export const PluginStopResultSchema = z.object({
  pluginId: z.string(),
  state: PluginStateSchema,
});

export const PluginInstallResultSchema = z.object({
  pluginId: z.string(),
  installPath: z.string(),
  version: z.string(),
});

export const PluginUninstallResultSchema = z.object({
  pluginId: z.string(),
});

export const PluginListItemSchema = z.object({
  pluginId: z.string(),
  name: z.string(),
  version: z.string(),
  icon: z.string(),
  installPath: z.string(),
  state: PluginStateSchema,
  enabled: z.boolean(),
  autostart: z.boolean(),
  source: z.enum(["native-mcp", "package"]).optional(),
  transport: z.string().optional(),
  category: z.string().optional(),
  ui: z
    .object({
      entry: z.string(),
      window: z.object({
        mode: z.enum(["panel", "fullscreen", "widget"]).optional(),
        defaultSize: z.object({
          width: z.number().int().positive(),
          height: z.number().int().positive(),
        }).optional(),
        resizable: z.boolean().optional(),
      }).optional(),
    })
    .optional(),
  keepAliveOnClose: z.boolean(),
});

export const PluginListResultSchema = z.object({
  plugins: z.array(PluginListItemSchema),
});

export const ToolCallResultSchema = z.object({
  requestId: z.string(),
  result: z.unknown(),
});

export const ErrorSchema = z.object({
  code: z.string(),
  message: z.string(),
  details: z.record(z.string(), z.unknown()).optional(),
});

export const SuccessResponseSchema = z.object({
  kind: z.literal("response"),
  id: z.string(),
  ok: z.literal(true),
  result: z.unknown(),
});

export const ErrorResponseSchema = z.object({
  kind: z.literal("response"),
  id: z.string(),
  ok: z.literal(false),
  error: ErrorSchema,
});

export const ResponseSchema = z.discriminatedUnion("ok", [
  SuccessResponseSchema,
  ErrorResponseSchema,
]);

export type PluginStartResult = z.infer<typeof PluginStartResultSchema>;
export type PluginStopResult = z.infer<typeof PluginStopResultSchema>;
export type PluginInstallResult = z.infer<typeof PluginInstallResultSchema>;
export type PluginUninstallResult = z.infer<typeof PluginUninstallResultSchema>;
export type PluginListItem = z.infer<typeof PluginListItemSchema>;
export type PluginListResult = z.infer<typeof PluginListResultSchema>;
export type ToolCallResult = z.infer<typeof ToolCallResultSchema>;
