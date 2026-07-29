import { mkdtemp, readFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { afterEach, describe, expect, it, vi } from "vitest";

const safeStorageState = vi.hoisted(() => ({ available: true }));
vi.mock("electron", () => ({
  safeStorage: {
    isEncryptionAvailable: () => safeStorageState.available,
    encryptString: (value: string) => Buffer.from(`encrypted:${value}`),
    decryptString: (value: Buffer) => value.toString().replace(/^encrypted:/, ""),
  },
}));

import { MailSettingsStore } from "../src/main/mail-settings.js";

const input = {
  id: "work",
  name: "Work",
  email: "me@example.com",
  username: "me@example.com",
  password: "app-password",
  enabled: true,
  imap: {
    host: "imap.example.com",
    port: 993,
    secure: true,
    starttls: false,
    rejectUnauthorized: true,
  },
  smtp: {
    host: "smtp.example.com",
    port: 465,
    secure: true,
    starttls: false,
    rejectUnauthorized: true,
  },
};

describe("MailSettingsStore", () => {
  afterEach(() => {
    safeStorageState.available = true;
  });

  it("encrypts passwords at rest and excludes them from public account data", async () => {
    const directory = await mkdtemp(join(tmpdir(), "nusashell-mail-settings-"));
    const path = join(directory, "mail-settings.json");
    const store = new MailSettingsStore(path);

    const result = await store.save(input);

    expect(result.accounts[0]).toMatchObject({
      id: "work",
      email: "me@example.com",
      hasCredential: true,
    });
    expect(result.accounts[0]).not.toHaveProperty("password");
    expect(await readFile(path, "utf8")).not.toContain("app-password");
  });

  it("preserves an existing password when editing with a blank password", async () => {
    const directory = await mkdtemp(join(tmpdir(), "nusashell-mail-settings-"));
    const store = new MailSettingsStore(join(directory, "mail-settings.json"));
    await store.save(input);

    await store.save({ ...input, name: "Renamed", password: "" });

    expect(JSON.parse(store.runtimeEnvironment().NUSASHELL_MAIL_ACCOUNTS)[0])
      .toMatchObject({ name: "Renamed", password: "app-password" });
  });

  it("requires the credential again when changing a saved login endpoint", async () => {
    const directory = await mkdtemp(join(tmpdir(), "nusashell-mail-settings-"));
    const store = new MailSettingsStore(join(directory, "mail-settings.json"));
    await store.save(input);

    await expect(store.save({
      ...input,
      password: "",
      imap: { ...input.imap, host: "attacker.example.com" },
    })).rejects.toThrow(/re-enter the credential/i);
  });

  it("deletes an account and removes its credential from runtime configuration", async () => {
    const directory = await mkdtemp(join(tmpdir(), "nusashell-mail-settings-"));
    const store = new MailSettingsStore(join(directory, "mail-settings.json"));
    await store.save(input);

    await store.delete("work");

    expect(await store.getPublic()).toMatchObject({ accounts: [] });
    expect(JSON.parse(store.runtimeEnvironment().NUSASHELL_MAIL_ACCOUNTS)).toEqual([]);
  });

  it("serializes concurrent account mutations without losing an update", async () => {
    const directory = await mkdtemp(join(tmpdir(), "nusashell-mail-settings-"));
    const store = new MailSettingsStore(join(directory, "mail-settings.json"));

    await Promise.all([
      store.save(input),
      store.save({
        ...input,
        id: "personal",
        name: "Personal",
        email: "personal@example.com",
        username: "personal@example.com",
      }),
    ]);

    expect((await store.getPublic()).accounts.map((account) => account.id))
      .toEqual(["work", "personal"]);
  });

  it("does not rewrite encrypted accounts while secure storage is unavailable", async () => {
    const directory = await mkdtemp(join(tmpdir(), "nusashell-mail-settings-"));
    const path = join(directory, "mail-settings.json");
    await new MailSettingsStore(path).save(input);
    const original = await readFile(path, "utf8");
    safeStorageState.available = false;
    const lockedStore = new MailSettingsStore(path);

    expect((await lockedStore.getPublic()).accounts[0]?.hasCredential).toBe(true);
    expect(JSON.parse(lockedStore.runtimeEnvironment().NUSASHELL_MAIL_ACCOUNTS))
      .toEqual([]);
    await expect(lockedStore.delete("work")).rejects.toThrow(/secure credential storage/i);
    expect(await readFile(path, "utf8")).toBe(original);
  });

  it("rejects insecure remote connections unless the user explicitly selects TLS", async () => {
    const directory = await mkdtemp(join(tmpdir(), "nusashell-mail-settings-"));
    const store = new MailSettingsStore(join(directory, "mail-settings.json"));

    await expect(store.save({
      ...input,
      imap: { ...input.imap, secure: false, starttls: false },
    })).rejects.toThrow(/secure transport/i);

    await expect(store.save({
      ...input,
      smtp: { ...input.smtp, rejectUnauthorized: false },
    })).rejects.toThrow(/certificate verification/i);
  });
});
