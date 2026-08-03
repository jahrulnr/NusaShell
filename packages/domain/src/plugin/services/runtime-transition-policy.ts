import type { DomainError } from "../../shared/domain-error.js";
import type { Result } from "../../shared/result.js";
import { ok } from "../../shared/result.js";
import { InvalidRuntimeTransitionError } from "../errors/invalid-runtime-transition.error.js";
import type { PluginRuntimeState } from "../value-objects/runtime-state.js";

const ALLOWED_TRANSITIONS: Readonly<
  Record<PluginRuntimeState, readonly PluginRuntimeState[]>
> = {
  idle: ["starting", "disabled"],
  starting: ["running", "stopping", "crashed"],
  running: ["stopping", "background", "crashed"],
  background: ["running", "stopping", "crashed"],
  stopping: ["idle", "crashed"],
  crashed: ["starting", "idle", "disabled"],
  disabled: ["idle"],
};

export const RuntimeTransitionPolicy = {
  canTransition(from: PluginRuntimeState, to: PluginRuntimeState): boolean {
    return ALLOWED_TRANSITIONS[from].includes(to);
  },

  assertTransition(
    from: PluginRuntimeState,
    to: PluginRuntimeState,
  ): Result<PluginRuntimeState, DomainError> {
    if (!this.canTransition(from, to)) {
      return {
        ok: false,
        error: new InvalidRuntimeTransitionError(from, to),
      };
    }
    return ok(to);
  },
};
