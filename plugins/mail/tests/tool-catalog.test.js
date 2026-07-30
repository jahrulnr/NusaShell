import { describe, expect, it } from "vitest";
import { MAIL_TOOL_NAMES } from "../mcp/tool-catalog.js";

describe("Mail MCP tool catalog", () => {
  it("exposes only the explicit read-only milestone tools", () => {
    expect(MAIL_TOOL_NAMES).toEqual([
      "mail_accounts",
      "mail_account_get",
      "mail_account_test",
      "mail_mailboxes",
      "mail_inbox",
      "mail_messages",
      "mail_search",
      "mail_read",
    ]);
  });

  it("does not expose credential mutation or direct-send tools", () => {
    expect(MAIL_TOOL_NAMES).not.toContain("mail_set_credential");
    expect(MAIL_TOOL_NAMES).not.toContain("mail_send");
    expect(MAIL_TOOL_NAMES.every((name) => !name.includes("password"))).toBe(true);
  });
});
