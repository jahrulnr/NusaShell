import type {
  AgentMessage,
  AgentProvider,
  AgentProviderRequest,
  AgentProviderResult,
} from "@nusashell/application";

export interface OpenAiCompatibleAgentProviderOptions {
  readonly id?: string;
  readonly baseUrl: string;
  readonly apiKey: string;
  readonly model: string;
  readonly fetchFn?: typeof fetch;
}

/**
 * One wire adapter for OpenAI and OpenAI-compatible gateways. It deliberately
 * implements only the non-streaming Chat Completions function-call contract.
 */
export class OpenAiCompatibleAgentProvider implements AgentProvider {
  readonly id: string;
  private readonly endpoint: string;
  private readonly fetchFn: typeof fetch;

  constructor(private readonly options: OpenAiCompatibleAgentProviderOptions) {
    this.id = options.id ?? "openai-compatible";
    this.endpoint = `${options.baseUrl.replace(/\/+$/, "")}/chat/completions`;
    this.fetchFn = options.fetchFn ?? fetch;
  }

  async complete(request: AgentProviderRequest): Promise<AgentProviderResult> {
    const response = await this.fetchFn(this.endpoint, {
      method: "POST",
      headers: {
        "content-type": "application/json",
        authorization: `Bearer ${this.options.apiKey}`,
      },
      body: JSON.stringify({
        model: this.options.model,
        messages: request.messages.map(toOpenAiMessage),
        tools: request.tools.map((tool) => ({
          type: "function",
          function: {
            name: tool.name,
            ...(tool.description ? { description: tool.description } : {}),
            parameters: tool.inputSchema ?? { type: "object", properties: {} },
          },
        })),
        tool_choice: request.tools.length > 0 ? "auto" : "none",
        stream: false,
      }),
    });

    const payload = await readJson(response);
    if (!response.ok) {
      throw new Error(`Provider returned HTTP ${response.status}`);
    }
    return parseOpenAiResult(payload, this.options.model);
  }
}

function toOpenAiMessage(message: AgentMessage): Record<string, unknown> {
  if (message.role === "tool") {
    return { role: "tool", tool_call_id: message.toolCallId, content: message.content };
  }
  if (message.role === "assistant") {
    return {
      role: "assistant",
      ...(message.content ? { content: message.content } : {}),
      ...(message.toolCalls ? {
        tool_calls: message.toolCalls.map((call) => ({
          id: call.id,
          type: "function",
          function: { name: call.name, arguments: JSON.stringify(call.args) },
        })),
      } : {}),
    };
  }
  return { role: message.role, content: message.content };
}

async function readJson(response: Response): Promise<unknown> {
  try {
    return await response.json();
  } catch {
    return undefined;
  }
}

function parseOpenAiResult(payload: unknown, fallbackModel: string): AgentProviderResult {
  if (!isRecord(payload) || !Array.isArray(payload.choices) || !isRecord(payload.choices[0])) {
    throw new Error("Provider response does not contain a completion choice");
  }
  const choice = payload.choices[0];
  if (!isRecord(choice.message)) {
    throw new Error("Provider response does not contain a completion message");
  }
  const message = choice.message;
  const toolCalls = Array.isArray(message.tool_calls) ? message.tool_calls.map(parseToolCall) : [];
  return {
    ...(typeof message.content === "string" ? { text: message.content } : {}),
    ...(toolCalls.length > 0 ? { toolCalls } : {}),
    model: typeof payload.model === "string" ? payload.model : fallbackModel,
  };
}

function parseToolCall(value: unknown) {
  if (!isRecord(value) || typeof value.id !== "string" || !isRecord(value.function) ||
    typeof value.function.name !== "string" || typeof value.function.arguments !== "string") {
    throw new Error("Provider returned an invalid tool call");
  }
  let args: unknown;
  try {
    args = JSON.parse(value.function.arguments);
  } catch {
    throw new Error("Provider returned invalid JSON tool arguments");
  }
  if (!isRecord(args)) throw new Error("Provider tool arguments must be an object");
  return { id: value.id, name: value.function.name, args };
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}
