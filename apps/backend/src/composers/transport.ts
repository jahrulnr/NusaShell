import {
  MessageRouter,
  WebSocketServer,
  WebSocketEventPublisher,
} from "@nusashell/transport-ws";
import type { EventDispatcher } from "@nusashell/application";
import type { Logger } from "@nusashell/infrastructure";
import type { ContainerOptions } from "../container.js";
import type { BusParts } from "./bus-registration.js";

export interface TransportParts {
  readonly router: MessageRouter;
  readonly wsServer: WebSocketServer;
  readonly eventPublisher: WebSocketEventPublisher;
}

export function createTransport(
  options: ContainerOptions,
  logger: Logger,
  eventDispatcher: EventDispatcher,
  buses: BusParts,
): TransportParts {
  const router = new MessageRouter({ commandBus: buses.commandBus, queryBus: buses.queryBus, logger });
  const wsServer = new WebSocketServer(router, {
    port: options.port,
    host: options.host ?? "0.0.0.0",
    logger,
  });
  const eventPublisher = new WebSocketEventPublisher(wsServer.sessionRegistry, wsServer.subscriptionRegistry);
  eventDispatcher.onAny(eventPublisher);
  return { router, wsServer, eventPublisher };
}
