import { describe, expect, it } from "vitest";
import { ManifestSchema } from "../src/manifest/manifest-schema.js";

describe("manifest automation block", () => {
  const baseManifest = {
    id: "nusashell.mail",
    name: "Mail",
    version: "1.0.0",
    icon: "📝",
    mcp: { transport: "stdio", command: "node", args: ["server.js"] },
  };

  it("accepts a manifest without automation (backward compatible)", () => {
    const result = ManifestSchema.safeParse(baseManifest);
    expect(result.success).toBe(true);
    expect(result.success && result.data.automation).toBeUndefined();
  });

  it("accepts a manifest with automation.emits", () => {
    const result = ManifestSchema.safeParse({
      ...baseManifest,
      automation: {
        emits: [
          {
            type: "mail.new",
            description: "New mail arrived",
            payloadSchema: {
              type: "object",
              properties: { messageId: { type: "string" } },
              required: ["messageId"],
            },
          },
        ],
      },
    });
    expect(result.success).toBe(true);
    if (result.success) {
      expect(result.data.automation?.emits).toHaveLength(1);
      expect(result.data.automation?.emits?.[0]?.type).toBe("mail.new");
    }
  });

  it("accepts a manifest with automation.poll", () => {
    const result = ManifestSchema.safeParse({
      ...baseManifest,
      automation: {
        poll: [{ tool: "mail_sync", suggestEvery: "5m", diffHint: "new message ids" }],
      },
    });
    expect(result.success).toBe(true);
    if (result.success) {
      expect(result.data.automation?.poll?.[0]?.tool).toBe("mail_sync");
    }
  });

  it("accepts both emits and poll", () => {
    const result = ManifestSchema.safeParse({
      ...baseManifest,
      automation: {
        emits: [{ type: "mail.new", description: "New mail" }],
        poll: [{ tool: "mail_sync", suggestEvery: "5m" }],
      },
    });
    expect(result.success).toBe(true);
  });

  it("rejects an emit type with invalid characters", () => {
    const result = ManifestSchema.safeParse({
      ...baseManifest,
      automation: {
        emits: [{ type: "mail new!", description: "bad" }],
      },
    });
    expect(result.success).toBe(false);
  });

  it("rejects an emit with empty description", () => {
    const result = ManifestSchema.safeParse({
      ...baseManifest,
      automation: {
        emits: [{ type: "mail.new", description: "" }],
      },
    });
    expect(result.success).toBe(false);
  });

  it("rejects an emit type starting with a dot", () => {
    const result = ManifestSchema.safeParse({
      ...baseManifest,
      automation: {
        emits: [{ type: ".mail.new", description: "bad" }],
      },
    });
    expect(result.success).toBe(false);
  });

  it("rejects an invalid suggestEvery format", () => {
    const result = ManifestSchema.safeParse({
      ...baseManifest,
      automation: {
        poll: [{ tool: "mail_sync", suggestEvery: "5minutes" }],
      },
    });
    expect(result.success).toBe(false);
  });

  it("accepts an empty automation object", () => {
    const result = ManifestSchema.safeParse({
      ...baseManifest,
      automation: {},
    });
    expect(result.success).toBe(true);
  });
});
