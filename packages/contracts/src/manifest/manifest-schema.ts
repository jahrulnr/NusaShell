import { z } from "zod";

export const ManifestSchema = z.object({
  id: z.string().min(1),
  name: z.string().min(1),
  version: z.string().min(1),
  icon: z.string().min(1),
  ui: z.object({
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
  }),
  mcp: z.object({
    transport: z.enum(["stdio", "sse", "http"]),
    command: z.string().min(1).optional(),
    args: z.array(z.string()).optional(),
    url: z.string().min(1).optional(),
    env: z.record(z.string(), z.string()).optional(),
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
