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
  | "system.ping"
  | "system.version"
  | "job.add"
  | "job.list"
  | "job.set-enabled"
  | "job.run"
  | "job.remove"
  | "job.output"
  | "job.validate-schedule"
  | "acp.run"
  | "acp.cancel"
  | "acp.permission_answer"
  | "acp.ask_answer"
  | "acp.session_info"
  | "acp.probe"
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
  | "agent.context"
  | "agent.turn_started"
  | "agent.turn_end"
  | "agent.turn_superseded"
  | "agent.cancel_requested"
  | "agent.learning_updated"
  | "job.completed"
  | "job.failed"
  | "acp.text_delta"
  | "acp.thought_delta"
  | "acp.tool_call"
  | "acp.tool_call_update"
  | "acp.plan"
  | "acp.permission_request"
  | "acp.ask_request"
  | "acp.turn_end"
  | "acp.session_state";

export interface EventEnvelope<TPayload = unknown> {
  readonly kind: "event";
  readonly event: EventType;
  readonly sequence: number;
  readonly payload: TPayload;
}

export type WireMessage = RequestEnvelope | ResponseEnvelope | EventEnvelope;
