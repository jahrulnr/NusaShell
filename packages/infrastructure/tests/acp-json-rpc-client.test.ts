import { describe, expect, it, beforeEach } from "vitest";
import { EventEmitter } from "node:events";
import { AcpJsonRpcClient } from "../src/acp/acp-json-rpc-client.js";
import { CursorAcpExtension, CodexAcpExtension, resolveAcpExtension } from "../src/acp/extensions/index.js";
import type { AcpProviderExtension, AcpExtensionHandled, AcpExtensionContext } from "../src/acp/extensions/index.js";
import type { AcpClientSink, AcpProviderDescriptor } from "@nusashell/application";

interface CapturedRequest {
  id: number;
  method: string;
  params: unknown;
}

class FakeStdin {
  readonly lines: string[] = [];
  private onWrite?: (line: string) => void;
  setOnWrite(cb: (line: string) => void): void { this.onWrite = cb; }
  write(line: string): boolean {
    this.lines.push(line);
    this.onWrite?.(line);
    return true;
  }
}

class FakeChildProcess extends EventEmitter {
  readonly stdin = new FakeStdin();
  readonly stdout = new EventEmitter();
  readonly stderr = new EventEmitter();
  killed = false;
  readonly requests: CapturedRequest[] = [];

  constructor() {
    super();
    this.stdin.setOnWrite((line) => {
      try {
        const parsed = JSON.parse(line) as { id?: number; method: string; params?: unknown };
        if (parsed.id !== undefined) {
          this.requests.push({ id: parsed.id, method: parsed.method, params: parsed.params });
        }
      } catch {
        // ignore non-JSON lines
      }
    });
  }

  kill(): boolean {
    this.killed = true;
    return true;
  }

  /** Send a JSON-RPC response (result) for the given request id to stdout. */
  respond(id: number, result: unknown): void {
    this.stdout.emit("data", Buffer.from(JSON.stringify({ jsonrpc: "2.0", id, result }) + "\n", "utf8"));
  }

  /** Send a JSON-RPC error response for the given request id to stdout. */
  respondError(id: number, code: number, message: string): void {
    this.stdout.emit("data", Buffer.from(JSON.stringify({ jsonrpc: "2.0", id, error: { code, message } }) + "\n", "utf8"));
  }

  /** Send a JSON-RPC server->client request to stdout (no id match needed). */
  sendRequest(id: number, method: string, params: unknown): void {
    this.stdout.emit("data", Buffer.from(JSON.stringify({ jsonrpc: "2.0", id, method, params }) + "\n", "utf8"));
  }

  /** Send a JSON-RPC notification to stdout. */
  notify(method: string, params: unknown): void {
    this.stdout.emit("data", Buffer.from(JSON.stringify({ jsonrpc: "2.0", method, params }) + "\n", "utf8"));
  }
}

function makeSink(overrides: Partial<AcpClientSink> = {}): AcpClientSink {
  return {
    publish: () => {},
    requestPermission: async () => ({ optionId: "allow" }),
    askQuestion: async () => ({ text: "" }),
    ...overrides,
  };
}

const cursorProvider: AcpProviderDescriptor = {
  providerId: "cursor",
  command: "agent",
  args: ["acp"],
  authMethodId: "cursor_login",
};

const codexProvider: AcpProviderDescriptor = {
  providerId: "codex",
  command: "codex-acp",
  args: [],
  env: { NO_BROWSER: "1", INITIAL_AGENT_MODE: "agent" },
};

/** Drive a startSession handshake on the fake child: initialize [+ auth] + session/new. */
async function driveStartSession(child: FakeChildProcess, provider: AcpProviderDescriptor, expectAuth: boolean): Promise<void> {
  const init = await waitForRequest(child, "initialize");
  child.respond(init.id, {
    authMethods: expectAuth ? [{ id: provider.authMethodId }] : [{ id: "api-key" }],
  });
  if (expectAuth && provider.authMethodId) {
    const auth = await waitForRequest(child, "authenticate");
    child.respond(auth.id, {});
  }
  const newSession = await waitForRequest(child, "session/new");
  child.respond(newSession.id, { sessionId: "sess-1", configOptions: [] });
}

const consumedIds = new Set<number>();

beforeEach(() => { consumedIds.clear(); });

