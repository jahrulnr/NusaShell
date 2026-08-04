export type { AcpExtensionContext, AcpExtensionHandled, AcpProviderExtension, AcpConfigOptionApplyDescriptor } from "./acp-provider-extension.js";
export { CursorAcpExtension } from "./cursor-acp-extension.js";
export { CodexAcpExtension } from "./codex-acp-extension.js";
export { GeminiAcpExtension } from "./gemini-acp-extension.js";
export { parsePlanSteps } from "./acp-shared.js";

import type { AcpProviderExtension } from "./acp-provider-extension.js";
import { CursorAcpExtension } from "./cursor-acp-extension.js";
import { CodexAcpExtension } from "./codex-acp-extension.js";
import { GeminiAcpExtension } from "./gemini-acp-extension.js";

const EXTENSIONS: readonly AcpProviderExtension[] = [
  new CursorAcpExtension(),
  new CodexAcpExtension(),
  new GeminiAcpExtension(),
];

export function resolveAcpExtension(providerId: string): AcpProviderExtension | undefined {
  return EXTENSIONS.find((ext) => ext.matches(providerId));
}
