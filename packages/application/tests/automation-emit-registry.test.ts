import { describe, expect, it } from "vitest";
import { AutomationEmitRegistry } from "../src/plugin/services/automation-emit-registry.js";
import type { AutomationConfig } from "@nusashell/domain";

describe("AutomationEmitRegistry", () => {
  it("registers emits for a plugin", () => {
    const registry = new AutomationEmitRegistry();
    const automation: AutomationConfig = {
      emits: [{ type: "mail.new", description: "New mail" }],
    };
    registry.register("nusashell.mail", automation);
    expect(registry.isOwnedBy("nusashell.mail", "mail.new")).toBe(true);
    expect(registry.isOwnedBy("nusashell.other", "mail.new")).toBe(false);
  });

  it("returns the owning plugin for a type", () => {
    const registry = new AutomationEmitRegistry();
    registry.register("nusashell.mail", {
      emits: [{ type: "mail.new", description: "New mail" }],
    });
    expect(registry.ownerOf("mail.new")).toBe("nusashell.mail");
    expect(registry.ownerOf("unknown.type")).toBeUndefined();
  });

  it("lists emits for a plugin", () => {
    const registry = new AutomationEmitRegistry();
    registry.register("nusashell.mail", {
      emits: [
        { type: "mail.new", description: "New mail" },
        { type: "mail.folder_changed", description: "Folder changed" },
      ],
    });
    expect(registry.emitsFor("nusashell.mail")).toEqual(["mail.new", "mail.folder_changed"]);
    expect(registry.emitsFor("nusashell.other")).toEqual([]);
  });

  it("rejects type collision between two plugins", () => {
    const registry = new AutomationEmitRegistry();
    registry.register("nusashell.mail", {
      emits: [{ type: "mail.new", description: "New mail" }],
    });
    expect(() =>
      registry.register("nusashell.other", {
        emits: [{ type: "mail.new", description: "Hijacked" }],
      }),
    ).toThrow(/collision/);
  });

  it("allows the same plugin to re-register its own types", () => {
    const registry = new AutomationEmitRegistry();
    registry.register("nusashell.mail", {
      emits: [{ type: "mail.new", description: "New mail" }],
    });
    expect(() =>
      registry.register("nusashell.mail", {
        emits: [
          { type: "mail.new", description: "Updated" },
          { type: "mail.sent", description: "Mail sent" },
        ],
      }),
    ).not.toThrow();
    expect(registry.isOwnedBy("nusashell.mail", "mail.new")).toBe(true);
    expect(registry.isOwnedBy("nusashell.mail", "mail.sent")).toBe(true);
  });

  it("unregisters a plugin's emits", () => {
    const registry = new AutomationEmitRegistry();
    registry.register("nusashell.mail", {
      emits: [{ type: "mail.new", description: "New mail" }],
    });
    registry.unregister("nusashell.mail");
    expect(registry.isOwnedBy("nusashell.mail", "mail.new")).toBe(false);
    expect(registry.ownerOf("mail.new")).toBeUndefined();
  });

  it("handles unregister for a plugin with no emits", () => {
    const registry = new AutomationEmitRegistry();
    expect(() => registry.unregister("nusashell.unknown")).not.toThrow();
  });

  it("handles undefined automation", () => {
    const registry = new AutomationEmitRegistry();
    registry.register("nusashell.plain", undefined);
    expect(registry.emitsFor("nusashell.plain")).toEqual([]);
  });

  it("handles automation with no emits", () => {
    const registry = new AutomationEmitRegistry();
    registry.register("nusashell.pollonly", { poll: [{ tool: "sync" }] });
    expect(registry.emitsFor("nusashell.pollonly")).toEqual([]);
  });

  it("clears all registrations", () => {
    const registry = new AutomationEmitRegistry();
    registry.register("nusashell.mail", {
      emits: [{ type: "mail.new", description: "New mail" }],
    });
    registry.register("nusashell.files", {
      emits: [{ type: "files.modified", description: "File modified" }],
    });
    registry.clear();
    expect(registry.ownerOf("mail.new")).toBeUndefined();
    expect(registry.ownerOf("files.modified")).toBeUndefined();
  });
});
