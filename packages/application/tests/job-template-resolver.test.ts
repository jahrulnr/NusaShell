import { describe, expect, it } from "vitest";
import {
  resolveTemplates,
  resolveTemplatesInRecord,
  templateContextFromEvent,
} from "../src/job/services/job-template-resolver.js";
import { createAutomationEvent } from "../src/events/automation-event.js";

describe("job template resolver", () => {
  describe("resolveTemplates", () => {
    const ctx = templateContextFromEvent(
      createAutomationEvent("mail.new", "nusashell.mail", {
        messageId: "abc123",
        from: { address: "boss@corp.com", name: "Boss" },
        subject: "Re: budget",
        count: 3,
        flag: true,
      }),
    );

    it("resolves {{event.type}}", () => {
      expect(resolveTemplates("Event: {{event.type}}", ctx)).toBe("Event: mail.new");
    });

    it("resolves {{event.pluginId}}", () => {
      expect(resolveTemplates("From: {{event.pluginId}}", ctx)).toBe("From: nusashell.mail");
    });

    it("resolves {{payload.*}} with dot-path", () => {
      expect(resolveTemplates("Subject: {{payload.subject}}", ctx)).toBe("Subject: Re: budget");
    });

    it("resolves nested payload paths", () => {
      expect(resolveTemplates("From: {{payload.from.address}}", ctx)).toBe("From: boss@corp.com");
    });

    it("stringifies non-string values", () => {
      expect(resolveTemplates("Count: {{payload.count}}", ctx)).toBe("Count: 3");
      expect(resolveTemplates("Flag: {{payload.flag}}", ctx)).toBe("Flag: true");
    });

    it("leaves missing paths as literal", () => {
      expect(resolveTemplates("{{payload.missing}}", ctx)).toBe("{{payload.missing}}");
      expect(resolveTemplates("{{payload.from.nonexistent}}", ctx)).toBe("{{payload.from.nonexistent}}");
    });

    it("does NOT resolve templates with whitespace inside braces", () => {
      expect(resolveTemplates("{{ payload.subject }}", ctx)).toBe("{{ payload.subject }}");
    });

    it("resolves multiple templates in one string", () => {
      const result = resolveTemplates(
        "[{{event.type}}] {{payload.subject}} from {{payload.from.name}}",
        ctx,
      );
      expect(result).toBe("[mail.new] Re: budget from Boss");
    });

    it("leaves unknown template prefixes as literal", () => {
      expect(resolveTemplates("{{user.name}}", ctx)).toBe("{{user.name}}");
    });
  });

  describe("resolveTemplatesInRecord", () => {
    const ctx = templateContextFromEvent(
      createAutomationEvent("files.modified", "nusashell.files", { path: "/etc/hosts" }),
    );

    it("resolves templates in string values", () => {
      const result = resolveTemplatesInRecord(
        { path: "{{payload.path}}", plugin: "{{event.pluginId}}" },
        ctx,
      );
      expect(result).toEqual({ path: "/etc/hosts", plugin: "nusashell.files" });
    });

    it("passes through non-string values unchanged", () => {
      const result = resolveTemplatesInRecord(
        { count: 42, active: true, nested: { a: 1 } },
        ctx,
      );
      expect(result).toEqual({ count: 42, active: true, nested: { a: 1 } });
    });

    it("handles empty record", () => {
      expect(resolveTemplatesInRecord({}, ctx)).toEqual({});
    });
  });

  describe("templateContextFromEvent", () => {
    it("handles event with undefined pluginId", () => {
      const ctx = templateContextFromEvent(
        createAutomationEvent("test.event", undefined, { a: 1 }),
      );
      expect(ctx.event.pluginId).toBe("");
      expect(resolveTemplates("{{event.pluginId}}", ctx)).toBe("");
    });

    it("handles event with empty payload", () => {
      const ctx = templateContextFromEvent(
        createAutomationEvent("test.event", "test.plugin", {}),
      );
      expect(resolveTemplates("{{payload.missing}}", ctx)).toBe("{{payload.missing}}");
    });
  });
});
