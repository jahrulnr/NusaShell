import type {
  RequestMethod,
  ResponseEnvelope,
  EventEnvelope,
  EventType,
} from "@nusashell/contracts";
import { WebSocketConnection } from "./websocket-connection.js";
import { RequestManager } from "./request-manager.js";
import { EventSubscriber } from "./event-subscriber.js";
import { ReconnectPolicy, type ReconnectOptions, DEFAULT_RECONNECT_OPTIONS } from "./reconnect-policy.js";
import { PluginsApi, ToolsApi } from "../api/plugins-api.js";
import { ConnectionClosedError } from "../errors/connection-closed.error.js";
import { NusaClientError } from "../errors/nusa-client.error.js";

export interface NusaClientOptions {
  readonly url: string;
  readonly defaultTimeoutMs?: number;
  readonly reconnect?: Partial<ReconnectOptions>;
}

export type ReconnectStatusCallback = () => void;

export class NusaClient {
  readonly plugins: PluginsApi;
  readonly tools: ToolsApi;
  readonly events: EventSubscriber;

  private readonly connection: WebSocketConnection;
  private readonly requestManager: RequestManager;
  private readonly reconnectPolicy: ReconnectPolicy;
  private readonly reconnectOptions: ReconnectOptions;

  private intentionalDisconnect = false;
  private reconnecting = false;
  private reconnectTimer: ReturnType<typeof setTimeout> | undefined;
  private onReconnectCallback: ReconnectStatusCallback | undefined;
  private onReconnectFailedCallback: ReconnectStatusCallback | undefined;

  constructor(options: NusaClientOptions) {
    this.requestManager = new RequestManager(options.defaultTimeoutMs);
    this.events = new EventSubscriber();

    this.reconnectOptions = { ...DEFAULT_RECONNECT_OPTIONS, ...options.reconnect };
    this.reconnectPolicy = new ReconnectPolicy(this.reconnectOptions);

    this.connection = new WebSocketConnection(options.url, {
      onMessage: (data) => this.handleMessage(data),
      onOpen: () => {
        if (this.reconnecting) {
          this.reconnecting = false;
          this.reconnectPolicy.reset();
          this.onReconnectCallback?.();
        }
      },
      onClose: () => this.handleClose(),
      onError: () => {},
    });

    this.plugins = new PluginsApi(this);
    this.tools = new ToolsApi(this);
  }

  connect(): Promise<void> {
    this.intentionalDisconnect = false;
    return this.connection.connect();
  }

  async disconnect(): Promise<void> {
    this.intentionalDisconnect = true;
    this.cancelReconnectTimer();
    this.reconnecting = false;
    this.requestManager.close();
    this.events.clear();
    await this.connection.disconnect();
  }

  get isConnected(): boolean {
    return this.connection.isConnected;
  }

  get isReconnecting(): boolean {
    return this.reconnecting;
  }

  onReconnect(callback: ReconnectStatusCallback): void {
    this.onReconnectCallback = callback;
  }

  onReconnectFailed(callback: ReconnectStatusCallback): void {
    this.onReconnectFailedCallback = callback;
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

  private handleClose(): void {
    this.requestManager.close();

    if (this.intentionalDisconnect) {
      this.events.clear();
      return;
    }

    if (this.reconnectOptions.enabled && this.reconnectPolicy.shouldRetry()) {
      this.scheduleReconnect();
    } else {
      this.events.clear();
    }
  }

  private scheduleReconnect(): void {
    const delay = this.reconnectPolicy.getDelay();
    this.reconnecting = true;
    this.reconnectPolicy.recordAttempt();

    this.reconnectTimer = setTimeout(() => {
      this.reconnectTimer = undefined;
      void this.doReconnect();
    }, delay);
  }

  private async doReconnect(): Promise<void> {
    if (this.intentionalDisconnect) {
      this.reconnecting = false;
      return;
    }

    try {
      await this.connection.connect();
    } catch {
      if (this.reconnectPolicy.shouldRetry()) {
        this.scheduleReconnect();
      } else {
        this.reconnecting = false;
        this.events.clear();
        this.onReconnectFailedCallback?.();
      }
    }
  }

  private cancelReconnectTimer(): void {
    if (this.reconnectTimer) {
      clearTimeout(this.reconnectTimer);
      this.reconnectTimer = undefined;
    }
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
