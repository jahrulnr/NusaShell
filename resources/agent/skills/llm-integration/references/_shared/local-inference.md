# Local / self-hosted inference

Run open-weight models on your own hardware through an OpenAI-compatible HTTP
server. Use this when privacy, cost, latency, or offline development matter
more than frontier model quality. Verified 2026-09-11.

## When local beats hosted

- **Privacy / data residency:** prompts never leave the machine. Required for
  PII, source code, or air-gapped environments.
- **Cost at volume:** no per-token billing; the only cost is hardware and
  electricity. Break-even is high throughput or long-running agents.
- **Offline / dev loop:** iterate on prompt wiring, streaming, and tool parsing
  without an API key or network.

## What stays the same

Most runtimes expose an **OpenAI-compatible** `/v1/chat/completions` (and often
`/v1/completions`, `/v1/embeddings`). The request/response shape, SSE streaming
framing, `tool_calls`, `usage`, and `finish_reason` from
`../openai/chat-completions.md` apply directly. Point your existing OpenAI
client at a local `base_url` and it usually works.

## What changes

- **No auth (usually):** most servers accept any non-empty key; some ignore the
  header entirely. Never assume a real credential is required.
- **Model lifecycle is yours:** models load into VRAM on first request (slow
  first token), idle out, and must be pulled/quantized. Hosted APIs hide this.
- **Hardware limits set the ceiling:** context window, batch size, and which
  models fit are bounded by VRAM, not a pricing tier.
- **Tool-calling reliability varies by model family**, not by runtime. A
  server can expose the OpenAI tools schema but the underlying model may emit
  malformed tool-call JSON.

## Runtime delta table

| Runtime | Default base URL | Auth | Model naming | Tool calling | Notes |
|---|---|---|---|---|---|
| **Ollama** | `http://127.0.0.1:11434` (native) or `…/v1` (compat) | None local; `OLLAMA_API_KEY` only for cloud/remote | `name:tag` (e.g. `gemma4:latest`); `/api/tags` lists installed | Native `/api/chat` preferred; the `/v1` compat shim **breaks tool calling** and can emit raw tool JSON as text | `keep_alive` controls VRAM residency; `options.num_ctx` overrides context (default 2048) |
| **llama.cpp** (`llama-server`) | `http://127.0.0.1:8080/v1` | None by default | Whatever you pass as `--alias` or the HF repo id | Requires `--jinja` + a tools-enabled chat template; OpenAI-shaped `/v1/chat/completions` | `-c`/`--ctx-size` sets context; `/props` inspects the active template |
| **vLLM** | `http://127.0.0.1:8000/v1` | None by default; `--api-key` optional | HF repo id (e.g. `meta-llama/Llama-3.1-8B-Instruct`) | `--enable-auto-tool-choice` + `--tool-call-parser <parser>` required; `parallel_tool_calls` defaults true | `--max-model-len` caps context; `--chat-template` for tool templates |
| **LM Studio** | `http://127.0.0.1:1234/v1` | Optional `LM_API_TOKEN` | `author/model` key (e.g. `qwen/qwen3.5-9b`); `/api/v1/models` | OpenAI-shaped; quality depends on the loaded GGUF | JIT model loading + idle TTL/auto-evict own the lifecycle; keep `/v1` in the base URL |
| **SGLang** | `http://127.0.0.1:30000/v1` | `api_key: "EMPTY"` or `"None"` literal | HF repo id | OpenAI-shaped tools; parser support is model-dependent | `sglang serve`; `/health` + `/get_model_info`; `--tp-size`, `--mem-fraction-static` |
| **Ollama Cloud** (hosted twist) | `https://ollama.com` | `OLLAMA_API_KEY` **required** | Cloud catalog ids (`kimi-k2.6`, `glm-5.2`, `minimax-m2.7`), not local pull names | Native `/api/chat` (same shape as local Ollama) | No local daemon; `/api/embed` may not be authorized by a cloud key |

## Per-runtime quick reference

### Ollama
- Pull/list: `ollama pull <model>`; `GET /api/tags` lists installed; `GET /api/show`
  reports capabilities (vision/tools/thinking) and `contextWindow`.
- Native chat: `POST /api/chat` with `{model, messages, tools, stream, options,
  keep_alive}`. Prefer this for tools — the `/v1/chat/completions` shim is fine
  for plain chat but degrades tool calls.
- `keep_alive`: duration string (`"5m"`) or seconds; `0` unloads immediately.
  Set long enough to avoid reload churn between turns.
- `options.num_ctx`: override context window (default 2048). The OpenAI shim
  has no way to set it — bake it into a `Modelfile` (`PARAMETER num_ctx`) and
  `ollama create` a named model, or use the native API.
- Embeddings: `POST /api/embed` (`{model, input}`) — native shape, not OpenAI
  `/v1/embeddings` (both exist; `/api/embed` is canonical).

### llama.cpp (`llama-server`)
- Launch: `llama-server -m model.gguf -c 8192 --jinja --port 8080` (or `-hf
  <repo>:<quant>` to pull from Hugging Face). `-c`/`--ctx-size` is the context
  budget; undersizing truncates silently.
- `--jinja` is **required** for tool calling and for models whose chat template
  isn't a llama.cpp built-in. Without it, only commonly-used templates work and
  tools may not render. Inspect the active template at `GET /props`.
