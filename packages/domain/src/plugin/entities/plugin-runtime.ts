import { Entity } from "../../shared/entity.js";
import type { DomainError } from "../../shared/domain-error.js";
import type { Result } from "../../shared/result.js";
import { PluginStateChangedEvent } from "../events/plugin-state-changed.event.js";
import { RuntimeTransitionPolicy } from "../services/runtime-transition-policy.js";
import type { PluginId } from "../value-objects/plugin-id.js";
import type { PluginRuntimeState } from "../value-objects/runtime-state.js";

export class PluginRuntime extends Entity<PluginId> {
  private constructor(
    id: PluginId,
    readonly state: PluginRuntimeState,
  ) {
    super(id);
  }

  static create(id: PluginId, state: PluginRuntimeState): PluginRuntime {
    return new PluginRuntime(id, state);
  }

  static createIdle(id: PluginId): PluginRuntime {
    return new PluginRuntime(id, "idle");
  }

  transitionTo(
    next: PluginRuntimeState,
    occurredAt: Date = new Date(),
  ): Result<PluginRuntime, DomainError> {
    const transition = RuntimeTransitionPolicy.assertTransition(
      this.state,
      next,
    );
    if (!transition.ok) {
      return transition;
    }

    const nextRuntime = new PluginRuntime(this.id, next);
    nextRuntime.record(
      PluginStateChangedEvent.create(this.id, this.state, next, occurredAt),
    );
    return { ok: true, value: nextRuntime };
  }
}
