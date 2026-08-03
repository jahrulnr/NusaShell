export interface AcpProviderDescriptor {
  readonly providerId: string;
  readonly command: string;
  readonly args: readonly string[];
  readonly authMethodId?: string;
  readonly env?: Readonly<Record<string, string>>;
  /** Config options applied after session/new, such as provider-specific mode. */
  readonly preferredConfig?: Readonly<Record<string, string | boolean>>;
}

export type AcpContentBlock =
  | { readonly type: "text"; readonly text: string }
  | { readonly type: "image"; readonly data: string; readonly mimeType: string };

export type AcpToolKind = "terminal" | "read" | "edit" | "unknown";
export type AcpToolStatus = "pending" | "running" | "ok" | "fail";

export interface AcpToolCall {
  readonly id: string;
  readonly title: string;
  readonly kind: AcpToolKind;
  readonly status: AcpToolStatus;
  readonly summary: string;
  readonly rawInput?: unknown;
}

export interface AcpPlanStep {
  readonly id: string;
  readonly text: string;
  readonly done: boolean;
}

export interface AcpPermissionOption {
  readonly optionId: string;
  readonly name: string;
  readonly kind?: "allow" | "deny" | "allow_once" | "allow_always" | undefined;
}

export interface AcpPermissionRequest {
  readonly requestId: string;
  readonly toolTitle: string;
  readonly detail?: string | undefined;
  readonly options: readonly AcpPermissionOption[];
}

export interface AcpPermissionAnswer {
  readonly optionId: string;
}

export interface AcpAskOption {
  readonly optionId: string;
  readonly name: string;
}

export interface AcpAskRequest {
  readonly requestId: string;
  readonly question: string;
  readonly options?: readonly AcpAskOption[] | undefined;
  readonly multiSelect?: boolean | undefined;
  readonly allowFreeText?: boolean | undefined;
}

export interface AcpAskAnswer {
  readonly optionIds?: readonly string[] | undefined;
  readonly text?: string | undefined;
}

export type AcpClientEvent =
  | { readonly type: "acp.text_delta"; readonly traceId: string; readonly delta: string; readonly messageId?: string | undefined }
  | { readonly type: "acp.thought_delta"; readonly traceId: string; readonly delta: string }
  | { readonly type: "acp.tool_call"; readonly traceId: string; readonly call: AcpToolCall }
  | { readonly type: "acp.tool_call_update"; readonly traceId: string; readonly callId: string; readonly status: AcpToolStatus; readonly summary?: string | undefined }
  | { readonly type: "acp.plan"; readonly traceId: string; readonly steps: readonly AcpPlanStep[] }
  | { readonly type: "acp.session_state"; readonly traceId: string; readonly conversationId: string; readonly state: AcpSessionState }
  | { readonly type: "acp.turn_end"; readonly traceId: string; readonly ok: boolean; readonly error?: string | undefined };

export interface AcpConfigOptionValue {
  readonly value: string;
  readonly name: string;
  readonly description?: string | undefined;
}

export interface AcpConfigOption {
  readonly id: string;
  readonly name: string;
  readonly description?: string | undefined;
  readonly category?: string | undefined;
  readonly type: "select" | "boolean";
  readonly currentValue: string | boolean;
  readonly options?: readonly AcpConfigOptionValue[] | undefined;
}

export type AcpSessionState = "idle" | "starting" | "running" | "error" | "cancelled";

export interface AcpClientSink {
  publish(event: AcpClientEvent): void | Promise<void>;
  requestPermission(request: AcpPermissionRequest): Promise<AcpPermissionAnswer>;
  askQuestion(request: AcpAskRequest): Promise<AcpAskAnswer>;
}

export interface AcpClientPort {
  /**
   * Start (or restart) a session with the given provider.
   * Returns a stable session id for the conversation.
   */
  startSession(
    conversationId: string,
    provider: AcpProviderDescriptor,
    cwd: string,
    sink: AcpClientSink,
  ): Promise<string>;

  /**
   * Send a user prompt for the active session.
   */
  prompt(traceId: string, conversationId: string, content: readonly AcpContentBlock[]): Promise<void>;

  /**
   * Cancel an in-progress turn.
   */
  cancel(traceId: string, conversationId: string): Promise<void>;

  /**
   * Release all resources and kill the child process for a session.
   */
  closeSession(conversationId: string): Promise<void>;

  /**
   * Get the current config options for a session (models, modes, etc).
   */
  getConfigOptions(conversationId: string): readonly AcpConfigOption[];

  /**
   * Change a session config option (e.g. model, mode).
   * Returns the full updated config options list.
   */
  setConfigOption(conversationId: string, configId: string, value: string | boolean): Promise<readonly AcpConfigOption[]>;
}
