import type { DomainError } from "../../shared/domain-error.js";
import type { Result } from "../../shared/result.js";
import { err, ok } from "../../shared/result.js";
import type { Plugin } from "../entities/plugin.js";
import type { PluginRuntime } from "../entities/plugin-runtime.js";
import { PluginDisabledError } from "../errors/plugin-disabled.error.js";
import { InvalidRuntimeTransitionError } from "../errors/invalid-runtime-transition.error.js";
import { PluginId } from "../value-objects/plugin-id.js";
import { RuntimeTransitionPolicy } from "./runtime-transition-policy.js";

export const PluginLifecyclePolicy = {
  canStart(plugin: Plugin, runtime: PluginRuntime): Result<true, DomainError> {
    if (!plugin.enabled) {
      return err(new PluginDisabledError(PluginId.toString(plugin.id)));
    }
    if (runtime.state === "starting" || runtime.state === "running") {
      return ok(true);
    }
    if (
      runtime.state === "idle" ||
      runtime.state === "crashed"
    ) {
      return RuntimeTransitionPolicy.assertTransition(runtime.state, "starting").ok
        ? ok(true)
        : err(
            new InvalidRuntimeTransitionError(runtime.state, "starting"),
          );
    }
    return err(
      new InvalidRuntimeTransitionError(runtime.state, "starting"),
    );
  },

  canStop(_plugin: Plugin, runtime: PluginRuntime): Result<true, DomainError> {
    if (
      runtime.state === "running" ||
      runtime.state === "background" ||
      runtime.state === "starting"
    ) {
      const transition = RuntimeTransitionPolicy.assertTransition(
        runtime.state,
        "stopping",
      );
      return transition.ok ? ok(true) : transition;
    }
    return err(
      new InvalidRuntimeTransitionError(runtime.state, "stopping"),
    );
  },

  canCallTool(
    plugin: Plugin,
    runtime: PluginRuntime,
  ): Result<true, DomainError> {
    if (!plugin.enabled) {
      return err(new PluginDisabledError(PluginId.toString(plugin.id)));
    }
    if (runtime.state !== "running") {
      return err(
        new InvalidRuntimeTransitionError(runtime.state, "running"),
      );
    }
    return ok(true);
  },
};
