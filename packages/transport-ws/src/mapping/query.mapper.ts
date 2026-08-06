import type { ParsedRequest } from "@nusashell/contracts";
import type {
  GetPromptQuery,
  GetPluginQuery,
  GetPluginStateQuery,
  ListPluginsQuery,
  ListPromptsQuery,
  ListResourcesQuery,
  ListResourceTemplatesQuery,
  ListToolsQuery,
  ReadResourceQuery,
  SystemPingQuery,
  SystemVersionQuery,
  ListJobsQuery,
  JobOutputQuery,
  ValidateScheduleQuery,
  GetAcpSessionInfoQuery,
  ListPipelinesQuery,
  ListPipelineRunsQuery,
  GetPipelineRunQuery,
  GetActiveTurnQuery,
  ToolJobListQuery,
} from "@nusashell/application";

export function mapToQuery(
  request: ParsedRequest,
):
  | ListPluginsQuery
  | GetPluginQuery
  | GetPluginStateQuery
  | ListToolsQuery
  | ListPromptsQuery
  | GetPromptQuery
  | ListResourcesQuery
  | ListResourceTemplatesQuery
  | ReadResourceQuery
  | SystemPingQuery
  | SystemVersionQuery
  | ListJobsQuery
  | JobOutputQuery
  | ValidateScheduleQuery
  | GetAcpSessionInfoQuery
  | ListPipelinesQuery
  | ListPipelineRunsQuery
  | GetPipelineRunQuery
  | GetActiveTurnQuery
  | ToolJobListQuery
  | null {
  switch (request.method) {
    case "plugin.list":
      return { kind: "list-plugins" } as ListPluginsQuery;
    case "plugin.get":
      return {
        kind: "get-plugin",
        pluginId: request.payload.pluginId,
      } as GetPluginQuery;
    case "plugin.state":
      return {
        kind: "get-plugin-state",
        pluginId: request.payload.pluginId,
      } as GetPluginStateQuery;
    case "tool.list":
      return {
        kind: "list-tools",
        pluginId: request.payload.pluginId,
      } as ListToolsQuery;
    case "prompt.list":
      return { kind: "list-prompts", pluginId: request.payload.pluginId } as ListPromptsQuery;
    case "prompt.get":
      return {
        kind: "get-prompt",
        pluginId: request.payload.pluginId,
        name: request.payload.name,
        args: request.payload.args,
      } as GetPromptQuery;
    case "resource.list":
      return { kind: "list-resources", pluginId: request.payload.pluginId } as ListResourcesQuery;
    case "resource.template.list":
      return {
        kind: "list-resource-templates",
        pluginId: request.payload.pluginId,
      } as ListResourceTemplatesQuery;
    case "resource.read":
      return {
        kind: "read-resource",
        pluginId: request.payload.pluginId,
        uri: request.payload.uri,
      } as ReadResourceQuery;
    case "system.ping":
      return { kind: "system-ping" } as SystemPingQuery;
    case "system.version":
      return { kind: "system-version" } as SystemVersionQuery;
    case "job.list":
      return { kind: "list-jobs" } as ListJobsQuery;
    case "pipeline.list":
      return { kind: "list-pipelines" } as ListPipelinesQuery;
    case "pipeline.runs": {
      const payload = request.payload as { id: string; limit?: number; includeBody?: boolean };
      return {
        kind: "list-pipeline-runs",
        pipelineId: payload.id,
        ...(payload.limit !== undefined ? { limit: payload.limit } : {}),
        ...(payload.includeBody !== undefined ? { includeBody: payload.includeBody } : {}),
      } as ListPipelineRunsQuery;
    }
    case "pipeline.run-get": {
      const payload = request.payload as { runId: string; includeBody?: boolean };
      return {
        kind: "get-pipeline-run",
        runId: payload.runId,
        ...(payload.includeBody !== undefined ? { includeBody: payload.includeBody } : {}),
      } as GetPipelineRunQuery;
    }
    case "job.output":
      return {
        kind: "job-output",
        id: request.payload.id,
        ...(request.payload.limit !== undefined ? { limit: request.payload.limit } : {}),
        ...(request.payload.includeBody !== undefined
          ? { includeBody: request.payload.includeBody }
          : {}),
      } as JobOutputQuery;
    case "job.validate-schedule":
      return { kind: "validate-schedule", schedule: request.payload.schedule } as ValidateScheduleQuery;
    case "acp.session_info":
      return {
        kind: "get-acp-session-info",
        conversationId: request.payload.conversationId,
      } as GetAcpSessionInfoQuery;
    case "agent.get_active_turn":
      return {
        kind: "get-active-turn",
        conversationId: request.payload.conversationId,
      } as GetActiveTurnQuery;
    case "agent.tool_job_list":
      return {
        kind: "tool-job-list",
        conversationId: request.payload.conversationId,
      } as ToolJobListQuery;
    default:
      return null;
  }
}
