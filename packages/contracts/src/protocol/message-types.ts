export type MessageKind = "request" | "response" | "event";

export type RequestMethod =
  | "plugin.start"
  | "plugin.stop"
  | "plugin.restart"
  | "plugin.install"
  | "plugin.uninstall"
  | "plugin.list"
  | "plugin.get"
  | "plugin.state"
  | "tool.call"
  | "tool.cancel"
  | "tool.list"
  | "agent.run"
  | "system.ping"
  | "system.version"
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
  | "tool.call_completed";

export interface EventEnvelope<TPayload = unknown> {
  readonly kind: "event";
  readonly event: EventType;
  readonly sequence: number;
  readonly payload: TPayload;
}

export type WireMessage = RequestEnvelope | ResponseEnvelope | EventEnvelope;
