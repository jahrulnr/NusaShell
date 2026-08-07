import { mkdir, readFile, rename, writeFile } from "node:fs/promises";
import { dirname } from "node:path";
import { access } from "node:fs/promises";
import type {
  AcpConfigOptionSummary,
  AcpModelOption,
  AcpProviderConfig,
  AcpProviderManifest,
  AcpProviderPublic,
  AcpProviderSaveInput,
  AcpRoutingPublic,
  AcpRoutingSettings,
} from "../shared/acp-provider-contract.js";
import type { LoggerPort } from "@nusashell/application";
import { computeAcpTryOrder } from "./acp-routing-order.js";

const ACP_PROVIDER_MANIFESTS: readonly AcpProviderManifest[] = [
  {
    id: "cursor",
    displayName: "Cursor",
    monogram: "CU",
    description: "Cursor ACP agent via the `agent acp` CLI.",
    command: process.env.NUSASHELL_CURSOR_AGENT_BIN ?? "agent",
    args: ["acp"],
    // Prefer existing ~/.config/cursor file auth. Do not default to
    // cursor_login — that forces a browser OAuth on every Connect/session.
    // Users can still pick cursor_login under Configure for a fresh login.
    authMethodIds: ["cursor_login"],
    preferredConfig: { mode: "agent" },
    defaultMode: "agent",
    unverified: false,
  },
  {
    id: "devin",
    displayName: "Devin",
    monogram: "DV",
    description: "Devin Local ACP agent via the `devin acp` CLI.",
    command: process.env.NUSASHELL_DEVIN_BIN ?? "devin",
    args: ["acp"],
    // Devin ACP intentionally does not reuse CLI credentials; Connect must
    // invoke the advertised browser/PKCE authentication method.
    authMethodIds: ["devin-browser"],
    preferredConfig: { mode: "bypass" },
    unverified: true,
  },
  {
    id: "codex",
    displayName: "Codex",
    monogram: "CX",
    description: "OpenAI Codex ACP agent via the `codex-acp` adapter.",
    command: process.env.NUSASHELL_CODEX_ACP_BIN ?? "npx",
    args: ["-y", "@agentclientprotocol/codex-acp"],
    authMethodIds: ["api-key"],
    env: { NO_BROWSER: "1", INITIAL_AGENT_MODE: "agent" },
    // ACP exposes this as agent-full-access; it is Codex's YOLO equivalent.
    preferredConfig: { mode: "agent-full-access" },
    defaultMode: "agent-full-access",
    unverified: true,
  },
  {
    id: "claude-code",
    displayName: "Claude Code",
    monogram: "CC",
    description: "Anthropic Claude Code ACP agent.",
    command: "claude-code-acp",
    args: [],
    unverified: true,
  },
  {
    id: "gemini",
    displayName: "Gemini",
    monogram: "GM",
    description: "Google Gemini CLI ACP agent.",
    command: process.env.NUSASHELL_GEMINI_BIN ?? "gemini",
    args: ["--acp"],
    // Gemini CLI 0.53.1+ advertises these auth methods; session/new succeeds
    // without explicit authenticate when a cached OAuth token exists.
    authMethodIds: ["oauth-personal", "gemini-api-key", "vertex-ai", "gateway"],
    // `yolo` is Gemini's bypass equivalent (auto-apply all tool permissions).
    preferredConfig: { mode: "yolo" },
    defaultMode: "yolo",
    unverified: true,
  },
];

export class AcpProviderStore {
  private state: AcpProviderConfig[] | null = null;
  private routing: AcpRoutingSettings | null = null;

  constructor(
    private readonly path: string,
    private readonly routingPath: string,
    private readonly logger?: LoggerPort,
  ) {}

