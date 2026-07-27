import { z } from "zod";

export const PluginStartRequestSchema = z.object({
  kind: z.literal("request"),
  id: z.string().min(1),
  method: z.literal("plugin.start"),
  payload: z.object({
    pluginId: z.string().min(1),
  }),
});

export const PluginStopRequestSchema = z.object({
  kind: z.literal("request"),
  id: z.string().min(1),
  method: z.literal("plugin.stop"),
  payload: z.object({
    pluginId: z.string().min(1),
  }),
});

export const PluginListRequestSchema = z.object({
  kind: z.literal("request"),
  id: z.string().min(1),
  method: z.literal("plugin.list"),
  payload: z.object({}).optional().default({}),
});

export const ToolCallRequestSchema = z.object({
  kind: z.literal("request"),
  id: z.string().min(1),
  method: z.literal("tool.call"),
  payload: z.object({
    pluginId: z.string().min(1),
    requestId: z.string().min(1),
    toolName: z.string().min(1),
    args: z.record(z.string(), z.unknown()),
    timeoutMs: z.number().int().positive().optional(),
  }),
});

export const ToolCancelRequestSchema = z.object({
  kind: z.literal("request"),
  id: z.string().min(1),
  method: z.literal("tool.cancel"),
  payload: z.object({
    pluginId: z.string().min(1),
    requestId: z.string().min(1),
  }),
});

export const RequestSchema = z.discriminatedUnion("method", [
  PluginStartRequestSchema,
  PluginStopRequestSchema,
  PluginListRequestSchema,
  ToolCallRequestSchema,
  ToolCancelRequestSchema,
]);

export type PluginStartRequest = z.infer<typeof PluginStartRequestSchema>;
export type PluginStopRequest = z.infer<typeof PluginStopRequestSchema>;
export type PluginListRequest = z.infer<typeof PluginListRequestSchema>;
export type ToolCallRequest = z.infer<typeof ToolCallRequestSchema>;
export type ToolCancelRequest = z.infer<typeof ToolCancelRequestSchema>;
export type ParsedRequest = z.infer<typeof RequestSchema>;