function waitForRequest(child: FakeChildProcess, method: string, timeoutMs = 1000): Promise<CapturedRequest> {
  return new Promise((resolve, reject) => {
    const start = Date.now();
    const tick = () => {
      const req = child.requests.find((r) => r.method === method && !consumedIds.has(r.id));
      if (req) {
        consumedIds.add(req.id);
        resolve(req);
        return;
      }
      if (Date.now() - start > timeoutMs) {
        reject(new Error(`timeout waiting for ${method}`));
        return;
      }
      setImmediate(tick);
    };
    tick();
  });
}

describe("AcpProviderExtension registry", () => {
  it("resolves the Cursor extension for cursor", () => {
    const ext = resolveAcpExtension("cursor");
    expect(ext).toBeInstanceOf(CursorAcpExtension);
  });

  it("resolves the Codex extension for codex", () => {
    const ext = resolveAcpExtension("codex");
    expect(ext).toBeInstanceOf(CodexAcpExtension);
  });

  it("returns undefined for an unknown provider", () => {
    expect(resolveAcpExtension("unknown")).toBeUndefined();
  });
});

describe("CursorAcpExtension", () => {
  const ext = new CursorAcpExtension();
  const baseCtx: AcpExtensionContext = {
    provider: cursorProvider,
    sink: makeSink(),
    traceId: "trace-1",
    sessionId: "sess-1",
  };

  it("handles cursor/ask_question via sink.askQuestion", async () => {
    let captured: unknown;
    const ctx: AcpExtensionContext = {
      ...baseCtx,
      sink: makeSink({
        askQuestion: async (req) => {
          captured = req;
          return { text: "yes" };
        },
      }),
    };
    const handled = await ext.handleServerRequest!(ctx, "cursor/ask_question", {
      requestId: "r1",
      question: "Continue?",
      options: [{ id: "yes", name: "Yes" }, { id: "no", name: "No" }],
    });
    expect(handled?.result).toEqual({ text: "yes" });
    expect((captured as { question: string }).question).toBe("Continue?");
  });

  it("handles cursor/create_plan by publishing an acp.plan event", async () => {
    let published: unknown;
    const ctx: AcpExtensionContext = {
      ...baseCtx,
      sink: makeSink({
        publish: (event) => { published = event; },
      }),
    };
    const handled = await ext.handleServerRequest!(ctx, "cursor/create_plan", {
      steps: [{ id: "s1", content: "Do thing", status: "pending" }],
    });
    expect(handled?.result).toEqual({ accepted: true });
    expect((published as { type: string }).type).toBe("acp.plan");
  });

  it("returns undefined for an unknown method", async () => {
    const handled = await ext.handleServerRequest!(baseCtx, "vendor/ping", {});
    expect(handled).toBeUndefined();
  });
});

describe("AcpJsonRpcClient — authenticate soft-fail", () => {
  it("skips authenticate when authMethodId is unset and reaches session/new", async () => {
    const child = new FakeChildProcess();
    const client = new AcpJsonRpcClient(() => child as unknown as import("node:child_process").ChildProcess);
    const provider: AcpProviderDescriptor = { providerId: "codex", command: "codex-acp", args: [] };
    const promise = client.startSession("c1", provider, "/tmp", makeSink());
    await driveStartSession(child, provider, false);
    await promise;
    expect(child.requests.map((r) => r.method)).toEqual(["initialize", "session/new"]);
  });

  it("soft-fails authenticate and still reaches session/new", async () => {
    const child = new FakeChildProcess();
    const logs: string[] = [];
    const logger = { info: () => {}, warn: (m: string) => logs.push(m), error: () => {}, debug: () => {} };
    const client = new AcpJsonRpcClient(() => child as unknown as import("node:child_process").ChildProcess, logger);
    const provider: AcpProviderDescriptor = { providerId: "codex", command: "codex-acp", args: [], authMethodId: "api-key" };

    const promise = client.startSession("c1", provider, "/tmp", makeSink());
    // initialize
    const init = await waitForRequest(child, "initialize");
    child.respond(init.id, { authMethods: [{ id: "api-key" }] });
    // authenticate — respond with error (soft-fail)
    const auth = await waitForRequest(child, "authenticate");
    child.respondError(auth.id, -32603, "Missing CODEX_API_KEY");
    // session/new should still be called
    const newSession = await waitForRequest(child, "session/new");
    child.respond(newSession.id, { sessionId: "sess-1", configOptions: [] });
    await promise;
    expect(child.requests.map((r) => r.method)).toEqual(["initialize", "authenticate", "session/new"]);
    expect(logs.some((m) => m.includes("soft-fail"))).toBe(true);
  });

  it("hard-fails when authMethodId is set but not advertised", async () => {
    const child = new FakeChildProcess();
    const client = new AcpJsonRpcClient(() => child as unknown as import("node:child_process").ChildProcess);
    const provider: AcpProviderDescriptor = { providerId: "codex", command: "codex-acp", args: [], authMethodId: "openai-api-key" };

    const promise = client.startSession("c1", provider, "/tmp", makeSink());
    const init = await waitForRequest(child, "initialize");
    child.respond(init.id, { authMethods: [{ id: "api-key" }] });
    await expect(promise).rejects.toThrow(/did not advertise auth method/);
    // session/new must NOT have been called
    expect(child.requests.find((r) => r.method === "session/new")).toBeUndefined();
  });
});