  async list(): Promise<readonly AcpProviderPublic[]> {
    const saved = await this.load();
    const byId = new Map(saved.map((item) => [item.providerId, item]));
    const result: AcpProviderPublic[] = [];
    for (const manifest of ACP_PROVIDER_MANIFESTS) {
      const savedConfig = byId.get(manifest.id);
      const enabled = savedConfig?.enabled ?? false;
      const command = savedConfig?.command ?? manifest.command;
      const args = savedConfig?.args ?? manifest.args;
      const authMethodId = savedConfig?.authMethodId ?? manifest.authMethodId;
      const authStatus = savedConfig?.authStatus;
      const authCheckedAt = savedConfig?.authCheckedAt;
      const authError = savedConfig?.authError;
      const detected = await isCommandOnPath(command);
      const status = !enabled
        ? "disabled"
        : !(command && detected)
          ? "not-configured"
          : authStatus === "connected"
            ? "configured"
            : "needs-auth";
      result.push({
        manifest: { ...manifest, unverified: manifest.unverified ?? false },
        config: {
          providerId: manifest.id,
          enabled,
          command,
          args,
          ...(authMethodId !== undefined ? { authMethodId } : {}),
          ...(authStatus !== undefined ? { authStatus } : {}),
          ...(authCheckedAt !== undefined ? { authCheckedAt } : {}),
          ...(authError !== undefined ? { authError } : {}),
          ...((savedConfig?.preferredConfig ?? manifest.preferredConfig)
            ? { preferredConfig: savedConfig?.preferredConfig ?? manifest.preferredConfig }
            : {}),
          ...(savedConfig?.models ? { models: savedConfig.models } : {}),
          ...(savedConfig?.defaultModelId ? { defaultModelId: savedConfig.defaultModelId } : {}),
          ...(savedConfig?.configOptions ? { configOptions: savedConfig.configOptions } : {}),
        },
        detected,
        status,
      });
    }
    return result;
  }

  async save(input: AcpProviderSaveInput): Promise<readonly AcpProviderPublic[]> {
    const current = await this.load();
    const existing = current.find((item) => item.providerId === input.providerId);
    const next: AcpProviderConfig = {
      providerId: input.providerId,
      enabled: input.enabled !== undefined ? input.enabled : (existing?.enabled ?? false),
      command: input.command !== undefined ? input.command : existing?.command,
      args: input.args !== undefined ? input.args : existing?.args,
      ...(input.authMethodId !== undefined || existing?.authMethodId !== undefined
        ? { authMethodId: input.authMethodId !== undefined ? input.authMethodId : existing?.authMethodId }
        : {}),
      ...(input.authStatus !== undefined || existing?.authStatus !== undefined
        ? { authStatus: input.authStatus !== undefined ? input.authStatus : existing?.authStatus }
        : {}),
      ...(input.authCheckedAt !== undefined || existing?.authCheckedAt !== undefined
        ? { authCheckedAt: input.authCheckedAt !== undefined ? input.authCheckedAt : existing?.authCheckedAt }
        : {}),
      ...(input.authError !== undefined || existing?.authError !== undefined
        ? {
            // Empty string clears a previous error after a successful reconnect.
            ...(input.authError !== undefined
              ? (input.authError ? { authError: input.authError } : {})
              : (existing?.authError ? { authError: existing.authError } : {})),
          }
        : {}),
      ...(input.preferredConfig !== undefined || existing?.preferredConfig !== undefined
        ? { preferredConfig: input.preferredConfig !== undefined ? input.preferredConfig : existing?.preferredConfig }
        : {}),
      ...(input.models !== undefined || existing?.models !== undefined
        ? { models: input.models !== undefined ? input.models : existing?.models }
        : {}),
      ...(input.defaultModelId !== undefined || existing?.defaultModelId !== undefined
        ? { defaultModelId: input.defaultModelId !== undefined ? input.defaultModelId : existing?.defaultModelId }
        : {}),
      ...(input.configOptions !== undefined || existing?.configOptions !== undefined
        ? { configOptions: input.configOptions !== undefined ? input.configOptions : existing?.configOptions }
        : {}),
    };
    const configs = existing
      ? current.map((item) => item.providerId === input.providerId ? next : item)
      : [next, ...current];
    await this.persist(configs);
    return this.list();
  }

  /**
   * Set the default model for a provider: mirror it into `preferredConfig.model`
   * and persist `defaultModelId` so the detail view can show the selection.
   */
  async setDefaultModel(providerId: string, modelId: string): Promise<readonly AcpProviderPublic[]> {
    const provider = await this.getEffective(providerId);
    const preferredConfig = {
      ...(provider?.config.preferredConfig ?? {}),
      model: modelId,
    };
    return this.save({ providerId, defaultModelId: modelId, preferredConfig });
  }

  /**
   * Set the default mode (bypass/yolo) for a provider: mirror it into
   * `preferredConfig.mode` and persist so subagent runs inherit it.
   */
  async setDefaultMode(providerId: string, mode: string): Promise<readonly AcpProviderPublic[]> {
    const provider = await this.getEffective(providerId);
    const preferredConfig = {
      ...(provider?.config.preferredConfig ?? {}),
      mode,
    };
    return this.save({ providerId, preferredConfig });
  }

