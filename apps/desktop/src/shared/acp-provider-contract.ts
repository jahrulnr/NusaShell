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
  readonly unverified?: boolean;
}

export type AcpAuthStatus = "needs-auth" | "connected";

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
