import { safeStorage } from "electron";
import { mkdir, readFile, rename, writeFile } from "node:fs/promises";
import { dirname } from "node:path";
import type {
  MailServerSettings,
  PublicMailAccount,
  PublicMailSettings,
  SaveMailAccountInput,
} from "../shared/mail-contract.js";

interface MailAccountSettings extends Omit<SaveMailAccountInput, "password"> {
  readonly password?: string;
  readonly encryptedPassword?: string;
}

interface StoredMailAccount extends Omit<MailAccountSettings, "password" | "encryptedPassword"> {
  readonly password?: string;
}

export class MailSettingsStore {
  private accounts: MailAccountSettings[] | null = null;
  private mutationQueue: Promise<void> = Promise.resolve();

  constructor(private readonly path: string) {}

  async load(): Promise<readonly MailAccountSettings[]> {
    if (this.accounts) return this.accounts;
    try {
      const parsed = JSON.parse(await readFile(this.path, "utf8")) as { accounts?: unknown };
      if (parsed.accounts !== undefined && !Array.isArray(parsed.accounts)) {
        throw new Error("Mail settings accounts must be an array");
      }
      this.accounts = (parsed.accounts ?? [])
        .map((value, index) => readStoredAccount(value, index));
    } catch (error) {
      if (isFileNotFound(error)) {
        this.accounts = [];
      } else {
        throw new Error("Could not load mail settings", { cause: error });
      }
    }
    return this.accounts;
  }

  async getPublic(): Promise<PublicMailSettings> {
    return this.public(await this.load());
  }

  save(input: SaveMailAccountInput): Promise<PublicMailSettings> {
    return this.mutate(() => this.saveNow(input));
  }

  delete(accountId: string): Promise<PublicMailSettings> {
    return this.mutate(() => this.deleteNow(accountId));
  }

  private async saveNow(input: SaveMailAccountInput): Promise<PublicMailSettings> {
    const current = [...await this.load()];
    const id = normalizeId(input.id);
    const existing = current.find((account) => account.id === id);
    const suppliedPassword = input.password?.trim();
    if (existing && !suppliedPassword && connectionIdentityChanged(existing, input)) {
      throw new Error("Re-enter the credential when changing mail server or login settings");
    }
    const password = suppliedPassword || existing?.password;
    const encryptedPassword = suppliedPassword ? undefined : existing?.encryptedPassword;
    if (!password && !encryptedPassword) {
      throw new Error("Mail account password or app password is required");
    }
    if (!safeStorage.isEncryptionAvailable()) {
      throw new Error("Secure credential storage is unavailable on this system");
    }

    const account: MailAccountSettings = {
      id,
      name: requiredText(input.name, "Account name", 120),
      email: validEmail(input.email),
      username: requiredText(input.username, "Username", 320),
      ...(password ? { password } : {}),
      ...(encryptedPassword ? { encryptedPassword } : {}),
      enabled: input.enabled,
      imap: validServer(input.imap, "IMAP"),
      smtp: validServer(input.smtp, "SMTP"),
    };
    const next = existing
      ? current.map((item) => item.id === id ? account : item)
      : [...current, account];
    await this.persist(next);
    this.accounts = next;
    return this.public(next);
  }

  private async deleteNow(accountId: string): Promise<PublicMailSettings> {
    if (!safeStorage.isEncryptionAvailable()) {
      throw new Error("Secure credential storage is unavailable on this system");
    }
    const id = normalizeId(accountId);
    const next = [...await this.load()].filter((account) => account.id !== id);
    await this.persist(next);
    this.accounts = next;
    return this.public(next);
  }

  runtimeEnvironment(): Record<string, string> {
    const accounts = (this.accounts ?? [])
      .filter((account) => account.password)
      .map((account) => ({
        id: account.id,
        name: account.name,
        email: account.email,
        username: account.username,
        password: account.password as string,
        enabled: account.enabled,
        imap: account.imap,
        smtp: account.smtp,
      }));
    return { NUSASHELL_MAIL_ACCOUNTS: JSON.stringify(accounts) };
  }

  private public(accounts: readonly MailAccountSettings[]): PublicMailSettings {
    return {
      canPersistCredentials: safeStorage.isEncryptionAvailable(),
      accounts: accounts.map(({ password, encryptedPassword, ...account }): PublicMailAccount => ({
        ...account,
        hasCredential: Boolean(password || encryptedPassword),
      })),
    };
  }