  async getEffective(providerId: string): Promise<AcpProviderPublic | null> {
    const list = await this.list();
    return list.find((item) => item.manifest.id === providerId) ?? null;
  }

  async getRouting(): Promise<AcpRoutingPublic> {
    const routing = await this.loadRouting();
    const providers = await this.list();
    const connected = providers
      .filter((p) => p.config.enabled && p.config.authStatus === "connected")
      .map((p) => p.manifest.id);
    const order = computeAcpTryOrder({
      ...(routing.defaultProviderId ? { defaultProviderId: routing.defaultProviderId } : {}),
      ...(routing.fallbackProviderIds ? { fallbackProviderIds: routing.fallbackProviderIds } : {}),
      connectedIds: connected,
    });
    return {
      ...(routing.defaultProviderId ? { defaultProviderId: routing.defaultProviderId } : {}),
      ...(routing.fallbackProviderIds ? { fallbackProviderIds: routing.fallbackProviderIds } : {}),
      tryOrder: order,
    };
  }

  async saveRouting(settings: AcpRoutingSettings): Promise<AcpRoutingPublic> {
    const next: AcpRoutingSettings = {
      ...(settings.defaultProviderId !== undefined ? { defaultProviderId: settings.defaultProviderId || undefined } : {}),
      ...(settings.fallbackProviderIds !== undefined ? { fallbackProviderIds: settings.fallbackProviderIds.filter((id): id is string => typeof id === "string" && id.length > 0) } : {}),
    };
    await this.persistRouting(next);
    return this.getRouting();
  }

  /** Resolve the effective try-order for a subagent spawn. */
  async resolveTryOrder(): Promise<readonly string[]> {
    const routing = await this.getRouting();
    this.logger?.info(`acp.resolveTryOrder tryOrder=[${routing.tryOrder.join(",")}]`);
    return routing.tryOrder;
  }

  private async loadRouting(): Promise<AcpRoutingSettings> {
    if (this.routing) return this.routing;
    try {
      const raw = JSON.parse(await readFile(this.routingPath, "utf8")) as unknown;
      this.routing = normalizeRouting(raw);
    } catch (error) {
      if (isFileNotFound(error)) {
        this.routing = {};
      } else {
        throw new Error("Could not load ACP routing settings", { cause: error });
      }
    }
    return this.routing;
  }

  private async persistRouting(settings: AcpRoutingSettings): Promise<void> {
    await mkdir(dirname(this.routingPath), { recursive: true });
    const temporaryPath = `${this.routingPath}.tmp`;
    await writeFile(temporaryPath, JSON.stringify(settings, null, 2), { mode: 0o600 });
    await rename(temporaryPath, this.routingPath);
    this.routing = settings;
  }

  private async load(): Promise<AcpProviderConfig[]> {
    if (this.state) return this.state;
    try {
      const raw = JSON.parse(await readFile(this.path, "utf8")) as unknown;
      this.state = normalizeConfigs(raw);
    } catch (error) {
      if (isFileNotFound(error)) {
        this.state = [];
      } else {
        throw new Error("Could not load ACP provider settings", { cause: error });
      }
    }
    return this.state;
  }

  private async persist(configs: AcpProviderConfig[]): Promise<void> {
    await mkdir(dirname(this.path), { recursive: true });
    const temporaryPath = `${this.path}.tmp`;
    await writeFile(temporaryPath, JSON.stringify(configs, null, 2), { mode: 0o600 });
    await rename(temporaryPath, this.path);
    this.state = configs;
  }
}

async function isCommandOnPath(command: string | undefined): Promise<boolean> {
  if (!command) return false;
  const firstToken = command.split(/\s+/)[0];
  if (!firstToken) return false;
  try {
    await access(firstToken);
    return true;
  } catch {
    if (firstToken.includes("/")) return false;
    // For commands without a path, rely on the shell. This is a shallow check.
    return true;
  }
}

