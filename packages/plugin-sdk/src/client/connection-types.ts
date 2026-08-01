export type ConnectionStatus = "disconnected" | "connecting" | "connected";

export interface WebSocketConnectionCallbacks {
  onMessage: (data: string) => void;
  onOpen: () => void;
  onClose: () => void;
  onError: (error: Error) => void;
}

/**
 * Abstract connection interface that both Node.js (`ws`) and browser
 * (native WebSocket) implementations satisfy. Allows NusaClient to
 * work in both environments via dependency injection.
 */
export interface IWebSocketConnection {
  connect(): Promise<void>;
  send(data: string): void;
  disconnect(): Promise<void>;
  readonly isConnected: boolean;
  readonly connectionStatus: ConnectionStatus;
}

export type WebSocketConnectionFactory = (
  url: string,
  callbacks: WebSocketConnectionCallbacks,
) => IWebSocketConnection | Promise<IWebSocketConnection>;
