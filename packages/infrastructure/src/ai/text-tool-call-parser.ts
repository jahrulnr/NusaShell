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

export function extractTextToolCalls(rawText: string): TextToolCallParseResult {
  const text = normalizeKimiText(rawText);
  const located = [
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
    calls.push(item.call);
  }
  cleaned += text.slice(cursor);
  return { calls, text: cleaned.trim() };
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

function normalizeKimiText(value: string): string {
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
