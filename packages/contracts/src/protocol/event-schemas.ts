import { z } from "zod";

const PluginStateSchema = z.enum([
  "idle",
  "starting",
  "running",
  "stopping",
  "crashed",
]);

export const PluginInstalledEventSchema = z.object({
  kind: z.literal("event"),
  event: z.literal("plugin.installed"),
  sequence: z.number().int().nonnegative(),
  payload: z.object({
    pluginId: z.string(),
    version: z.string(),
    timestamp: z.string(),
  }),
});

export const PluginUninstalledEventSchema = z.object({
  kind: z.literal("event"),
  event: z.literal("plugin.uninstalled"),
  sequence: z.number().int().nonnegative(),
  payload: z.object({
    pluginId: z.string(),
    timestamp: z.string(),
  }),
});

export const PluginStartedEventSchema = z.object({
  kind: z.literal("event"),
  event: z.literal("plugin.started"),
  sequence: z.number().int().nonnegative(),
  payload: z.object({
    pluginId: z.string(),
    state: PluginStateSchema,
    pid: z.number().int(),
    timestamp: z.string(),
  }),
});

export const PluginStoppedEventSchema = z.object({
  kind: z.literal("event"),
  event: z.literal("plugin.stopped"),
  sequence: z.number().int().nonnegative(),
  payload: z.object({
    pluginId: z.string(),
    state: PluginStateSchema,
    timestamp: z.string(),
  }),
});

export const PluginCrashedEventSchema = z.object({
  kind: z.literal("event"),
  event: z.literal("plugin.crashed"),
  sequence: z.number().int().nonnegative(),
  payload: z.object({
    pluginId: z.string(),
    state: PluginStateSchema,
    exitCode: z.number().int(),
    timestamp: z.string(),
  }),
});

export const PluginStateChangedEventSchema = z.object({
  kind: z.literal("event"),
  event: z.literal("plugin.state_changed"),
  sequence: z.number().int().nonnegative(),
  payload: z.object({
    pluginId: z.string(),
    oldState: PluginStateSchema,
    newState: PluginStateSchema,
    timestamp: z.string(),
  }),
});

export const ToolCallCompletedEventSchema = z.object({
  kind: z.literal("event"),
  event: z.literal("tool.call_completed"),
  sequence: z.number().int().nonnegative(),
  payload: z.object({
    pluginId: z.string(),
    requestId: z.string(),
    toolName: z.string(),
    success: z.boolean(),
    timestamp: z.string(),
  }),
});

export const EventSchema = z.discriminatedUnion("event", [
  PluginInstalledEventSchema,
  PluginUninstalledEventSchema,
  PluginStartedEventSchema,
  PluginStoppedEventSchema,
  PluginCrashedEventSchema,
  PluginStateChangedEventSchema,
  ToolCallCompletedEventSchema,
]);

export type PluginInstalledEvent = z.infer<typeof PluginInstalledEventSchema>;
export type PluginUninstalledEvent = z.infer<typeof PluginUninstalledEventSchema>;
export type PluginStartedEvent = z.infer<typeof PluginStartedEventSchema>;
export type PluginStoppedEvent = z.infer<typeof PluginStoppedEventSchema>;
export type PluginCrashedEvent = z.infer<typeof PluginCrashedEventSchema>;
export type PluginStateChangedEvent = z.infer<typeof PluginStateChangedEventSchema>;
export type ToolCallCompletedEvent = z.infer<typeof ToolCallCompletedEventSchema>;
export type ParsedEvent = z.infer<typeof EventSchema>;
