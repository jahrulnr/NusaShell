import type { AgentToolCall } from "@nusashell/application";

interface LocatedCall {
  readonly start: number;
  readonly end: number;
  readonly call: AgentToolCall;
}

export interface TextToolCallParseResult {
  readonly calls: readonly AgentToolCall[];
  readonly text: string;
}

/**
 * Recover tool calls that models embed in assistant text, and strip protocol
 * leakage so it never reaches the user UI.
 *
 * Supported call dialects:
 * - fenced `<function=name>…`
 * - Anthropic invoke / tool_use XML
 * - Kimi pipe markers
 * - DeepSeek V3.2/V4 DSML (`<｜DSML｜tool_calls>` / invoke / parameter)
 *
 * Also strips echoed tool results (`<tool_result>…`) and orphan DSML tokens —
 * DeepSeek-v4-flash sometimes regurgitates prior MCP envelopes + half-closed
 * DSML tags into the free-text stream.
 */
export function extractTextToolCalls(rawText: string): TextToolCallParseResult {
  const text = stripKnownModelControlTokens(normalizeSpecialTokenText(rawText));
  const located = [
    ...extractDsmlCalls(text),
    ...extractFunctionCalls(text),
    ...extractAnthropicInvokes(text),
    ...extractToolUseCalls(text),
    ...extractKimiCalls(text),
  ].sort((left, right) => left.start - right.start);

  const calls: AgentToolCall[] = [];
  let cleaned = "";
  let cursor = 0;
  for (const item of located) {
    if (item.start >= cursor) {
      cleaned += text.slice(cursor, item.start);
      cursor = item.end;
    }
    if (item.call.name) calls.push(item.call);
  }
  cleaned += text.slice(cursor);
  return { calls, text: stripLeakedToolProtocol(cleaned).trim() };
}

/**
 * Remove non-semantic generation sentinels that occasionally escape an
 * OpenAI-compatible provider's tokenizer. This intentionally has a narrow
 * allowlist: prose is never classified as reasoning from its wording.
 */
export function stripKnownModelControlTokens(value: string): string {
  return value
    .replace(/[｜│]/g, "|")
    .replace(/[▁‗]/g, "_")
    .replace(/<\|\s*(?:begin|end)_(?:of_)?(?:sentence|text|turn|message)\s*\|>/gi, "");
}

/**
 * Project a provider reasoning payload to displayable text. Opaque encrypted
 * and redacted blocks deliberately remain out of the UI and transcript.
 */
export function extractReasoningText(value: unknown): string {
  if (typeof value === "string") return stripKnownModelControlTokens(value);
  if (Array.isArray(value)) return value.map(extractReasoningText).join("");
  if (!isRecord(value)) return "";
  const type = typeof value.type === "string" ? value.type.toLowerCase() : "";
  if (type.includes("encrypted") || type.includes("redacted")) return "";
  return extractReasoningText(value.text)
    || extractReasoningText(value.content)
    || extractReasoningText(value.thinking)
    || extractReasoningText(value.reasoning)
    || extractReasoningText(value.summary);
}

export function mergeTextToolCalls(
  nativeCalls: readonly AgentToolCall[],
  text: string,
): TextToolCallParseResult {
  const parsed = extractTextToolCalls(text);
  const consumed = new Set<number>();
  const merged = nativeCalls.map((native) => {
    const index = parsed.calls.findIndex((candidate, candidateIndex) =>
      !consumed.has(candidateIndex) && candidate.name === native.name);
    if (index < 0) return native;
    consumed.add(index);
    return Object.keys(native.args).length === 0
      ? { ...native, args: parsed.calls[index]?.args ?? {} }
      : native;
  });
  parsed.calls.forEach((call, index) => {
    if (!consumed.has(index)) merged.push(call);
  });
  return { calls: merged, text: parsed.text };
}

/**
 * DeepSeek V3.2/V4 DSML tool markup.
 * Special tokens use fullwidth pipes (｜); normalizeSpecialTokenText folds them
 * to `|` first so one regex covers both tokenized and ASCII-leak shapes.
 *
 * ```
 * <|DSML|tool_calls>
 * <|DSML|invoke name="fn">
 * <|DSML|parameter name="x" string="true">val</|DSML|parameter>
 * </|DSML|invoke>
 * </|DSML|tool_calls>
 * ```
 * V3.2 may use `function_calls` as the wrapper name instead of `tool_calls`.
 */
