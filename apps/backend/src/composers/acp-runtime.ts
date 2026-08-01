import { spawn } from "node:child_process";
import { AcpJsonRpcClient, type Logger } from "@nusashell/infrastructure";
import {
  AcpSessionService,
  AcpPermissionService,
  AcpAskBridgeService,
  type EventDispatcher,
} from "@nusashell/application";
import type { ContainerOptions } from "../container.js";
import type { AgentRuntimeParts } from "./agent-runtime.js";

export interface AcpRuntimeParts {
  readonly acpClient: AcpJsonRpcClient;
  readonly acpSessionService: AcpSessionService;
  readonly acpPermissionService: AcpPermissionService;
  readonly acpAskService: AcpAskBridgeService;
}

export function createAcpRuntime(
  _options: ContainerOptions,
  logger: Logger,
  eventDispatcher: EventDispatcher,
  agent: AgentRuntimeParts,
): AcpRuntimeParts {
  const acpClient = new AcpJsonRpcClient(spawn, logger);
  const acpPermissionService = new AcpPermissionService();
  const acpAskService = new AcpAskBridgeService();
  const acpSessionService = new AcpSessionService({
    client: acpClient,
    permissionService: acpPermissionService,
    askService: acpAskService,
    eventDispatcher,
    logger,
    streamSeq: agent.streamSeqRegistry,
  });
  return { acpClient, acpSessionService, acpPermissionService, acpAskService };
}
