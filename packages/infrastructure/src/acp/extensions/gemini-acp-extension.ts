import type { AcpConfigOption, AcpConfigOptionValue } from "@nusashell/application";
import type {
  AcpConfigOptionApplyDescriptor,
  AcpExtensionHandled,
  AcpProviderExtension,
} from "./acp-provider-extension.js";

/**
 * Gemini CLI ACP extension.
 *
 * Gemini CLI 0.53.1+ speaks ACP but diverges from the baseline in two places:
 *  - `session/new` returns `{ modes, models }` instead of `configOptions`.
 *  - Mode/model changes use `session/set_mode` / `session/set_model`
 *    (with `modeId`/`modelId` params) instead of
 *    `session/set_config_option`. Note: the camelCase
 *    `setSessionMode`/`unstable_setSessionModel` names are the ACP SDK's
 *    TypeScript interface methods, NOT the JSON-RPC wire methods — sending
 *    them verbatim returns -32601 Method not found.
 *
 * This extension normalizes both into the uniform `AcpConfigOption[]` shape the
 * UI expects, so the renderer's config-option picker works for Gemini without
 * provider-specific UI branches.
 *
 * Mode ids: `default`, `autoEdit`, `yolo`, `plan`. `yolo` is Gemini's bypass
 * equivalent (auto-apply all tool permissions).
 */
export class GeminiAcpExtension implements AcpProviderExtension {
  matches(providerId: string): boolean {
    return providerId === "gemini";
  }

  async handleServerRequest(
    _ctx: unknown,
    _method: string,
    _params: Record<string, unknown>,
  ): Promise<AcpExtensionHandled | undefined> {
    return undefined;
  }

  /**
   * Map Gemini `session/new` `{ modes, models }` into two synthetic
   * `AcpConfigOption` entries (`mode` + `model`) so the UI config picker and
   * `ImportAcpModelsCommand` extraction work unchanged.
   */
  normalizeSessionConfig(sessionResult: unknown): readonly AcpConfigOption[] | undefined {
    if (typeof sessionResult !== "object" || sessionResult === null) return undefined;
    const raw = sessionResult as {
      modes?: unknown;
      models?: unknown;
      configOptions?: unknown;
    };
    // Only engage when the Gemini-specific `modes`/`models` shape is present.
    // If a future Gemini version ships baseline `configOptions`, defer to the
    // core parser by returning undefined.
    if (!raw.modes && !raw.models) return undefined;
    const options: AcpConfigOption[] = [];
    const modeOption = toSelectOption("mode", "Mode", raw.modes, "currentModeId", "availableModes", "id");
    if (modeOption) options.push(modeOption);
    const modelOption = toSelectOption("model", "Model", raw.models, "currentModelId", "availableModels", "modelId");
    if (modelOption) options.push(modelOption);
    return options.length > 0 ? options : undefined;
  }

  /**
   * Route `setConfigOption` to the Gemini JSON-RPC method for the given
   * configId. Returns `undefined` for unknown ids so the core client can fall
   * back to `session/set_config_option` if Gemini ever adds baseline support.
   */
  applyConfigOption(
    sessionId: string,
    configId: string,
    value: string | boolean,
  ): AcpConfigOptionApplyDescriptor | undefined {
    if (configId === "mode" && typeof value === "string") {
      return {
        method: "session/set_mode",
        params: { sessionId, modeId: value },
        toConfigOptions: (result) => this.rebuildFromResponse(result, sessionId),
      };
    }
    if (configId === "model" && typeof value === "string") {
      return {
        method: "session/set_model",
        params: { sessionId, modelId: value },
        toConfigOptions: (result) => this.rebuildFromResponse(result, sessionId),
      };
    }
    return undefined;
  }

  /**
   * Rebuild the synthetic configOptions snapshot from a session/set_mode /
   * session/set_model response. Gemini's response shape for these methods is
   * not documented as returning the full modes/models tree, so we only update
   * the `currentValue` of the matching option when the response echoes the new
   * id. When the response is empty, the caller (core client) keeps the prior
   * snapshot and only updates the changed option locally.
   */
  private rebuildFromResponse(_result: unknown, _sessionId: string): readonly AcpConfigOption[] | undefined {
    // Gemini session/set_mode and session/set_model responses do not echo the
    // full modes/models tree. Return undefined so the core client updates the
    // changed option's currentValue locally (it knows configId + value).
    return undefined;
  }
}

/**
 * Build a synthetic select `AcpConfigOption` from a Gemini `{ available*, current*Id }` block.
 */
function toSelectOption(
  id: string,
  name: string,
  block: unknown,
  currentKey: string,
  availableKey: string,
  valueKey: string,
): AcpConfigOption | undefined {
  if (typeof block !== "object" || block === null) return undefined;
  const b = block as Record<string, unknown>;
  const currentValue = typeof b[currentKey] === "string" ? (b[currentKey] as string) : "";
  const available = b[availableKey];
  if (!Array.isArray(available)) {
    return currentValue
      ? { id, name, type: "select", currentValue, options: undefined }
      : undefined;
  }
  const options: AcpConfigOptionValue[] = [];
  for (const entry of available) {
    if (typeof entry !== "object" || entry === null) continue;
    const e = entry as Record<string, unknown>;
    const value = typeof e[valueKey] === "string" ? (e[valueKey] as string) : "";
    if (!value) continue;
    const label = typeof e.name === "string" ? (e.name as string) : value;
    const description = typeof e.description === "string" ? (e.description as string) : undefined;
    options.push({ value, name: label, ...(description ? { description } : {}) });
  }
  if (options.length === 0 && !currentValue) return undefined;
  return {
    id,
    name,
    type: "select",
    currentValue,
    ...(options.length > 0 ? { options } : {}),
  };
}