function extractDsmlCalls(text: string): LocatedCall[] {
  const out: LocatedCall[] = [];
  const sectionPattern =
    /<\|\s*DSML\s*\|\s*(?:tool_calls|function_calls)\s*>([\s\S]*?)<\/\|\s*DSML\s*\|\s*(?:tool_calls|function_calls)\s*>/gi;
  for (const section of text.matchAll(sectionPattern)) {
    const sectionBody = section[1] ?? "";
    const sectionStart = section.index ?? 0;
    const sectionEnd = sectionStart + (section[0]?.length ?? 0);
    const invokePattern =
      /<\|\s*DSML\s*\|\s*invoke\s+name\s*=\s*["']([^"']+)["']\s*>([\s\S]*?)<\/\|\s*DSML\s*\|\s*invoke\s*>/gi;
    let anyInside = false;
    for (const invoke of sectionBody.matchAll(invokePattern)) {
      anyInside = true;
      const name = (invoke[1] ?? "").trim();
      const args = parseDsmlParameters(invoke[2] ?? "");
      out.push({
        start: sectionStart,
        end: sectionEnd,
        call: createCall(name, args, out.length),
      });
    }
    if (!anyInside) {
      // Empty or garbled wrapper — mark span so markup still leaves cleaned text.
      out.push({
        start: sectionStart,
        end: sectionEnd,
        call: createCall("", {}, out.length),
      });
    }
  }

  // Standalone invoke outside a wrapper (partial streams / proxy detokenizers).
  const loneInvoke =
    /<\|\s*DSML\s*\|\s*invoke\s+name\s*=\s*["']([^"']+)["']\s*>([\s\S]*?)<\/\|\s*DSML\s*\|\s*invoke\s*>/gi;
  for (const invoke of text.matchAll(loneInvoke)) {
    const start = invoke.index ?? 0;
    const end = start + (invoke[0]?.length ?? 0);
    if (out.some((item) => start >= item.start && end <= item.end)) continue;
    out.push({
      start,
      end,
      call: createCall((invoke[1] ?? "").trim(), parseDsmlParameters(invoke[2] ?? ""), out.length),
    });
  }
  return out;
}

