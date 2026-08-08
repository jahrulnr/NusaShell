import { realpath, stat } from "node:fs/promises";
import { basename, relative, resolve } from "node:path";
import { PluginId } from "@nusashell/domain";
import { ApplicationError } from "../../errors/application-error.js";
import type { PluginInstallerPort } from "../../plugin/ports/plugin-installer.port.js";
import type { PluginRepositoryPort } from "../../plugin/ports/plugin-repository.port.js";
import type { PluginRuntimeManager } from "../../plugin/services/plugin-runtime-manager.js";
import type { AskQuestionService } from "./ask-question-service.js";
import { optionalString, requireString } from "./gateway-utils.js";

export interface McpPluginRegistrationDeps {
  readonly installer?: PluginInstallerPort | null;
  readonly repository?: PluginRepositoryPort;
  readonly runtimeManager: PluginRuntimeManager;
  readonly syncPlugins?: () => Promise<void>;
  readonly userPluginsRoot?: string;
  readonly bundledPluginsRoot?: string;
  readonly askQuestions?: AskQuestionService;
}

export async function execMcpRegister(
  deps: McpPluginRegistrationDeps,
  args: Readonly<Record<string, unknown>>,
  turnId: string,
  callId: string,
  interactive: boolean,
): Promise<unknown> {
  assertConfigured(deps);
  assertInteractive(interactive);
  const folder = await resolveUserPluginFolder(deps.userPluginsRoot!, args);
  await confirm(deps.askQuestions, turnId, callId, `Register the MCP plugin in ${folder}?`, {
    confirmLabel: "Register",
  });

  const result = await deps.installer!.installFromPath(folder);
  await deps.syncPlugins?.();
  return {
    ok: true,
    data: result,
    meta: { registered: true, userPluginsRoot: deps.userPluginsRoot },
  };
}

export async function execMcpUnregister(
  deps: McpPluginRegistrationDeps,
  args: Readonly<Record<string, unknown>>,
  turnId: string,
  callId: string,
  interactive: boolean,
): Promise<unknown> {
  assertConfigured(deps);
  assertInteractive(interactive);
  const pluginIdValue = requireString(args.pluginId, "pluginId");
  const pluginId = PluginId.create(pluginIdValue);
  if (!pluginId.ok) throw new ApplicationError("PLUGIN_NOT_FOUND", `Invalid plugin id: ${pluginId.error.message}`);
  const plugin = await deps.repository!.findById(pluginId.value);
  if (!plugin) throw new ApplicationError("PLUGIN_NOT_FOUND", `Plugin not found: ${pluginIdValue}`);

  const userRoot = await realpath(deps.userPluginsRoot!);
  const installPath = await realpath(plugin.installPath).catch(() => "");
  if (!isDirectChild(userRoot, installPath)) {
    throw new ApplicationError("AGENT_INVALID_INPUT", "Plugin is not inside the user plugins root; cannot be unregistered");
  }
  if (basename(installPath) !== pluginIdValue) {
    throw new ApplicationError("AGENT_INVALID_INPUT", "Plugin folder does not match the requested plugin id");
  }

  await confirm(deps.askQuestions, turnId, callId, `Unregister and remove the MCP plugin ${pluginIdValue}?`, {
    confirmLabel: "Unregister",
  });
  await deps.runtimeManager.removePlugin(pluginId.value);
  await deps.repository!.remove(pluginId.value);
  await deps.installer!.uninstall(pluginIdValue);
  await deps.syncPlugins?.();
  return { ok: true, data: { pluginId: pluginIdValue }, meta: { unregistered: true } };
}

function assertConfigured(deps: McpPluginRegistrationDeps): void {
  if (!deps.installer || !deps.repository || !deps.userPluginsRoot) {
    throw new ApplicationError("AGENT_INVALID_INPUT", "MCP plugin registration is not configured");
  }
}

function assertInteractive(interactive: boolean): void {
  if (!interactive) {
    throw new ApplicationError("AGENT_INVALID_INPUT", "MCP plugin registration is only available during interactive agent turns");
  }
}

async function resolveUserPluginFolder(root: string, args: Readonly<Record<string, unknown>>): Promise<string> {
  const folderArg = optionalString(args.folder);
  const pathArg = optionalString(args.path);
  if (!folderArg && !pathArg) throw new ApplicationError("AGENT_INVALID_INPUT", "folder or path is required");
  if (folderArg && pathArg) throw new ApplicationError("AGENT_INVALID_INPUT", "provide folder or path, not both");
  const rootPath = await realpath(root);
  const candidate = resolve(rootPath, folderArg || pathArg);
  if (!isDirectChild(rootPath, candidate)) {
    throw new ApplicationError("AGENT_INVALID_INPUT", "Plugin must be exactly one folder under the user plugins root");
  }
  const canonical = await realpath(candidate).catch(() => "");
  if (!canonical || !isDirectChild(rootPath, canonical) || !(await stat(canonical)).isDirectory()) {
    throw new ApplicationError("AGENT_INVALID_INPUT", "Plugin folder does not exist under the user plugins root");
  }
  return canonical;
}

function isDirectChild(root: string, candidate: string): boolean {
  const child = relative(resolve(root), resolve(candidate));
  return child.length > 0 && !child.startsWith("..") && !child.includes("/") && !child.includes("\\");
}

async function confirm(
  askQuestions: AskQuestionService | undefined,
  turnId: string,
  callId: string,
  question: string,
  labels: { readonly confirmLabel: string },
): Promise<void> {
  if (!askQuestions) throw new ApplicationError("AGENT_INVALID_INPUT", "Confirmation is not available in this runtime");
  const answer = await askQuestions.ask(turnId, callId, {
    question,
    options: [
      { id: "confirm", label: labels.confirmLabel, default: true },
      { id: "cancel", label: "Cancel" },
    ],
    allowFreeText: false,
    multiSelect: false,
  });
  if (!answer.data.optionIds?.includes("confirm")) {
    throw new ApplicationError("AGENT_INVALID_INPUT", "MCP plugin registration cancelled");
  }
}
