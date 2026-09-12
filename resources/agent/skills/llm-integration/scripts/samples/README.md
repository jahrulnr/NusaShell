# Runnable integration samples

The `openai-responses/` sample is a small Go web chat that makes real OpenAI
Responses API requests without an SDK dependency. It keeps the API key in the
backend, serves a browser UI, and demonstrates a bounded function-tool loop.
The endpoint is `POST https://api.openai.com/v1/responses`; see the skill's
[Responses contract](../../references/openai/responses.md) for the complete
request, response, streaming, tool, and error surface.

## Requirements

- An OpenAI API key exported as `OPENAI_API_KEY`.
- Go 1.22 or newer for the standard-library sample.

Never put the key in source code, a command-line argument, a committed `.env`
file, or diagnostic output. Set `OPENAI_MODEL` to a model available to the
account; the sample defaults to `gpt-5.5`. `OPENAI_BASE_URL` is optional and
defaults to `https://api.openai.com`.

```bash
export OPENAI_API_KEY='your-key-from-a-secret-store'
export OPENAI_MODEL='gpt-5.5'

go test ./resources/agent/skills/llm-integration/scripts/samples/openai-responses
go run ./resources/agent/skills/llm-integration/scripts/samples/openai-responses
# Open http://localhost:8080, then ask: What time is it?
```

For a one-shot terminal request, pass the prompt as an argument instead:

```bash
go run ./resources/agent/skills/llm-integration/scripts/samples/openai-responses \
  'Explain idempotency in one sentence.'
```

The web chat demonstrates:

- a same-origin browser client, with the API key held only by the Go server;
- an in-memory conversation per browser session, using `store: false` and
  replaying the required response items on the next turn;
- a stable `prompt_cache_key` per session. It is a cache-routing hint, not an
  authentication credential, and contains neither prompt text nor secrets;
- the allowlisted `getCurrentTime` function tool, including strict argument
  validation, bounded tool rounds, and `function_call_output` ordering;
- output parsing across all `output[]`/`content[]` items, usage aggregation,
  bounded timeouts, and bounded upstream error bodies; and
- untrusted-tool-output handling: tool results are data, never instructions,
  and are not allowed to authorize a new action or sink.

The sample intentionally keeps sessions in process memory; restarting it loses
the chat. Run it locally only. Before exposing it to other users, add
authentication, rate limits, persistence policy, and the product's approval
controls. Read [agent-hooks — behavior and implementation](../../references/agent-hooks/README.md)
for the broader tool-loop, untrusted-output, and sink-gating rules.

## Provider sources

- [OpenAI API quickstart](https://platform.openai.com/docs/quickstart/make-your-first-api-request)
  — API-key environment setup and a minimal Responses request.
- [OpenAI Responses API reference](https://developers.openai.com/api/reference/typescript/resources/beta/subresources/responses/methods/create)
  — request parameters, response items, and streaming event shapes.
- [OpenAI model guidance](https://developers.openai.com/api/docs/guides/latest-model)
  — current tool, reasoning, and response integration guidance.
