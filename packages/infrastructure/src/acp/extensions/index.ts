export type { AcpExtensionContext, AcpExtensionHandled, AcpProviderExtension } from "./acp-provider-extension.js";
export { CursorAcpExtension } from "./cursor-acp-extension.js";
export { CodexAcpExtension } from "./codex-acp-extension.js";
export { parsePlanSteps } from "./acp-shared.js";

import type { AcpProviderExtension } from "./acp-provider-extension.js";
import { CursorAcpExtension } from "./cursor-acp-extension.js";
import { CodexAcpExtension } from "./codex-acp-extension.js";

const EXTENSIONS: readonly AcpProviderExtension[] = [new CursorAcpExtension(), new CodexAcpExtension()];

export function resolveAcpExtension(providerId: string): AcpProviderExtension | undefined {
  return EXTENSIONS.find((ext) => ext.matches(providerId));
}
