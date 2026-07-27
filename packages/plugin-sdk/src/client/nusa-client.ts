import type {
  RequestMethod,
  ResponseEnvelope,
  EventEnvelope,
  EventType,
} from "@nusashell/contracts";
import { WebSocketConnection } from "./websocket-connection.js";
import { RequestManager } from "./request-manager.js";
import { EventSubscriber } from "./event-subscriber.js";
import { PluginsApi, ToolsApi } from "../api/plugins-api.js";
import { ConnectionClosedError } from "../errors/connection-closed.error.js";
import { NusaClientError } from "../errors/nusa-client.error.js";

export interface NusaClientOptions {
  readonly url: string;
  readonly defaultTimeoutMs?: number;
}

export class NusaClient {
  readonly plugins: PluginsApi;
  readonly tools: ToolsApi;
  readonly events: EventSubscriber;

  private readonly connection: WebSocketConnection;
  private readonly requestManager: RequestManager;

  constructor(options: NusaClientOptions) {
    this.requestManager = new RequestManager(options.defaultTimeoutMs);
    this.events = new EventSubscriber();

    this.connection = new WebSocketConnection(options.url, {
      onMessage: (data) => this.handleMessage(data),
      onOpen: () => {},
      onClose: () => {
        this.requestManager.close();
        this.events.clear();
      },
      onError: () => {},
    });

    this.plugins = new PluginsApi(this);
    this.tools = new ToolsApi(this);
  }

  connect(): Promise<void> {
    return this.connection.connect();
  }

  async disconnect(): Promise<void> {
    this.requestManager.close();
    this.events.clear();
    await this.connection.disconnect();
  }

  get isConnected(): boolean {
    return this.connection.isConnected;
  }

  request<TResult = unknown>(
    method: RequestMethod,
    payload: Record<string, unknown>,
    timeoutMs?: number,
  ): Promise<TResult> {
    if (!this.connection.isConnected) {
      return Promise.reject(new ConnectionClosedError("Not connected"));
    }

    const id = `req_${crypto.randomUUID()}`;
    const promise = this.requestManager.register(id, timeoutMs) as Promise<TResult>;

    const message = JSON.stringify({
      kind: "request",
      id,
      method,
      payload,
    });

    this.connection.send(message);
    return promise;
  }

  on<TPayload>(
    eventType: EventType,
    handler: (payload: TPayload) => void,
  ): () => void {
    return this.events.on(eventType, handler);
  }

  private handleMessage(data: string): void {
    let parsed: unknown;
    try {
      parsed = JSON.parse(data);
    } catch {
      return;
    }

    if (!parsed || typeof parsed !== "object") return;

    const msg = parsed as Record<string, unknown>;

    if (msg.kind === "response") {
      this.handleResponse(msg as unknown as ResponseEnvelope);
    } else if (msg.kind === "event") {
      this.handleEvent(msg as unknown as EventEnvelope);
    }
  }

  private handleResponse(response: ResponseEnvelope): void {
    if (response.ok) {
      this.requestManager.resolve(response.id, response.result);
    } else {
      this.requestManager.reject(
        response.id,
        new NusaClientError(response.error.code, response.error.message),
      );
    }
  }

  private handleEvent(envelope: EventEnvelope): void {
    this.events.dispatch(envelope);
  }
}
