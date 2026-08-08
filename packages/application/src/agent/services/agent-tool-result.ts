/**
 * Agent tool-result dual representation.
 *
 * Canonical typed model for tool results that preserves MCP structure
 * (content / structuredContent / isError) on ingestion, projects a
 * model-facing text string, and tracks truncation explicitly.
 *
 * @see docs/architecture/agent-runtime.md "Tool result dual representation"
 */

// ---------------------------------------------------------------------------
// Types
// ---------------------------------------------------------------------------

export type AgentToolStatus = "success" | "error" | "cancelled" | "timeout";

export type AgentToolContent =
  | { readonly type: "text"; readonly text: string }
  | { readonly type: "json"; readonly data: unknown };

export interface AgentToolResultMeta {
  readonly truncated: boolean;
  readonly originalChars?: number;
  readonly returnedChars?: number;
  readonly durationMs?: number;
  readonly exitCode?: number | null;
  readonly nextCursor?: string;
  readonly dataIsUntrusted: boolean;
}

export interface AgentToolResultError {
  readonly code: string;
  readonly message: string;
  readonly retryable: boolean;
}

export interface AgentToolResult {
  readonly callId: string;
  readonly toolName: string;
  readonly status: AgentToolStatus;
  readonly content: readonly AgentToolContent[];
  readonly structuredContent?: Record<string, unknown>;
  readonly metadata: AgentToolResultMeta;
  readonly error?: AgentToolResultError;
  /** Exact model string after first projection; rehydrate must reuse this. */
  modelOutput?: string;
}

// ---------------------------------------------------------------------------
// MCP ingestion types (infrastructure-facing DTO)
// ---------------------------------------------------------------------------

export interface McpContentPart {
  readonly type: string;
  readonly text?: string;
  readonly data?: string;
  readonly mimeType?: string;
}

export interface McpRawResult {
  readonly content?: readonly McpContentPart[];
  readonly isError?: boolean;
  readonly structuredContent?: unknown;
}

export type McpIngestedResult =
  | {
    readonly kind: "ok";
    readonly structuredContent?: unknown;
    readonly content: readonly McpContentPart[];
  }
  | {
    readonly kind: "error";
    readonly message: string;
    readonly content: readonly McpContentPart[];
    readonly structuredContent?: unknown;
  };

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

function isMcpToolName(name: string): boolean {
  return name.startsWith("mcp_");
}

function defaultMeta(name: string, truncated = false): AgentToolResultMeta {
  return { truncated, dataIsUntrusted: isMcpToolName(name) };
}

const UNTRUSTED_RESULT_OPEN = "<untrusted_tool_result";
const UNTRUSTED_RESULT_CLOSE = "</untrusted_tool_result>";
const UNTRUSTED_DELIMITER_RE = /untrusted[-_]tool[-_]result/gi;

/**
 * The provider receives tool output as text, so keep the trust boundary in
 * that same text. This is intentionally compact: every MCP result gets clear
 * start/end tags without spending a paragraph of prompt budget per call.
 */
function wrapUntrustedToolResult(toolName: string, status: AgentToolStatus, body: string): string {
  if (!isMcpToolName(toolName)) return body;
  const safe = body.replace(UNTRUSTED_DELIMITER_RE, "untrusted tool result");
  return [
    `${UNTRUSTED_RESULT_OPEN} source="${toolName}" status="${status}">`,
    safe,
    UNTRUSTED_RESULT_CLOSE,
  ].join("\n");
}

// ---------------------------------------------------------------------------
// Factories
// ---------------------------------------------------------------------------

export function successToolResult(
  callId: string,
  toolName: string,
  payload: unknown,
  extra?: Partial<Pick<AgentToolResultMeta, "durationMs" | "exitCode" | "nextCursor">>,
): AgentToolResult {
  const content: AgentToolContent[] =
    typeof payload === "string"
      ? [{ type: "text", text: payload }]
      : [{ type: "json", data: payload }];
  const structuredContent =
    typeof payload === "object" && payload !== null && !Array.isArray(payload)
      ? payload as Record<string, unknown>
      : undefined;
  return {
    callId,
    toolName,
    status: "success",
    content,
    ...(structuredContent ? { structuredContent } : {}),
    metadata: { ...defaultMeta(toolName), ...extra },
  };
}

