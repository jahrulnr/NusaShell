import { describe, expect, it } from "vitest";
import { setMaxListeners } from "node:events";
import { parseOpenAiSse } from "../src/ai/openai-sse-parser.js";

describe("parseOpenAiSse", () => {
  it("removes its abort listener from idleSignal after the stream completes", async () => {
    const idleController = new AbortController();
    setMaxListeners(2, idleController.signal);
    const warnings: string[] = [];
    const onWarning = (warning: Error) => {
      if (warning.name === "MaxListenersExceededWarning") warnings.push(warning.message);
    };
    process.on("warning", onWarning);
    try {
      for (let i = 0; i < 5; i += 1) {
        // eslint-disable-next-line no-await-in-loop
        await parseOpenAiSse(
          makeChatResponse([
            'data: {"id":"c1","model":"m","choices":[{"delta":{"content":"hi"}}]}',
            'data: {"id":"c1","model":"m","choices":[{"delta":{"content":""},"finish_reason":"stop"}]}',
            "data: [DONE]",
          ]),
          "chat",
          undefined,
          undefined,
          1024 * 1024,
          undefined,
          idleController.signal,
        );
      }
      // Flush pending process.emitWarning (Node emits on next tick).
      await new Promise((resolve) => setImmediate(resolve));
    } finally {
      process.off("warning", onWarning);
    }
    expect(warnings).toEqual([]);
  });

  it("aborts promptly when idleSignal fires mid-stream", async () => {
    const idleController = new AbortController();
    const stream = new ReadableStream<Uint8Array>({
      start(controller) {
        controller.enqueue(new TextEncoder().encode(
          'data: {"id":"c1","model":"m","choices":[{"delta":{"content":"hi"}}]}\n\n',
        ));
        // Don't close — stall so the idle signal fires.
      },
    });
    const response = new Response(stream, { headers: { "content-type": "text/event-stream" } });
    const promise = parseOpenAiSse(
      response,
      "chat",
      undefined,
      undefined,
      1024 * 1024,
      undefined,
      idleController.signal,
    );
    idleController.abort(new Error("Provider request timed out"));
    await expect(promise).rejects.toThrow(/timed out|SSE read failed/);
  });
});

function makeChatResponse(lines: readonly string[]): Response {
  const encoder = new TextEncoder();
  const body = lines.map((line) => `${line}\n\n`).join("");
  const stream = new ReadableStream<Uint8Array>({
    start(controller) {
      controller.enqueue(encoder.encode(body));
      controller.close();
    },
  });
  return new Response(stream, { headers: { "content-type": "text/event-stream" } });
}