- Override templates with `--chat-template` (built-in name) or
  `--chat-template-file` (`.jinja` file) when the model's bundled template is
  buggy (e.g. some DeepSeek R1 distills).
- OpenAI endpoint: `POST /v1/chat/completions`; `model` is echo-only.

### vLLM
- Launch: `vllm serve <model> --max-model-len 32768 --enable-auto-tool-choice
  --tool-call-parser hermes` (parser must match the model family: `hermes`,
  `mistral`, `llama3_json`, `qwen3`, `openai`, …). Without
  `--enable-auto-tool-choice`, tools are silently ignored.
- `--max-model-len` is the hard context cap; exceeding it returns an error, not
  silent truncation. Set below the model's trained max to fit VRAM.
- `--chat-template` points at a tool-enabled Jinja template when the tokenizer's
  default lacks tool support.
- OpenAI server: `/v1/chat/completions`, `/v1/completions`, `/v1/embeddings`,
  `/v1/responses`. Pass vLLM-only params (e.g. `top_k`) via `extra_body`.

### LM Studio
- Headless: `lms server start --port 1234` or `lms daemon up`. Desktop app also
  exposes the same server. Discovery: `GET /api/v1/models` (note the `/api/v1`
  path, not `/v1`).
- JIT loading brings a model into VRAM on first request; idle TTL + auto-evict
  free it later. First request after eviction is slow (model reload).
- Auth is optional — only set `LM_API_TOKEN` if you enabled authentication in
  LM Studio. The OpenAI client needs a non-empty key; use any placeholder.
- Reasoning: LM Studio may advertise `reasoning_effort` levels; some builds
  accept `off`/`on` in discovery but reject them on `/v1/chat/completions`.
  Normalize to the documented scale before sending.

### SGLang
- Launch: `sglang serve --model-path <hf-repo> --port 30000 --tp-size N
  --mem-fraction-static 0.8`. OpenAI-compatible: `/v1/chat/completions`,
  `/v1/embeddings`, `/health`, `/get_model_info`.
- Auth is a literal placeholder (`"EMPTY"` / `"None"`) unless you enable it.
- Tool calling is OpenAI-shaped but parser support is model-dependent; verify
  with a minimal tool request before relying on it.

## Cross-cutting gotchas

- **Context window overrides:** Ollama defaults to 2048 (`num_ctx`) — far below
  most models' trained max. llama.cpp `-c` and vLLM `--max-model-len` are
  explicit. Always set the context budget; silent truncation breaks tool loops.
- **Tool-calling reliability is per-model, not per-runtime.** A runtime can
  expose the OpenAI tools schema while the model emits unparseable JSON. Test
  the specific model+quant. Hermes, Mistral, Llama 3.1+, Qwen tool-trained
  variants are the most reliable; small quants of tool models often regress.
- **Streaming parity:** OpenAI-compat servers generally emit standard SSE
  `data: {…}` chunks and a `data: [DONE]` terminator. Ollama's native
  `/api/chat` stream is NDJSON, not SSE — different parser. vLLM/SGLang/LM
  Studio/llama.cpp follow OpenAI SSE closely.
- **Embeddings:** Ollama `/api/embed` (native) vs `/v1/embeddings` (compat);
  vLLM and SGLang expose `/v1/embeddings`; llama.cpp exposes `/v1/embeddings`
  when built with embedding support. Ollama Cloud keys may not authorize
  embeddings — verify before assuming.
- **Quantization / VRAM:** Q4_K_M / Q5 are the practical sweet spot for
  quality/size. VRAM rule of thumb: model size in GB ≈ params × bytes/param at
  the chosen quant, plus ~1-2 GB overhead per context-K. A 7B at Q4 ≈ 4-5 GB;
  leave headroom for `num_ctx`/`-c`/`--max-model-len` KV cache.
- **CORS for local web apps:** most servers bind loopback and do not send
  `Access-Control-Allow-Origin`. For a browser front end, proxy through your
  own backend, or launch the server with `--host 0.0.0.0` and a CORS flag where
  supported (vLLM `--allowed-origins`, llama.cpp `--cors`). Never expose an
  unauthenticated server beyond a trusted network.
- **Health checks:** Ollama `GET /api/tags`; vLLM `GET /health`; SGLang
  `GET /health`; llama.cpp `GET /health`; LM Studio `GET /api/v1/models`. Probe
  before assuming a model is loaded — a healthy server with no loaded model
  still incurs a slow first-token.
- **Timeouts (model load is slow):** for interactive streams, use a short
  **connect timeout** plus an **idle timeout that resets on every chunk**. The
  first request after a model load/evict can take tens of seconds to first
  token; chunks then flow normally. Add a configurable product-level deadline
  when needed. Retry only transient failures (5xx, network, idle-timeout);
  never retry 400/422 (usually a bad tool schema or unsupported param).

## Related

- [../openai/README.md](../openai/README.md) — OpenAI API contracts (the shape
  most local servers imitate; Chat Completions, streaming, tools).
- [../_shared/sample-implementations.md](../_shared/sample-implementations.md) —
  optional LiteLLM adapter cross-checks: `litellm/llms/ollama/`, `litellm/llms/vllm/`,
  `litellm/llms/lm_studio/`, `litellm/llms/llamafile/` for request/response
  transformation patterns to port.
