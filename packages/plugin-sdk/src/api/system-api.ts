import { NusaClient } from "../client/nusa-client.js";

export interface PingResult {
  readonly pong: boolean;
  readonly timestamp: string;
}

export interface VersionResult {
  readonly version: string;
  readonly name: string;
}

export class SystemApi {
  constructor(private readonly client: NusaClient) {}

  ping(timeoutMs?: number): Promise<PingResult> {
    return this.client.request("system.ping", {}, timeoutMs);
  }

  version(timeoutMs?: number): Promise<VersionResult> {
    return this.client.request("system.version", {}, timeoutMs);
  }
}