export function errorToolResult(
  callId: string,
  toolName: string,
  code: string,
  message: string,
  retryable = false,
  extra?: Partial<Pick<AgentToolResultMeta, "durationMs">>,
): AgentToolResult {
  return {
    callId,
    toolName,
    status: "error",
    content: [],
    metadata: { ...defaultMeta(toolName), ...extra },
    error: { code, message, retryable },
  };
}

export function cancelledToolResult(callId: string, toolName: string): AgentToolResult {
  return {
    callId,
    toolName,
    status: "cancelled",
    content: [],
    metadata: defaultMeta(toolName),
    error: { code: "TOOL_CANCELLED", message: "Tool call was cancelled", retryable: false },
  };
}

export function timeoutToolResult(callId: string, toolName: string): AgentToolResult {
  return {
    callId,
    toolName,
    status: "timeout",
    content: [],
    metadata: defaultMeta(toolName),
    error: { code: "TOOL_TIMEOUT", message: "Tool call timed out", retryable: true },
  };
}

// ---------------------------------------------------------------------------
// fromGatewayValue — wraps meta-tool plain objects
// ---------------------------------------------------------------------------

export function fromGatewayValue(
  call: { readonly id: string; readonly name: string; readonly args?: Readonly<Record<string, unknown>> },
  value: unknown,
  extra?: Partial<Pick<AgentToolResultMeta, "durationMs" | "exitCode" | "nextCursor">>,
): AgentToolResult {
  return successToolResult(call.id, call.name, value, extra);
}

// ---------------------------------------------------------------------------
// fromThrownError — maps thrown errors to status codes
// ---------------------------------------------------------------------------

export function fromThrownError(
  call: { readonly id: string; readonly name: string },
  error: unknown,
): AgentToolResult {
  const message = error instanceof Error ? error.message : String(error ?? "Unknown tool error");
  const lower = message.toLowerCase();
  // Timeout is checked before cancellation: a message that mentions both
  // (e.g. "request cancelled: timed out") is a retryable timeout, not a
  // terminal cancellation.
  if (lower.includes("timed out") || lower.includes("timeout")) {
    return timeoutToolResult(call.id, call.name);
  }
  if (lower.includes("aborted") || lower.includes("cancel")) {
    return cancelledToolResult(call.id, call.name);
  }
  return errorToolResult(call.id, call.name, "TOOL_FAILED", message);
}

// ---------------------------------------------------------------------------
// ingestMcpToolResult — preserves MCP structure, does NOT throw on isError
// ---------------------------------------------------------------------------

export function ingestMcpToolResult(raw: McpRawResult): McpIngestedResult {
  const content = Array.isArray(raw.content) ? raw.content : [];
  if (raw.isError) {
    return {
      kind: "error",
      message: mcpErrorMessage(content),
      content,
      ...(raw.structuredContent !== undefined ? { structuredContent: raw.structuredContent } : {}),
    };
  }
  return {
    kind: "ok",
    content,
    ...(raw.structuredContent !== undefined ? { structuredContent: raw.structuredContent } : {}),
  };
}

function mcpErrorMessage(content: readonly McpContentPart[]): string {
  const messages = content
    .filter((item) => item && typeof item.text === "string")
    .map((item) => item.text!.trim())
    .filter(Boolean);
  return messages.join("\n") || "MCP tool call failed";
}

/**
 * Convert an ingested MCP result into a canonical AgentToolResult.
 */
