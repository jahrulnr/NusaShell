import { access, mkdir, mkdtemp, readFile, writeFile } from "node:fs/promises";
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

/** Fixture path layout matching plugins-data/nusashell.mail/mail-settings.json */
function pluginScopedPaths(dataRoot: string) {
  const targetPath = join(dataRoot, "plugins-data", "nusashell.mail", "mail-settings.json");
  const legacyPath = join(dataRoot, "mail-settings.json");
  return { targetPath, legacyPath };
}

async function pathExists(path: string): Promise<boolean> {
  try {
    await access(path);
    return true;
  } catch {
    return false;
  }
}

describe("MailSettingsStore", () => {
  afterEach(() => {
    safeStorageState.available = true;
  });

  it("encrypts passwords at rest and excludes them from public account data", async () => {
    const directory = await mkdtemp(join(tmpdir(), "nusashell-mail-settings-"));
    const { targetPath } = pluginScopedPaths(directory);
    const store = new MailSettingsStore(targetPath);

    const result = await store.save(input);

    expect(result.accounts[0]).toMatchObject({
      id: "work",
      email: "me@example.com",
      hasCredential: true,
    });
    expect(result.accounts[0]).not.toHaveProperty("password");
    expect(await readFile(targetPath, "utf8")).not.toContain("app-password");
  });

  it("writes persisted settings under plugins-data/nusashell.mail/", async () => {
    const directory = await mkdtemp(join(tmpdir(), "nusashell-mail-settings-"));
    const { targetPath, legacyPath } = pluginScopedPaths(directory);
    const store = new MailSettingsStore(targetPath);

    await store.save(input);

    expect(await pathExists(targetPath)).toBe(true);
    expect(await pathExists(legacyPath)).toBe(false);
  });

  it("preserves an existing password when editing with a blank password", async () => {
    const directory = await mkdtemp(join(tmpdir(), "nusashell-mail-settings-"));
    const store = new MailSettingsStore(pluginScopedPaths(directory).targetPath);
    await store.save(input);

    await store.save({ ...input, name: "Renamed", password: "" });

    expect(JSON.parse(store.runtimeEnvironment().NUSASHELL_MAIL_ACCOUNTS)[0])
      .toMatchObject({ name: "Renamed", password: "app-password" });
  });

  it("requires the credential again when changing a saved login endpoint", async () => {
    const directory = await mkdtemp(join(tmpdir(), "nusashell-mail-settings-"));
    const store = new MailSettingsStore(pluginScopedPaths(directory).targetPath);
    await store.save(input);

    await expect(store.save({
      ...input,
      password: "",
      imap: { ...input.imap, host: "attacker.example.com" },
    })).rejects.toThrow(/re-enter the credential/i);
  });

  it("deletes an account and removes its credential from runtime configuration", async () => {
    const directory = await mkdtemp(join(tmpdir(), "nusashell-mail-settings-"));
    const store = new MailSettingsStore(pluginScopedPaths(directory).targetPath);
    await store.save(input);

    await store.delete("work");

    expect(await store.getPublic()).toMatchObject({ accounts: [] });
    expect(JSON.parse(store.runtimeEnvironment().NUSASHELL_MAIL_ACCOUNTS)).toEqual([]);
  });

  it("serializes concurrent account mutations without losing an update", async () => {
    const directory = await mkdtemp(join(tmpdir(), "nusashell-mail-settings-"));
    const store = new MailSettingsStore(pluginScopedPaths(directory).targetPath);

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
    const path = pluginScopedPaths(directory).targetPath;
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
    const store = new MailSettingsStore(pluginScopedPaths(directory).targetPath);

    await expect(store.save({
      ...input,
      imap: { ...input.imap, secure: false, starttls: false },
    })).rejects.toThrow(/secure transport/i);

    await expect(store.save({
      ...input,
      smtp: { ...input.smtp, rejectUnauthorized: false },
    })).rejects.toThrow(/certificate verification/i);
  });

  describe("legacy root mail-settings.json migration", () => {
    /** Realistic at-rest shape: password field is already base64 from Electron safeStorage. */
    const encryptedPasswordBase64 = Buffer.from("encrypted:secret-app-password").toString("base64");
    const legacyBody = JSON.stringify({
      accounts: [{
        id: "legacy-work",
        name: "Legacy Work",
        email: "legacy@example.com",
        username: "legacy@example.com",
        password: encryptedPasswordBase64,
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
      }],
    }, null, 2);

    it("moves legacy root settings into plugins-data without re-encoding credentials", async () => {
      const directory = await mkdtemp(join(tmpdir(), "nusashell-mail-settings-"));
      const { targetPath, legacyPath } = pluginScopedPaths(directory);
      await writeFile(legacyPath, legacyBody, { mode: 0o600 });
      const store = new MailSettingsStore(targetPath);

      await store.migrateFrom(legacyPath);
      await store.load();

      expect(await pathExists(legacyPath)).toBe(false);
      expect(await pathExists(targetPath)).toBe(true);
      const migrated = await readFile(targetPath, "utf8");
      const migratedPassword = JSON.parse(migrated).accounts[0].password as string;
      expect(migratedPassword).toBe(encryptedPasswordBase64);
      expect(migratedPassword).toBe(
        JSON.parse(legacyBody).accounts[0].password,
      );
      const publicSettings = await store.getPublic();
      expect(publicSettings.accounts[0]).toMatchObject({
        id: "legacy-work",
        email: "legacy@example.com",
        hasCredential: true,
      });
      expect(JSON.parse(store.runtimeEnvironment().NUSASHELL_MAIL_ACCOUNTS)[0])
        .toMatchObject({ password: "secret-app-password" });
    });

    it("does not overwrite target when new path already exists", async () => {
      const directory = await mkdtemp(join(tmpdir(), "nusashell-mail-settings-"));
      const { targetPath, legacyPath } = pluginScopedPaths(directory);
      const targetBody = JSON.stringify({
        accounts: [{
          id: "new-wins",
          name: "New Wins",
          email: "new@example.com",
          username: "new@example.com",
          password: Buffer.from("encrypted:new-password").toString("base64"),
          enabled: true,
          imap: input.imap,
          smtp: input.smtp,
        }],
      }, null, 2);
      await writeFile(legacyPath, legacyBody, { mode: 0o600 });
      await mkdir(join(directory, "plugins-data", "nusashell.mail"), { recursive: true });
      await writeFile(targetPath, targetBody, { mode: 0o600 });
      const store = new MailSettingsStore(targetPath);

      await store.migrateFrom(legacyPath);
      await store.load();

      expect(await readFile(targetPath, "utf8")).toBe(targetBody);
      expect(await pathExists(legacyPath)).toBe(true);
      expect((await store.getPublic()).accounts[0]?.id).toBe("new-wins");
    });

    it("is a no-op when legacy file is absent", async () => {
      const directory = await mkdtemp(join(tmpdir(), "nusashell-mail-settings-"));
      const { targetPath, legacyPath } = pluginScopedPaths(directory);
      const store = new MailSettingsStore(targetPath);

      await store.migrateFrom(legacyPath);
      await store.load();

      expect(await pathExists(targetPath)).toBe(false);
      expect(await pathExists(legacyPath)).toBe(false);
      expect(await store.getPublic()).toMatchObject({ accounts: [] });
    });

    it("is idempotent after a successful one-way migration", async () => {
      const directory = await mkdtemp(join(tmpdir(), "nusashell-mail-settings-"));
      const { targetPath, legacyPath } = pluginScopedPaths(directory);
      await writeFile(legacyPath, legacyBody, { mode: 0o600 });
      const store = new MailSettingsStore(targetPath);

      await store.migrateFrom(legacyPath);
      const afterFirst = await readFile(targetPath, "utf8");
      await store.migrateFrom(legacyPath);

      expect(await pathExists(legacyPath)).toBe(false);
      expect(await readFile(targetPath, "utf8")).toBe(afterFirst);
    });
  });
});
