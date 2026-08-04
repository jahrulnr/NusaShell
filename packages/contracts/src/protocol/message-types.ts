export type MessageKind = "request" | "response" | "event";

export type RequestMethod =
  | "plugin.start"
  | "plugin.stop"
  | "plugin.restart"
  | "plugin.install"
  | "plugin.uninstall"
  | "plugin.autostart"
  | "plugin.list"
  | "plugin.get"
  | "plugin.state"
  | "tool.call"
  | "tool.cancel"
  | "tool.list"
  | "prompt.list"
  | "prompt.get"
  | "resource.list"
  | "resource.template.list"
  | "resource.read"
  | "agent.run"
  | "agent.cancel"
  | "agent.ask_answer"
  | "agent.get_active_turn"
  | "agent.manage_todos"
  | "agent.todos_get"
  | "agent.tool_job_list"
  | "agent.tool_job_kill"
  | "system.ping"
  | "system.version"
  | "job.add"
  | "job.update"
  | "job.list"
  | "job.set-enabled"
  | "job.run"
  | "job.cancel"
  | "job.remove"
  | "job.output"
  | "job.validate-schedule"
  | "pipeline.add"
  | "pipeline.update"
  | "pipeline.remove"
  | "pipeline.run"
  | "pipeline.list"
  | "acp.run"
  | "acp.cancel"
  | "acp.permission_answer"
  | "acp.ask_answer"
  | "acp.session_info"
  | "acp.probe"
  | "acp.ensure_session"
  | "acp.set_config_option"
  | "subscribe"
  | "unsubscribe";

export interface RequestEnvelope<TPayload = unknown> {
  readonly kind: "request";
  readonly id: string;
  readonly method: RequestMethod;
  readonly protocolVersion?: string;
  readonly payload: TPayload;
}

export interface SuccessResponseEnvelope<TResult = unknown> {
  readonly kind: "response";
  readonly id: string;
  readonly ok: true;
  readonly result: TResult;
}

export interface ErrorResponseEnvelope {
  readonly kind: "response";
  readonly id: string;
  readonly ok: false;
  readonly error: {
    readonly code: string;
    readonly message: string;
    readonly details?: Readonly<Record<string, unknown>>;
  };
}

export type ResponseEnvelope<TResult = unknown> =
  | SuccessResponseEnvelope<TResult>
  | ErrorResponseEnvelope;

export type EventType =
  | "plugin.installed"
  | "plugin.uninstalled"
  | "plugin.started"
  | "plugin.stopped"
  | "plugin.crashed"
  | "plugin.state_changed"
  | "tool.call_completed"
  | "agent.text_delta"
  | "agent.reasoning_delta"
  | "agent.tool_call_start"
  | "agent.tool_call_end"
  | "agent.ask_request"
  | "agent.context"
  | "agent.turn_started"
  | "agent.turn_end"
  | "agent.turn_superseded"
  | "agent.cancel_requested"
  | "agent.learning_updated"
  | "agent.todo_updated"
  | "agent.tool_job_started"
  | "agent.tool_job_update"
  | "agent.tool_job_ended"
  | "job.completed"
  | "job.failed"
  | "job.started"
  | "job.cancelled"
  | "acp.text_delta"
  | "acp.thought_delta"
  | "acp.tool_call"
  | "acp.tool_call_update"
  | "acp.plan"
  | "acp.permission_request"
  | "acp.ask_request"
  | "acp.turn_end"
  | "acp.session_state"
  | "subagent.run_started"
  | "subagent.run_ended";

export interface EventEnvelope<TPayload = unknown> {
  readonly kind: "event";
  readonly event: EventType;
  readonly sequence: number;
  readonly payload: TPayload;
}

export type WireMessage = RequestEnvelope | ResponseEnvelope | EventEnvelope;