export function fromIngestedMcp(
  callId: string,
  toolName: string,
  ingested: McpIngestedResult,
  extra?: Partial<Pick<AgentToolResultMeta, "durationMs" | "exitCode" | "nextCursor">>,
): AgentToolResult {
  if (ingested.kind === "error") {
    return errorToolResult(callId, toolName, "TOOL_FAILED", ingested.message, false, extra);
  }
  // Prefer structuredContent; fall back to sole text part.
  if (ingested.structuredContent !== undefined) {
    return successToolResult(callId, toolName, ingested.structuredContent, extra);
  }
  const textParts = ingested.content
    .filter((p) => p.type === "text" && typeof p.text === "string")
    .map((p) => p.text!);
  if (textParts.length === 1) {
    return successToolResult(callId, toolName, textParts[0], extra);
  }
  if (textParts.length > 1) {
    return successToolResult(callId, toolName, textParts.join("\n"), extra);
  }
  // Empty content — return empty success.
  return successToolResult(callId, toolName, "", extra);
}

// ---------------------------------------------------------------------------
// projectModelToolResult — canonical → model-facing text string
// ---------------------------------------------------------------------------

const PROJECTED_STRUCTURED_MAX = 50_000;
/** Shared with the durable conversation card cap in desktop. */
export const MODEL_TOOL_OUTPUT_MAX_CHARS = 12_000;

export function projectModelToolResult(result: AgentToolResult): string {
  // If already projected, reuse exact string (idempotent).
  if (result.modelOutput !== undefined) return result.modelOutput;

  const body = projectBody(result);
  const projected = projectWithinOutputCap(result.toolName, result.status, body, result.metadata.dataIsUntrusted);
  result.modelOutput = projected; // cache on first projection
  return projected;
}

function projectWithinOutputCap(toolName: string, status: AgentToolStatus, body: string, untrusted: boolean): string {
  const wrap = (value: string) => untrusted ? wrapUntrustedToolResult(toolName, status, value) : value;
  const full = wrap(body);
  if (full.length <= MODEL_TOOL_OUTPUT_MAX_CHARS) return full;

  // Clamp the raw body first, then rebuild the XML boundary. This keeps the
  // close tag intact and makes the capped string safe to persist verbatim.
  const overhead = wrap("").length;
  const bodyBudget = Math.max(0, MODEL_TOOL_OUTPUT_MAX_CHARS - overhead);
  const limited = truncateToolResultText(body, bodyBudget);
  const projected = wrap(limited);
  return projected.length <= MODEL_TOOL_OUTPUT_MAX_CHARS
    ? projected
    : wrap(truncateToolResultText(limited, Math.max(0, bodyBudget - (projected.length - MODEL_TOOL_OUTPUT_MAX_CHARS))));
}

function projectBody(result: AgentToolResult): string {
  if (result.status === "success") {
    const textPart = result.content.find((c) => c.type === "text");
    if (textPart && textPart.type === "text" && !result.structuredContent) {
      // Text/command path: the MCP text part is already the actual terminal
      // result. Preserve it verbatim; the untrusted envelope is the only
      // model-facing framing it needs.
      return textPart.text;
    }
    // Structured path: compact terminal-like key/value and table output.
    const data = result.structuredContent ?? result.content.find((c) => c.type === "json")?.data;
    return projectStructuredEnvelope(data, result.metadata, result.metadata.dataIsUntrusted);
  }
  // Status lives on untrusted envelopes. Their body stays the real error text.
  const err = result.error ?? { code: "TOOL_FAILED", message: "Unknown error", retryable: false };
  if (result.metadata.dataIsUntrusted) return err.message;
  return [
    `status=${result.status}`,
    `code=${terminalValue(err.code)}`,
    `retryable=${err.retryable}`,
    "",
    "stderr:",
    err.message,
  ].join("\n");
}

function projectStructuredEnvelope(data: unknown, meta: AgentToolResultMeta, untrusted: boolean): string {
  const lines: string[] = untrusted ? [] : ["status=success", `truncated=${meta.truncated}`];
  if (untrusted && meta.truncated) lines.push("truncated=true");
  if (meta.exitCode !== undefined && meta.exitCode !== null) lines.push(`exit_code=${meta.exitCode}`);
  if (meta.nextCursor) lines.push(`next_cursor=${terminalValue(meta.nextCursor)}`);
  const rendered = formatTerminalData(data);
  if (!rendered) return lines.join("\n");
  return lines.length > 0 ? [...lines, "", rendered].join("\n") : rendered;
}