  private async persist(accounts: readonly MailAccountSettings[]): Promise<void> {
    await mkdir(dirname(this.path), { recursive: true });
    const stored: StoredMailAccount[] = accounts.map((account) => ({
      id: account.id,
      name: account.name,
      email: account.email,
      username: account.username,
      enabled: account.enabled,
      imap: account.imap,
      smtp: account.smtp,
      ...(account.password
        ? { password: safeStorage.encryptString(account.password).toString("base64") }
        : account.encryptedPassword
          ? { password: account.encryptedPassword }
        : {}),
    }));
    const temporaryPath = `${this.path}.tmp`;
    await writeFile(temporaryPath, JSON.stringify({ accounts: stored }, null, 2), {
      mode: 0o600,
    });
    await rename(temporaryPath, this.path);
  }

  private mutate<T>(operation: () => Promise<T>): Promise<T> {
    const result = this.mutationQueue.then(operation, operation);
    this.mutationQueue = result.then(() => undefined, () => undefined);
    return result;
  }
}

function readStoredAccount(value: unknown, index: number): MailAccountSettings {
  if (!value || typeof value !== "object" || Array.isArray(value)) {
    throw new Error(`Mail account record ${index + 1} is invalid`);
  }
  const raw = value as Record<string, unknown>;
  try {
    const encrypted = text(raw.password);
    const password = encrypted && safeStorage.isEncryptionAvailable()
      ? safeStorage.decryptString(Buffer.from(encrypted, "base64"))
      : undefined;
    return {
      id: normalizeId(text(raw.id)),
      name: requiredText(raw.name, "Account name", 120),
      email: validEmail(raw.email),
      username: requiredText(raw.username, "Username", 320),
      ...(password ? { password } : {}),
      ...(!password && encrypted ? { encryptedPassword: encrypted } : {}),
      enabled: raw.enabled !== false,
      imap: validServer(raw.imap, "IMAP"),
      smtp: validServer(raw.smtp, "SMTP"),
    };
  } catch (error) {
    throw new Error(`Mail account record ${index + 1} could not be read`, { cause: error });
  }
}

function validServer(value: unknown, label: string): MailServerSettings {
  if (!value || typeof value !== "object" || Array.isArray(value)) {
    throw new Error(`${label} server settings are required`);
  }
  const raw = value as Record<string, unknown>;
  const secure = raw.secure === true;
  const starttls = raw.starttls === true;
  if (secure === starttls) {
    throw new Error(`${label} requires exactly one secure transport (TLS or STARTTLS)`);
  }
  if (raw.rejectUnauthorized === false) {
    throw new Error(`${label} certificate verification cannot be disabled`);
  }
  const port = Number(raw.port);
  if (!Number.isInteger(port) || port < 1 || port > 65535) {
    throw new Error(`${label} port must be between 1 and 65535`);
  }
  return {
    host: requiredText(raw.host, `${label} host`, 253),
    port,
    secure,
    starttls,
    rejectUnauthorized: true,
  };
}

function connectionIdentityChanged(
  existing: MailAccountSettings,
  input: SaveMailAccountInput,
): boolean {
  return existing.email !== input.email.trim().toLowerCase()
    || existing.username !== input.username.trim()
    || serverIdentity(existing.imap) !== serverIdentity(input.imap)
    || serverIdentity(existing.smtp) !== serverIdentity(input.smtp);
}

function serverIdentity(server: MailServerSettings): string {
  return [
    server.host.trim().toLowerCase(),
    server.port,
    server.secure,
    server.starttls,
    server.rejectUnauthorized,
  ].join("|");
}

function normalizeId(value: string): string {
  const id = value.trim().toLowerCase()
    .replace(/[^a-z0-9._-]+/g, "-")
    .replace(/^-+|-+$/g, "");
  if (!id || id.length > 64) throw new Error("Mail account ID is invalid");
  return id;
}

function validEmail(value: unknown): string {
  const email = requiredText(value, "Email address", 320).toLowerCase();
  if (!/^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(email)) {
    throw new Error("Email address is invalid");
  }
  return email;
}

function requiredText(value: unknown, label: string, maxLength: number): string {
  const result = text(value).trim();
  if (!result || result.length > maxLength || /[\u0000-\u001f\u007f]/.test(result)) {
    throw new Error(`${label} is invalid`);
  }
  return result;
}

function text(value: unknown): string {
  return typeof value === "string" ? value : "";
}

function isFileNotFound(error: unknown): boolean {
  return typeof error === "object" && error !== null && "code" in error && error.code === "ENOENT";
}