describe("AcpJsonRpcClient — spawn env merge", () => {
  it("passes provider.env defaults into the spawn env", async () => {
    let capturedEnv: NodeJS.ProcessEnv | undefined;
    const child = new FakeChildProcess();
    const spawnFn = (_cmd: string, _args: readonly string[], opts: { cwd: string; env: NodeJS.ProcessEnv }) => {
      capturedEnv = opts.env;
      return child as unknown as import("node:child_process").ChildProcess;
    };
    const client = new AcpJsonRpcClient(spawnFn);
    const promise = client.startSession("c1", codexProvider, "/tmp", makeSink());
    await driveStartSession(child, codexProvider, false);
    await promise;
    expect(capturedEnv?.NO_BROWSER).toBe("1");
    expect(capturedEnv?.INITIAL_AGENT_MODE).toBe("agent");
  });
});

describe("AcpJsonRpcClient — extension dispatch", () => {
  it("dispatches a vendor server request to the extension and returns its result", async () => {
    const fakeExt: AcpProviderExtension = {
      matches: (id) => id === "fake",
      handleServerRequest: async (_ctx, method, _params): Promise<AcpExtensionHandled | undefined> => {
        if (method === "vendor/ping") return { result: { pong: true } };
        return undefined;
      },
    };
    // Patch the resolver by using a provider id that the real registry doesn't know,
    // then inject the extension via a custom spawnFn-less path. We test the dispatch
    // by using the Cursor provider (which has a real extension) and cursor/ask_question.
    const child = new FakeChildProcess();
    const client = new AcpJsonRpcClient(() => child as unknown as import("node:child_process").ChildProcess);
    const sink = makeSink({
      askQuestion: async () => ({ text: "ok" }),
    });
    const promise = client.startSession("c1", cursorProvider, "/tmp", sink);
    // Drive handshake (cursor_login advertised + authenticated)
    const init = await waitForRequest(child, "initialize");
    child.respond(init.id, { authMethods: [{ id: "cursor_login" }] });
    const auth = await waitForRequest(child, "authenticate");
    child.respond(auth.id, {});
    const newSession = await waitForRequest(child, "session/new");
    child.respond(newSession.id, { sessionId: "sess-1", configOptions: [] });
    await promise;

    // Now send a cursor/ask_question server request
    const askResponse = new Promise<string>((resolve) => {
      child.stdin.write = ((line: string) => {
        const msg = JSON.parse(line);
        if (msg.result?.text === "ok") resolve(msg.result.text);
        return true;
      }) as typeof child.stdin.write;
    });
    child.sendRequest(500, "cursor/ask_question", { requestId: "r1", question: "ok?" });
    expect(await askResponse).toBe("ok");
    expect(fakeExt.matches("fake")).toBe(true);
  });

  it("returns -32601 for an unknown vendor method with no extension", async () => {
    const child = new FakeChildProcess();
    const client = new AcpJsonRpcClient(() => child as unknown as import("node:child_process").ChildProcess);
    const provider: AcpProviderDescriptor = { providerId: "unknown", command: "x", args: [] };
    const promise = client.startSession("c1", provider, "/tmp", makeSink());
    const init = await waitForRequest(child, "initialize");
    child.respond(init.id, { authMethods: [] });
    const newSession = await waitForRequest(child, "session/new");
    child.respond(newSession.id, { sessionId: "sess-1", configOptions: [] });
    await promise;

    const errorResponse = new Promise<number>((resolve) => {
      child.stdin.write = ((line: string) => {
        const msg = JSON.parse(line);
        if (msg.error?.code === -32601) resolve(msg.error.code);
        return true;
      }) as typeof child.stdin.write;
    });
    child.sendRequest(501, "vendor/unknown", {});
    expect(await errorResponse).toBe(-32601);
  });
});