function parseDsmlParameters(body: string): Record<string, unknown> {
  const args: Record<string, unknown> = {};
  const parameterPattern =
    /<\|\s*DSML\s*\|\s*parameter\s+name\s*=\s*["']([^"']+)["'](?:\s+string\s*=\s*["'](true|false)["'])?\s*>([\s\S]*?)<\/\|\s*DSML\s*\|\s*parameter\s*>/gi;
  for (const match of body.matchAll(parameterPattern)) {
    const name = match[1] ?? "";
    const stringFlag = (match[2] ?? "true").toLowerCase() === "true";
    const raw = match[3] ?? "";
    if (!name) continue;
    if (stringFlag) {
      args[name] = raw;
      continue;
    }
    try {
      args[name] = JSON.parse(raw.trim()) as unknown;
    } catch {
      args[name] = coerceValue(raw);
    }
  }
  return args;
}

/**
 * Remove tool protocol that is not a recoverable call: echoed tool results,
 * orphan DSML open/close tokens, and short pre-tag garbled detokenization.
 */
export function stripLeakedToolProtocol(text: string): string {
  let next = text;
  // Paired tool_result blocks (models echoing MCP envelopes as free text).
  next = next.replace(/[^\s\n]{0,8}<tool_result\b[^>]*>[\s\S]*?<\/tool_result>/gi, "");
  next = next.replace(/<tool_result\b[^>]*>[\s\S]*?<\/tool_result>/gi, "");
  // Orphan opens/closes + short junk immediately before a stray closer.
  next = next.replace(/[^\s\n]{0,8}<\/tool_result>/gi, "");
  next = next.replace(/<\/?tool_result\b[^>]*>/gi, "");
  // Orphan DSML tokens (partial or unmatched after extractDsmlCalls).
  next = next.replace(/<\/?\|\s*DSML\s*\|[^>\n]{0,80}>/gi, "");
  // Half-open DSML tool_calls that never closed.
  next = next.replace(/<\|\s*DSML\s*\|\s*(?:tool_calls|function_calls)\s*>[\s\S]*$/gi, "");
  // Control residue from broken special-token detokenizers (e.g. "Bdy_S").
  next = next.replace(/(^|\n)\s*[A-Za-z]{1,4}_[A-Za-z0-9]{1,4}\s*(?=\n|$)/g, "$1");
  return next.replace(/[ \t]+\n/g, "\n").replace(/\n{3,}/g, "\n\n").trim();
}

function extractFunctionCalls(text: string): LocatedCall[] {
  const out: LocatedCall[] = [];
  const pattern = /<function\s*=\s*([^>\s]+)>([\s\S]*?)<\/function>/gi;
  for (const match of text.matchAll(pattern)) {
    out.push(located(match, match[1] ?? "", parseEqualsParameters(match[2] ?? ""), out.length));
  }
  return out;
}

function extractAnthropicInvokes(text: string): LocatedCall[] {
  const out: LocatedCall[] = [];
  const pattern = /<invoke\s+name\s*=\s*["']([^"']+)["']>([\s\S]*?)<\/invoke>/gi;
  for (const match of text.matchAll(pattern)) {
    const item = located(match, match[1] ?? "", parseNamedParameters(match[2] ?? ""), out.length);
    const wrapperStart = text.lastIndexOf("<function_calls", item.start);
    const wrapperEnd = text.indexOf("</function_calls>", item.end);
    out.push(wrapperStart >= 0 && wrapperEnd >= item.end
      ? { ...item, start: wrapperStart, end: wrapperEnd + "</function_calls>".length }
      : item);
  }
  return out;
}

function extractToolUseCalls(text: string): LocatedCall[] {
  const out: LocatedCall[] = [];
  const pattern = /<tool_use>([\s\S]*?)<\/tool_use>/gi;
  for (const match of text.matchAll(pattern)) {
    const body = match[1] ?? "";
    const name = /<name>([\s\S]*?)<\/name>/i.exec(body)?.[1]?.trim() ?? "";
    const parameters = /<parameters>([\s\S]*?)<\/parameters>/i.exec(body)?.[1] ?? "";
    const args: Record<string, unknown> = {};
    for (const parameter of parameters.matchAll(/<([a-zA-Z_][\w.-]*)>([\s\S]*?)<\/\1>/g)) {
      args[parameter[1] ?? ""] = coerceValue(parameter[2] ?? "");
    }
    out.push(located(match, name, args, out.length));
  }
  return out;
}

function extractKimiCalls(text: string): LocatedCall[] {
  const out: LocatedCall[] = [];
  const sectionPattern = /<\|(?:redacted_)?tool_calls(?:_section)?_begin(?:_kimi)?\|>([\s\S]*?)<\|(?:redacted_)?tool_calls(?:_section)?_end(?:_kimi)?\|>/gi;
  for (const section of text.matchAll(sectionPattern)) {
    const sectionBody = section[1] ?? "";
    const sectionStart = section.index ?? 0;
    const callPattern = /<\|(?:redacted_)?tool_call_begin(?:_kimi)?\|>([\s\S]*?)<\|(?:redacted_)?tool_call_end(?:_kimi)?\|>/gi;
    for (const callMatch of sectionBody.matchAll(callPattern)) {
      const body = (callMatch[1] ?? "").trim();
      const split = body.split(/<\|(?:tool_call_argument_begin|tool_sep)\|>/i);
      const header = (split.shift() ?? "").trim();
      const name = header.replace(/^functions\./i, "").replace(/:\d+\s*$/, "").trim().split(/\s+/)[0] ?? "";
      const args = parseKimiArguments(split.join("<|tool_sep|>").trim());
      out.push({
        start: sectionStart,
        end: sectionStart + (section[0]?.length ?? 0),
        call: createCall(name, args, out.length),
      });
    }
  }
  return out;
}

function parseEqualsParameters(body: string): Record<string, unknown> {
  const args: Record<string, unknown> = {};
  for (const match of body.matchAll(/<parameter\s*=\s*([^>\s]+)>([\s\S]*?)<\/parameter>/gi)) {
    args[match[1] ?? ""] = coerceValue(match[2] ?? "");
  }
  return args;
}

function parseNamedParameters(body: string): Record<string, unknown> {
  const args: Record<string, unknown> = {};
  for (const match of body.matchAll(/<parameter\s+name\s*=\s*["']([^"']+)["']>([\s\S]*?)<\/parameter>/gi)) {
    args[match[1] ?? ""] = coerceValue(match[2] ?? "");
  }
  return args;
}

function parseKimiArguments(value: string): Record<string, unknown> {
  if (!value) return {};
  try {
    const parsed: unknown = JSON.parse(value);
    if (isRecord(parsed)) return parsed;
  } catch {
    // Continue with the delimiter and bare-command fallbacks.
  }
  const args: Record<string, unknown> = {};
  for (const segment of value.split(/<\|tool_sep\|>/i)) {
    const [key, ...rest] = segment.trim().split(/\r?\n/);
    if (key && rest.length > 0) args[key.trim()] = coerceValue(rest.join("\n"));
  }
  return Object.keys(args).length > 0 ? args : { input: value.trim() };
}

/**
 * Fold DeepSeek/Kimi special-token presentation into a stable ASCII-ish form
 * used by the extractors: fullwidth pipes → `|`, unusual underscores → `_`.
 */
function normalizeSpecialTokenText(value: string): string {
  return value
    .replace(/[｜│]/g, "|")
    .replace(/[▁‗]/g, "_")
    .replace(/<\s*\|\s*([^>]*?)\s*\|\s*>/g, (_match, token: string) =>
      `<|${token.replace(/\s+/g, "_").replace(/^_+|_+$/g, "")}|>`);
}

function coerceValue(value: string): unknown {
  const normalized = value.trim();
  if (/^(true|yes)$/i.test(normalized)) return true;
  if (/^(false|no)$/i.test(normalized)) return false;
  if (/^-?\d+$/.test(normalized)) return Number.parseInt(normalized, 10);
  if (/^-?(?:\d+\.\d*|\d*\.\d+)$/.test(normalized)) return Number.parseFloat(normalized);
  return normalized;
}

function located(
  match: RegExpMatchArray,
  name: string,
  args: Record<string, unknown>,
  index: number,
): LocatedCall {
  const start = match.index ?? 0;
  return {
    start,
    end: start + (match[0]?.length ?? 0),
    call: createCall(name.trim(), args, index),
  };
}

function createCall(name: string, args: Record<string, unknown>, index: number): AgentToolCall {
  return { id: `call_text_${index + 1}`, name, args };
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}
