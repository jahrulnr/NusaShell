export type TransportType = "stdio" | "sse" | "http";

export const TRANSPORT_TYPES: readonly TransportType[] = [
  "stdio",
  "sse",
  "http",
] as const;
