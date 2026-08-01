import type { Command } from "../../../messaging/command.js";

export interface ProbeAcpProviderCommand extends Command {
  readonly kind: "probe-acp-provider";
  readonly provider: {
    readonly providerId: string;
    readonly command: string;
    readonly args: readonly string[];
    readonly authMethodId?: string;
    readonly env?: Readonly<Record<string, string>>;
  };
}

export interface ProbeAcpProviderResult {
  readonly ok: boolean;
  readonly error?: string;
}
