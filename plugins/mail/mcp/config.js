import { z } from "zod";

const ServerSchema = z.object({
  host: z.string().trim().min(1).max(253),
  port: z.number().int().min(1).max(65535),
  secure: z.boolean().default(true),
  starttls: z.boolean().default(false),
  rejectUnauthorized: z.literal(true).default(true),
}).superRefine((server, context) => {
  if (server.secure === server.starttls) {
    context.addIssue({
      code: "custom",
      message: "Choose exactly one encrypted transport: implicit TLS or STARTTLS",
    });
  }
});

const AccountSchema = z.object({
  id: z.string().trim().min(1).max(64).regex(/^[a-z0-9][a-z0-9._-]*$/),
  name: z.string().trim().min(1).max(120),
  email: z.string().trim().email().max(320),
  username: z.string().min(1).max(320),
  password: z.string().min(1).max(4096),
  enabled: z.boolean().default(true),
  imap: ServerSchema,
  smtp: ServerSchema,
});

const AccountsSchema = z.array(AccountSchema).max(20);
const MAX_CONFIG_BYTES = 256 * 1024;

/**
 * Parses credentials injected by the NusaShell host when the MCP process starts.
 * The source manifest never contains these values.
 *
 * @param {NodeJS.ProcessEnv | Record<string, string | undefined>} environment
 */
export function loadAccountsFromEnvironment(environment = process.env) {
  const raw = environment.NUSASHELL_MAIL_ACCOUNTS;
  if (!raw) return [];
  if (Buffer.byteLength(raw, "utf8") > MAX_CONFIG_BYTES) {
    throw new Error("Mail account configuration exceeds the 256 KiB limit");
  }

  let input;
  try {
    input = JSON.parse(raw);
  } catch {
    throw new Error("Mail account configuration is not valid JSON");
  }

  const accounts = AccountsSchema.parse(input);
  const seen = new Set();
  for (const account of accounts) {
    if (seen.has(account.id)) {
      throw new Error(`Duplicate account id: ${account.id}`);
    }
    seen.add(account.id);
  }
  return accounts;
}

/**
 * Produces the only account representation allowed in MCP responses.
 * @param {z.infer<typeof AccountSchema>} account
 */
export function publicAccount(account) {
  return {
    id: account.id,
    name: account.name,
    email: account.email,
    enabled: account.enabled,
    auth: "password",
    incoming: {
      host: account.imap.host,
      port: account.imap.port,
      secure: account.imap.secure,
    },
    outgoing: {
      host: account.smtp.host,
      port: account.smtp.port,
      secure: account.smtp.secure,
    },
    capabilities: {
      receive: true,
      send: false,
      drafts: false,
      idle: false,
    },
  };
}

/**
 * @param {readonly z.infer<typeof AccountSchema>[]} accounts
 * @param {string} accountId
 */
export function resolveAccount(accounts, accountId) {
  const account = accounts.find((item) => item.id === accountId);
  if (!account) throw new Error(`Mail account not found: ${accountId}`);
  if (!account.enabled) throw new Error(`Mail account is not enabled: ${accountId}`);
  return account;
}

export { AccountSchema };