function normalizeConfigs(value: unknown): AcpProviderConfig[] {
  if (!Array.isArray(value)) return [];
  return value.flatMap((item) => {
    if (typeof item !== "object" || item === null) return [];
    const candidate = item as Partial<AcpProviderConfig>;
    if (typeof candidate.providerId !== "string" || typeof candidate.enabled !== "boolean") return [];
    let command = candidate.command;
    let args = Array.isArray(candidate.args) ? candidate.args.filter((arg): arg is string => typeof arg === "string") : undefined;
    // v0.0.49 migration: the Codex manifest default changed from `codex-acp`
    // to `npx -y @agentclientprotocol/codex-acp`. Drop stale saved commands
    // that match the old default so the new manifest default takes over.
    let authStatus = candidate.authStatus;
    let authError = candidate.authError;
    let authCheckedAt = candidate.authCheckedAt;
    if (candidate.providerId === "codex" && command === "codex-acp") {
      command = undefined;
      args = undefined;
      authStatus = undefined;
      authError = undefined;
      authCheckedAt = undefined;
    }
    return [{
      providerId: candidate.providerId,
      enabled: candidate.enabled,
      command,
      args,
      ...(typeof candidate.authMethodId === "string" ? { authMethodId: candidate.authMethodId } : {}),
      ...(authStatus === "connected" || authStatus === "needs-auth" ? { authStatus } : {}),
      ...(typeof authCheckedAt === "string" ? { authCheckedAt } : {}),
      ...(typeof authError === "string" ? { authError } : {}),
      ...(candidate.preferredConfig && typeof candidate.preferredConfig === "object" && !Array.isArray(candidate.preferredConfig)
        ? { preferredConfig: normalizePreferredConfig(candidate.preferredConfig as Record<string, unknown>) }
        : {}),
      ...(Array.isArray(candidate.models) ? { models: normalizeModels(candidate.models) } : {}),
      ...(typeof candidate.defaultModelId === "string" && candidate.defaultModelId ? { defaultModelId: candidate.defaultModelId } : {}),
      ...(Array.isArray(candidate.configOptions) ? { configOptions: normalizeConfigOptions(candidate.configOptions) } : {}),
    }];
  });
}

function normalizeModels(value: unknown): AcpModelOption[] {
  if (!Array.isArray(value)) return [];
  return value.flatMap((item) => {
    if (typeof item !== "object" || item === null) return [];
    const candidate = item as Partial<AcpModelOption>;
    if (typeof candidate.id !== "string" || typeof candidate.label !== "string") return [];
    return [{
      id: candidate.id,
      label: candidate.label,
      ...(typeof candidate.description === "string" ? { description: candidate.description } : {}),
    }];
  });
}

function normalizeConfigOptions(value: unknown): AcpConfigOptionSummary[] {
  if (!Array.isArray(value)) return [];
  return value.flatMap((item) => {
    if (typeof item !== "object" || item === null) return [];
    const candidate = item as Partial<AcpConfigOptionSummary>;
    if (typeof candidate.id !== "string" || typeof candidate.name !== "string") return [];
    if (candidate.type !== "select" && candidate.type !== "boolean") return [];
    const currentValue = candidate.currentValue;
    if (candidate.type === "boolean") {
      if (typeof currentValue !== "boolean") return [];
    } else {
      if (typeof currentValue !== "string") return [];
    }
    return [{
      id: candidate.id,
      name: candidate.name,
      type: candidate.type,
      currentValue,
      ...(Array.isArray(candidate.options) ? { options: candidate.options.flatMap((opt) => {
        if (typeof opt !== "object" || opt === null) return [];
        const o = opt as { value?: unknown; name?: unknown; description?: unknown };
        if (typeof o.value !== "string" || typeof o.name !== "string") return [];
        return [{ value: o.value, name: o.name, ...(typeof o.description === "string" ? { description: o.description } : {}) }];
      }) } : {}),
      ...(typeof candidate.description === "string" ? { description: candidate.description } : {}),
      ...(typeof candidate.category === "string" ? { category: candidate.category } : {}),
    }];
  });
}

function normalizePreferredConfig(value: Record<string, unknown>): Record<string, string | boolean> {
  const out: Record<string, string | boolean> = {};
  for (const [key, val] of Object.entries(value)) {
    if (typeof val === "string" || typeof val === "boolean") out[key] = val;
  }
  return out;
}

function normalizeRouting(value: unknown): AcpRoutingSettings {
  if (typeof value !== "object" || value === null || Array.isArray(value)) return {};
  const candidate = value as Partial<AcpRoutingSettings>;
  return {
    ...(typeof candidate.defaultProviderId === "string" && candidate.defaultProviderId
      ? { defaultProviderId: candidate.defaultProviderId }
      : {}),
    ...(Array.isArray(candidate.fallbackProviderIds)
      ? { fallbackProviderIds: candidate.fallbackProviderIds.filter((id): id is string => typeof id === "string" && id.length > 0) }
      : {}),
  };
}

function isFileNotFound(error: unknown): boolean {
  return typeof error === "object" && error !== null && "code" in error && error.code === "ENOENT";
}
