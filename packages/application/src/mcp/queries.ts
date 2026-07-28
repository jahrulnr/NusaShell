import { PluginId } from "@nusashell/domain";
import { ApplicationError } from "../errors/application-error.js";
import type { QueryHandler } from "../messaging/query-handler.js";
import type { PluginRuntimeManager } from "../plugin/services/plugin-runtime-manager.js";

export interface ListPromptsQuery { readonly kind: "list-prompts"; readonly pluginId: string; }
export interface GetPromptQuery { readonly kind: "get-prompt"; readonly pluginId: string; readonly name: string; readonly args: Readonly<Record<string, string>>; }
export interface ListResourcesQuery { readonly kind: "list-resources"; readonly pluginId: string; }
export interface ListResourceTemplatesQuery { readonly kind: "list-resource-templates"; readonly pluginId: string; }
export interface ReadResourceQuery { readonly kind: "read-resource"; readonly pluginId: string; readonly uri: string; }

function parsePluginId(raw: string): PluginId {
  const result = PluginId.create(raw);
  if (!result.ok) throw new ApplicationError("PLUGIN_NOT_FOUND", `Invalid plugin id: ${result.error.message}`);
  return result.value;
}

export class ListPromptsHandler implements QueryHandler<ListPromptsQuery, { prompts: readonly unknown[] }> {
  constructor(private readonly runtime: PluginRuntimeManager) {}
  async handle(query: ListPromptsQuery) { return { prompts: await this.runtime.listPrompts(parsePluginId(query.pluginId)) }; }
}

export class GetPromptHandler implements QueryHandler<GetPromptQuery, unknown> {
  constructor(private readonly runtime: PluginRuntimeManager) {}
  async handle(query: GetPromptQuery) { return this.runtime.getPrompt(parsePluginId(query.pluginId), query.name, query.args); }
}

export class ListResourcesHandler implements QueryHandler<ListResourcesQuery, { resources: readonly unknown[] }> {
  constructor(private readonly runtime: PluginRuntimeManager) {}
  async handle(query: ListResourcesQuery) { return { resources: await this.runtime.listResources(parsePluginId(query.pluginId)) }; }
}

export class ListResourceTemplatesHandler implements QueryHandler<ListResourceTemplatesQuery, { resourceTemplates: readonly unknown[] }> {
  constructor(private readonly runtime: PluginRuntimeManager) {}
  async handle(query: ListResourceTemplatesQuery) { return { resourceTemplates: await this.runtime.listResourceTemplates(parsePluginId(query.pluginId)) }; }
}

export class ReadResourceHandler implements QueryHandler<ReadResourceQuery, unknown> {
  constructor(private readonly runtime: PluginRuntimeManager) {}
  async handle(query: ReadResourceQuery) { return this.runtime.readResource(parsePluginId(query.pluginId), query.uri); }
}
