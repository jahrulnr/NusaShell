import type { ApplicationEvent } from "@nusashell/application";
import type { EventEnvelope } from "@nusashell/contracts";
import { mapPluginEvent } from "./plugin-event.mapper.js";
import { mapAgentEvent } from "./agent-event.mapper.js";
import { mapJobEvent } from "./job-event.mapper.js";
import { mapAcpEvent } from "./acp-event.mapper.js";

export function mapDomainEvent(event: ApplicationEvent, sequence: number): EventEnvelope | null {
  const envelope = mapDomainEventInner(event, sequence);
  if (envelope && event.streamSeq !== undefined) {
    return {
      ...envelope,
      payload: { ...(envelope.payload as Record<string, unknown>), streamSeq: event.streamSeq },
    };
  }
  return envelope;
}

function mapDomainEventInner(event: ApplicationEvent, sequence: number): EventEnvelope | null {
  const timestamp = event.occurredAt.toISOString();

  // Try each domain mapper in order; first match wins. Unknown events fall
  // through to null. Order is by event frequency: agent > plugin > acp > job.
  return (
    mapAgentEvent(event, sequence, timestamp) ??
    mapPluginEvent(event, sequence, timestamp) ??
    mapAcpEvent(event, sequence, timestamp) ??
    mapJobEvent(event, sequence, timestamp)
  );
}
