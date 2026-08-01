import path from "node:path";

/**
 * Workspace binding for MCP tool calls (Phase 1 host wrap).
 *
 * `conversation.workspace` is the source of truth for agent tool I/O. The
 * shell injects it into bundled path/cwd-shaped tool arguments so the model
 * cannot forget to pass an absolute path. Wrapping is host-side and only
 * rewrites *relative* values; explicit absolute paths and root markers are
 * preserved so the model can still target OS-absolute locations when it means
 * to. Containment is still enforced by each plugin server (Files root guard,
 * Terminal cwd validation), so wrapping never grants escape.
 */

const TERMINAL_PLUGIN_ID = "nusashell.terminal";
const FILES_PLUGIN_ID = "nusashell.files";

const TERMINAL_CWD_TOOLS: ReadonlySet<string> = new Set(["terminal_exec", "terminal_open"]);

const FILES_PATH_FIELDS: Readonly<Record<string, readonly string[]>> = {
  files_list: ["path"],
  files_tree: ["path"],
  files_read: ["path"],
  files_write: ["path"],
  files_mkdir: ["path"],
  files_delete: ["path"],
  files_search: ["path"],
  files_info: ["path"],
  files_grep: ["path"],
  files_patch: ["path"],
  files_append: ["path"],
  files_move: ["source", "destination"],
  files_copy: ["source", "destination"],
};

export interface WorkspaceToolWrapResult {
  readonly args: Readonly<Record<string, unknown>>;
  readonly rewritten: readonly string[];
}

function isRelativePath(value: unknown): value is string {
  return typeof value === "string"
    && value.length > 0
    && value !== "/"
    && !path.isAbsolute(value);
}

function joinWorkspace(workspace: string, relative: string): string {
  return path.resolve(workspace, relative);
}

/**
 * Inject `cwd` for Terminal tools. Omitted, empty, or relative `cwd` becomes
 * the absolute workspace. An explicit absolute `cwd` is preserved.
 */
export function wrapTerminalArgs(
  toolName: string,
  args: Readonly<Record<string, unknown>>,
  workspace: string,
): WorkspaceToolWrapResult {
  if (!TERMINAL_CWD_TOOLS.has(toolName)) return { args, rewritten: [] };
  const cwd = args.cwd;
  if (typeof cwd === "string" && cwd.trim() && path.isAbsolute(cwd.trim())) {
    return { args, rewritten: [] };
  }
  return {
    args: { ...args, cwd: path.resolve(workspace) },
    rewritten: ["cwd"],
  };
}

/**
 * Rewrite relative path-shaped arguments for Files tools into absolute paths
 * under the workspace. Absolute paths, `/`, and empty values are preserved.
 * The Files server still enforces root containment, so an absolute path
 * outside the Files root is rejected (the "else need Phase 2/3" fallback).
 */
export function wrapFilesArgs(
  toolName: string,
  args: Readonly<Record<string, unknown>>,
  workspace: string,
): WorkspaceToolWrapResult {
  const fields = FILES_PATH_FIELDS[toolName];
  if (!fields) return { args, rewritten: [] };
  const next: Record<string, unknown> = { ...args };
  const rewritten: string[] = [];
  for (const field of fields) {
    if (isRelativePath(args[field])) {
      next[field] = joinWorkspace(workspace, args[field] as string);
      rewritten.push(field);
    }
  }
  return { args: next, rewritten };
}

/**
 * Dispatch workspace wrapping for a granted tool. Unknown plugins are passed
 * through unchanged so third-party MCP servers are never silently mutated.
 */
export function wrapToolArgs(
  pluginId: string,
  toolName: string,
  args: Readonly<Record<string, unknown>>,
  workspace: string | undefined,
): Readonly<Record<string, unknown>> {
  if (!workspace) return args;
  if (pluginId === TERMINAL_PLUGIN_ID) return wrapTerminalArgs(toolName, args, workspace).args;
  if (pluginId === FILES_PLUGIN_ID) return wrapFilesArgs(toolName, args, workspace).args;
  return args;
}
