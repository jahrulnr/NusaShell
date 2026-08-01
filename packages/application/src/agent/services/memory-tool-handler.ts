import { ApplicationError } from "../../errors/application-error.js";
import type { MemoryStorePort, MemoryTarget } from "../../memory/ports/memory-store.port.js";
import { requireString, optionalString, memoryNotConfigured } from "./gateway-utils.js";

export async function execMemory(
  store: MemoryStorePort | undefined,
  args: Readonly<Record<string, unknown>>,
): Promise<unknown> {
  if (!store) return memoryNotConfigured();
  const action = requireString(args.action, "action");
  const target = requireString(args.target, "target") as MemoryTarget;
  if (target !== "memory" && target !== "user") {
    throw new ApplicationError("AGENT_INVALID_INPUT", `target must be "memory" or "user"`);
  }
  const content = optionalString(args.content);
  const oldText = optionalString(args.old_text);
  try {
    switch (action) {
      case "add":
        if (!content) throw new ApplicationError("AGENT_INVALID_INPUT", "content is required for add");
        return await store.add(target, content);
      case "replace":
        if (!oldText) throw new ApplicationError("AGENT_INVALID_INPUT", "old_text is required for replace");
        return await store.replace(target, oldText, content);
      case "remove":
        if (!oldText) throw new ApplicationError("AGENT_INVALID_INPUT", "old_text is required for remove");
        return await store.remove(target, oldText);
      default:
        throw new ApplicationError("AGENT_INVALID_INPUT", `Unsupported memory action: ${action}`);
    }
  } catch (error) {
    return {
      ok: false,
      error: { code: "memory_error", message: error instanceof Error ? error.message : String(error) },
      meta: {},
    };
  }
}
