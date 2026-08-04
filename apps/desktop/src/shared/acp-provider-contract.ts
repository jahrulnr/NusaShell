export interface AcpProviderManifest {
  readonly id: string;
  readonly displayName: string;
  readonly monogram: string;
  readonly description: string;
  readonly command: string;
  readonly args: readonly string[];
  readonly authMethodId?: string;
  /** UI hint only — auth method ids the adapter may advertise (seeded from live probe). */
  readonly authMethodIds?: readonly string[];
  /** Default spawn env merged under process.env (e.g. NO_BROWSER, INITIAL_AGENT_MODE). */
  readonly env?: Readonly<Record<string, string>>;
  /** ACP session config applied after session/new (for example mode). */
  readonly preferredConfig?: Readonly<Record<string, string | boolean>>;
  /** Default mode applied when the user enables bypass/yolo (e.g. Codex `agent-full-access`, Cursor `agent`). */
  readonly defaultMode?: string;
  readonly unverified?: boolean;
}

export type AcpAuthStatus = "needs-auth" | "connected";

/** A model option discovered from a probe session's `configOptions.model` list. */
export interface AcpModelOption {
  readonly id: string;
  readonly label: string;
  readonly description?: string;
}

/** Plain-data snapshot of an ACP config option for persistence and UI rendering. */
export interface AcpConfigOptionSummary {
  readonly id: string;
  readonly name: string;
  readonly type: "select" | "boolean";
  readonly currentValue: string | boolean;
  readonly options?: readonly { readonly value: string; readonly name: string; readonly description?: string }[];
  readonly description?: string;
  readonly category?: string;
}

export interface AcpProviderConfig {
  readonly providerId: string;
  readonly enabled: boolean;
  readonly command?: string | undefined;
  readonly args?: readonly string[] | undefined;
  /** Optional auth method chosen in Configure (overrides manifest default). */
  readonly authMethodId?: string | undefined;
  readonly authStatus?: AcpAuthStatus | undefined;
  readonly authCheckedAt?: string | undefined;
  readonly authError?: string | undefined;
  /** Per-provider config values applied on subagent spawn (e.g. mode, model). */
  readonly preferredConfig?: Readonly<Record<string, string | boolean>> | undefined;
  /** Models discovered from a probe session's `configOptions.model` list. */
  readonly models?: readonly AcpModelOption[] | undefined;
  /** Id of the model selected as default (mirrored into `preferredConfig.model`). */
  readonly defaultModelId?: string | undefined;
  /** Snapshot of config options from the last probe (for the detail view). */
  readonly configOptions?: readonly AcpConfigOptionSummary[] | undefined;
}

export interface AcpProviderPublic {
  readonly manifest: AcpProviderManifest;
  readonly config: AcpProviderConfig;
  readonly detected: boolean;
  readonly status: "configured" | "not-configured" | "disabled" | "needs-auth";
}

export interface AcpProviderSaveInput {
  readonly providerId: string;
  readonly enabled?: boolean;
  readonly command?: string;
  readonly args?: readonly string[];
  readonly authMethodId?: string;
  readonly authStatus?: AcpAuthStatus;
  readonly authCheckedAt?: string;
  readonly authError?: string;
  readonly preferredConfig?: Readonly<Record<string, string | boolean>>;
  readonly models?: readonly AcpModelOption[];
  readonly defaultModelId?: string;
  readonly configOptions?: readonly AcpConfigOptionSummary[];
}

/** ACP routing preferences for subagent delegation. */
export interface AcpRoutingSettings {
  readonly defaultProviderId?: string | undefined;
  readonly fallbackProviderIds?: readonly string[] | undefined;
}

export interface AcpRoutingPublic extends AcpRoutingSettings {
  /** Effective try-order resolved from default + fallback (connected providers only). */
  readonly tryOrder: readonly string[];
}
