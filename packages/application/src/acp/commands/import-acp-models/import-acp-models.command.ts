import type { Command } from "../../../messaging/command.js";
import type { AcpConfigOption } from "../../ports/acp-client.port.js";

export interface ImportAcpModelsCommand extends Command {
  readonly kind: "import-acp-models";
  readonly provider: {
    readonly providerId: string;
    readonly command: string;
    readonly args: readonly string[];
    readonly authMethodId?: string;
    readonly env?: Readonly<Record<string, string>>;
    readonly preferredConfig?: Readonly<Record<string, string | boolean>>;
  };
}

export type ImportAcpModelsResult =
  | { readonly ok: true; readonly configOptions: readonly AcpConfigOption[] }
  | { readonly ok: false; readonly error: string };
