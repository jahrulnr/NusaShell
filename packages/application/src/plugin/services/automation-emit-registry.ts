import type { AutomationConfig } from "@nusashell/domain";

/**
 * Registry of which plugin owns which automation event types.
 *
 * Built from loaded manifests. On plugin start, each `emits[].type` is
 * registered → `pluginId`. When a notification arrives, the shell checks
 * whether the connection's bound `pluginId` owns the declared `type`.
 *
 * Type collisions (two plugins declaring the same type) are rejected at
 * registration time.
 *
 * See tmp/plan/watch-to-agent/04-mcp-automation-contract.md §Event type ownership.
 */
export class AutomationEmitRegistry {
  private readonly typeToPlugin = new Map<string, string>();
  private readonly pluginToTypes = new Map<string, Set<string>>();

  /**
   * Register a plugin's automation emits. Throws on type collision with
   * another plugin.
   */
  register(pluginId: string, automation: AutomationConfig | undefined): void {
    if (!automation?.emits?.length) return;
    const types = new Set<string>();
    for (const emit of automation.emits) {
      const existingOwner = this.typeToPlugin.get(emit.type);
      if (existingOwner !== undefined && existingOwner !== pluginId) {
        throw new Error(
          `Automation emit type collision: "${emit.type}" is already owned by plugin "${existingOwner}", cannot register for "${pluginId}"`,
        );
      }
      this.typeToPlugin.set(emit.type, pluginId);
      types.add(emit.type);
    }
    this.pluginToTypes.set(pluginId, types);
  }

  /** Unregister a plugin's emits (e.g. on plugin stop/uninstall). */
  unregister(pluginId: string): void {
    const types = this.pluginToTypes.get(pluginId);
    if (!types) return;
    for (const type of types) {
      this.typeToPlugin.delete(type);
    }
    this.pluginToTypes.delete(pluginId);
  }

  /**
   * Check if a plugin is allowed to emit a given event type. Returns `true`
   * when the plugin owns the type (it declared it in `emits`).
   */
  isOwnedBy(pluginId: string, eventType: string): boolean {
    return this.typeToPlugin.get(eventType) === pluginId;
  }

  /** Get all event types owned by a plugin. */
  emitsFor(pluginId: string): readonly string[] {
    const types = this.pluginToTypes.get(pluginId);
    return types ? [...types] : [];
  }

  /** Get the owning plugin for an event type, or `undefined`. */
  ownerOf(eventType: string): string | undefined {
    return this.typeToPlugin.get(eventType);
  }

  /** Clear all registrations. */
  clear(): void {
    this.typeToPlugin.clear();
    this.pluginToTypes.clear();
  }
}