function formatTerminalData(data: unknown): string {
  const lines: string[] = [];
  appendTerminalValue(lines, data, "data");
  return lines.join("\n");
}

function appendTerminalValue(lines: string[], value: unknown, path: string): void {
  if (value === null || typeof value !== "object") {
    lines.push(`${path}=${terminalValue(value)}`);
    return;
  }
  if (Array.isArray(value)) {
    appendTerminalArray(lines, value, path);
    return;
  }
  const entries = Object.entries(value as Record<string, unknown>);
  if (entries.length === 0) {
    lines.push(`${path}={}`);
    return;
  }
  for (const [key, entry] of entries) {
    appendTerminalValue(lines, entry, path === "data" ? terminalKey(key) : `${path}.${terminalKey(key)}`);
  }
}

function appendTerminalArray(lines: string[], values: readonly unknown[], path: string): void {
  if (values.length === 0) {
    lines.push(`${path}=[]`);
    return;
  }
  if (values.every((value) => value === null || typeof value !== "object")) {
    lines.push(`${path}[${values.length}]`);
    for (const value of values) lines.push(`- ${terminalValue(value)}`);
    return;
  }
  const table = terminalTable(values);
  if (table) {
    lines.push(`${path}[${values.length}]`, table.header, ...table.rows);
    return;
  }
  const serialized = safeJsonStringify(values, PROJECTED_STRUCTURED_MAX);
  lines.push(`${path}=${serialized}`);
}

function terminalTable(values: readonly unknown[]): { readonly header: string; readonly rows: readonly string[] } | undefined {
  if (!values.every((value) => value && typeof value === "object" && !Array.isArray(value))) return undefined;
  const records = values as readonly Record<string, unknown>[];
  const columns = Object.keys(records[0] ?? {});
  if (columns.length === 0 || !records.every((record) => Object.keys(record).length === columns.length && columns.every((key) => key in record))) return undefined;
  if (!records.every((record) => columns.every((key) => record[key] === null || typeof record[key] !== "object"))) return undefined;
  return {
    header: columns.map(terminalKey).join("\t"),
    rows: records.map((record) => columns.map((key) => terminalValue(record[key])).join("\t")),
  };
}

function terminalKey(key: string): string {
  return /^[A-Za-z_][A-Za-z0-9_.-]*$/.test(key) ? key : JSON.stringify(key);
}

function terminalValue(value: unknown): string {
  if (typeof value === "string") {
    return /^[A-Za-z0-9_./:@%+=,-]+$/.test(value) ? value : JSON.stringify(value);
  }
  if (value === undefined) return "undefined";
  if (typeof value === "number" || typeof value === "boolean" || value === null) return String(value);
  return safeJsonStringify(value, PROJECTED_STRUCTURED_MAX);
}

function safeJsonStringify(data: unknown, maxChars: number): string {
  // JSON.stringify(undefined) returns undefined (not a string) — normalize so
  // callers can always read .length without throwing.
  const full = JSON.stringify(data) ?? "null";
  if (full.length <= maxChars) return full;
  // Truncate mid-string with explicit marker.
  const half = Math.floor((maxChars - 60) / 2);
  return full.slice(0, half) + `…[truncated: ${full.length} chars]…` + full.slice(-half);
}

// ---------------------------------------------------------------------------
// truncateToolResultText — head+tail with explicit omit marker
// ---------------------------------------------------------------------------

export function truncateToolResultText(text: string, maxChars: number): string {
  if (text.length <= maxChars) return text;
  const omitted = text.length - maxChars;
  const marker = `[omitted: ${omitted} chars]`;
  // Two newlines around marker = 2 chars overhead.
  const budget = maxChars - marker.length - 2;
  if (budget <= 0) {
    // Only marker fits (or less).
    return marker.slice(0, maxChars);
  }
  const head = Math.ceil(budget * 0.6);
  const tail = budget - head;
  return `${text.slice(0, head)}\n${marker}\n${text.slice(-tail)}`;
}
