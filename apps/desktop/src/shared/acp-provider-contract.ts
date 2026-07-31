export interface AcpProviderManifest {
  readonly id: string;
  readonly displayName: string;
  readonly monogram: string;
  readonly description: string;
  readonly command: string;
  readonly args: readonly string[];
  readonly authMethodId?: string;
  readonly unverified?: boolean;
}

export interface AcpProviderConfig {
  readonly providerId: string;
  readonly enabled: boolean;
  readonly command?: string | undefined;
  readonly args?: readonly string[] | undefined;
}

export interface AcpProviderPublic {
  readonly manifest: AcpProviderManifest;
  readonly config: AcpProviderConfig;
  readonly detected: boolean;
  readonly status: "configured" | "not-configured" | "disabled";
}

export interface AcpProviderSaveInput {
  readonly providerId: string;
  readonly enabled?: boolean;
  readonly command?: string;
  readonly args?: readonly string[];
}