describe("AcpJsonRpcClient — Gemini session/new modes+models", () => {
  const geminiProvider: AcpProviderDescriptor = {
    providerId: "gemini",
    command: "gemini",
    args: ["--acp"],
  };

  const geminiSessionNewResult = {
    sessionId: "sess-g1",
    modes: {
      currentModeId: "default",
      availableModes: [
        { id: "default", name: "Default" },
        { id: "yolo", name: "YOLO" },
      ],
    },
    models: {
      currentModelId: "gemini-2.5-pro",
      availableModels: [
        { modelId: "gemini-2.5-pro", name: "Gemini 2.5 Pro" },
        { modelId: "gemini-2.5-flash", name: "Gemini 2.5 Flash" },
      ],
    },
  };

  /** Drive a Gemini handshake: initialize (no auth) + session/new with modes+models. */
  async function driveGeminiStart(child: FakeChildProcess): Promise<void> {
    const init = await waitForRequest(child, "initialize");
    child.respond(init.id, { authMethods: [{ id: "oauth-personal" }] });
    const newSession = await waitForRequest(child, "session/new");
    child.respond(newSession.id, geminiSessionNewResult);
  }

  it("normalizes modes+models into synthetic configOptions (not [])", async () => {
    const child = new FakeChildProcess();
    const client = new AcpJsonRpcClient(() => child as unknown as import("node:child_process").ChildProcess);
    const promise = client.startSession("c1", geminiProvider, "/tmp", makeSink());
    await driveGeminiStart(child);
    await promise;
    const options = client.getConfigOptions("c1");
    expect(options).toHaveLength(2);
    const mode = options.find((o) => o.id === "mode");
    expect(mode).toBeDefined();
    expect(mode!.currentValue).toBe("default");
    expect(mode!.options).toHaveLength(2);
    const model = options.find((o) => o.id === "model");
    expect(model).toBeDefined();
    expect(model!.currentValue).toBe("gemini-2.5-pro");
    expect(model!.options).toHaveLength(2);
  });

  it("routes setConfigOption('mode','yolo') to session/set_mode and updates currentValue locally", async () => {
    const child = new FakeChildProcess();
    const client = new AcpJsonRpcClient(() => child as unknown as import("node:child_process").ChildProcess);
    const startPromise = client.startSession("c1", geminiProvider, "/tmp", makeSink());
    await driveGeminiStart(child);
    await startPromise;

    const setPromise = client.setConfigOption("c1", "mode", "yolo");
    const setModeReq = await waitForRequest(child, "session/set_mode");
    expect(setModeReq.params).toEqual({ sessionId: "sess-g1", modeId: "yolo" });
    // Gemini session/set_mode response is empty — core client mirrors value locally.
    child.respond(setModeReq.id, {});
    await setPromise;
    const mode = client.getConfigOptions("c1").find((o) => o.id === "mode");
    expect(mode!.currentValue).toBe("yolo");
  });

  it("routes setConfigOption('model', ...) to session/set_model", async () => {
    const child = new FakeChildProcess();
    const client = new AcpJsonRpcClient(() => child as unknown as import("node:child_process").ChildProcess);
    const startPromise = client.startSession("c1", geminiProvider, "/tmp", makeSink());
    await driveGeminiStart(child);
    await startPromise;

    const setPromise = client.setConfigOption("c1", "model", "gemini-2.5-flash");
    const setModelReq = await waitForRequest(child, "session/set_model");
    expect(setModelReq.params).toEqual({ sessionId: "sess-g1", modelId: "gemini-2.5-flash" });
    child.respond(setModelReq.id, {});
    await setPromise;
    const model = client.getConfigOptions("c1").find((o) => o.id === "model");
    expect(model!.currentValue).toBe("gemini-2.5-flash");
  });
});
