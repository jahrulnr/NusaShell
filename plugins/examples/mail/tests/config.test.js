import { describe, expect, it } from "vitest";
import {
  loadAccountsFromEnvironment,
  publicAccount,
  resolveAccount,
} from "../mcp/config.js";

const account = {
  id: "work",
  name: "Work",
  email: "me@example.com",
  username: "me@example.com",
  password: "app-password",
  enabled: true,
  imap: { host: "imap.example.com", port: 993, secure: true },
  smtp: { host: "smtp.example.com", port: 465, secure: true },
};

describe("mail account configuration", () => {
  it("loads multiple accounts from the injected runtime environment", () => {
    const accounts = loadAccountsFromEnvironment({
      NUSASHELL_MAIL_ACCOUNTS: JSON.stringify([
        account,
        { ...account, id: "personal", email: "personal@example.com" },
      ]),
    });

    expect(accounts.map((item) => item.id)).toEqual(["work", "personal"]);
  });

  it("never includes credentials in public account output", () => {
    const result = publicAccount(account);

    expect(result).toEqual(expect.objectContaining({
      id: "work",
      name: "Work",
      email: "me@example.com",
    }));
    expect(result).not.toHaveProperty("password");
    expect(result).not.toHaveProperty("username");
  });

  it("rejects duplicate account identifiers", () => {
    expect(() => loadAccountsFromEnvironment({
      NUSASHELL_MAIL_ACCOUNTS: JSON.stringify([account, account]),
    })).toThrow(/duplicate account id/i);
  });

  it("rejects plaintext or certificate-unverified server settings", () => {
    expect(() => loadAccountsFromEnvironment({
      NUSASHELL_MAIL_ACCOUNTS: JSON.stringify([{
        ...account,
        imap: { ...account.imap, secure: false, starttls: false },
      }]),
    })).toThrow(/encrypted transport/i);

    expect(() => loadAccountsFromEnvironment({
      NUSASHELL_MAIL_ACCOUNTS: JSON.stringify([{
        ...account,
        smtp: { ...account.smtp, rejectUnauthorized: false },
      }]),
    })).toThrow();
  });

  it("does not resolve disabled accounts", () => {
    expect(() => resolveAccount([{ ...account, enabled: false }], "work"))
      .toThrow(/not enabled/i);
  });
});
