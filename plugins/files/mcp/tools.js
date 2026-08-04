import { z } from "zod";
import { FILES_TOOL_NAMES } from "./tool-catalog.js";

const filePath = z.string().trim().min(1).max(4096);
const rootPath = z.string().trim().min(0).max(4096).default("");
const depth = z.number().int().min(1).max(10).default(3);
const head = z.number().int().min(1).max(100000).optional();
const tail = z.number().int().min(1).max(100000).optional();
const startLine = z.number().int().min(1).max(100000).optional();
const endLine = z.number().int().min(1).max(100000).optional();
const lineNumbers = z.boolean().default(false);
const maxBytes = z.number().int().min(1).max(100 * 1024 * 1024).default(10 * 1024 * 1024);
const recursive = z.boolean().default(false);
const pattern = z.string().trim().min(1).max(500);

const grepGlob = z.string().trim().min(1).max(500).optional();
const excludeGlobs = z.array(z.string().trim().min(1).max(500)).max(20).optional();
const oldString = z.string().min(1).max(1024 * 1024);
const newString = z.string().max(1024 * 1024);

const schemas = {
  files_list: z.object({ path: rootPath }).strict(),
  files_tree: z.object({ path: rootPath, depth, exclude: excludeGlobs, includeFiles: z.boolean().default(true) }).strict(),
  files_read: z.object({ path: filePath, head, tail, start: startLine, end: endLine, lineNumbers, maxBytes }).strict(),
  files_write: z.object({ path: filePath, content: z.string().max(10 * 1024 * 1024) }).strict(),
  files_mkdir: z.object({ path: filePath }).strict(),
  files_move: z.object({ source: filePath, destination: filePath }).strict(),
  files_copy: z.object({ source: filePath, destination: filePath }).strict(),
  files_delete: z.object({ path: filePath, recursive }).strict(),
  files_search: z.object({ path: rootPath, pattern, exclude: excludeGlobs, type: z.enum(["file", "dir", "any"]).default("any"), maxDepth: z.number().int().min(1).max(20).default(10) }).strict(),
  files_info: z.object({ path: filePath }).strict(),
  files_grep: z.object({ path: rootPath, pattern, glob: grepGlob, before: z.number().int().min(0).max(10).default(0), after: z.number().int().min(0).max(10).default(0), ignoreCase: z.boolean().default(false), exclude: excludeGlobs, maxResults: z.number().int().min(1).max(1000).default(500) }).strict(),
  files_patch: z.object({
    path: filePath,
    edits: z.union([
      z.object({ old_string: oldString, new_string: newString, replace_all: z.boolean().default(false) }),
      z.array(z.object({ old_string: oldString, new_string: newString, replace_all: z.boolean().default(false) })),
    ]),
    preview: z.boolean().default(false),
  }).strict(),
  files_append: z.object({ path: filePath, content: z.string().max(10 * 1024 * 1024) }).strict(),
  files_exists: z.object({ path: filePath }).strict(),
  files_touch: z.object({ path: filePath, createParents: z.boolean().default(true), updateOnly: z.boolean().default(false) }).strict(),
};

export const FILES_TOOLS = Object.freeze([
  descriptor("files_list", "List directory contents with file metadata (name, size, modified, type).", {
    path: stringProperty("Directory path relative to the files plugin root (user home by default). Use empty string for root; \"/\" resolves to the OS filesystem root.", ""),
  }),
  descriptor("files_tree", "Recursive directory tree up to a depth limit. Supports exclude globs and includeFiles filter.", {
    path: stringProperty("Directory path relative to the files plugin root (user home by default). Use empty string for root; \"/\" resolves to the OS filesystem root.", ""),
    depth: integerProperty(1, 10, 3, "Maximum tree depth (1-10)."),
    exclude: { type: "array", items: { type: "string" }, description: "Glob patterns to exclude (e.g. [\"node_modules\", \".git\"]). Max 20." },
    includeFiles: { type: "boolean", description: "Include files in the tree (default true). Set false for dirs-only.", default: true },
  }),
  descriptor("files_read", "Read a text file. Use start/end for a line range, or head/tail for first/last N lines. Binary files are rejected.", {
    path: stringProperty("File path relative to the files plugin root (user home by default)."),
    head: integerProperty(1, 100000, undefined, "Number of lines from the top (legacy, use start/end for ranges)."),
    tail: integerProperty(1, 100000, undefined, "Number of lines from the bottom (legacy, use start/end for ranges)."),
    start: integerProperty(1, 100000, undefined, "1-based start line (inclusive). Takes priority over head/tail."),
    end: integerProperty(1, 100000, undefined, "1-based end line (inclusive). Takes priority over head/tail."),
    lineNumbers: { type: "boolean", description: "Prefix each line with its 1-based line number (NNN|content).", default: false },
    maxBytes: integerProperty(1, 104857600, 10485760, "Maximum file size in bytes (default 10 MB)."),
  }, ["path"]),
  descriptor("files_write", "Create or overwrite a file. Parent directories are created automatically.", {
    path: stringProperty("File path relative to the files plugin root (user home by default)."),
    content: { type: "string", description: "File content (UTF-8 text, max 10 MB)." },
  }, ["path", "content"], false),
  descriptor("files_mkdir", "Create an empty directory. Missing parent directories are created automatically.", {
    path: stringProperty("Directory path relative to the files plugin root (user home by default)."),
  }, ["path"], false),
  descriptor("files_move", "Move or rename a file or directory.", {
    source: stringProperty("Current path relative to the files plugin root (user home by default)."),
    destination: stringProperty("New path relative to the files plugin root (user home by default)."),
  }, ["source", "destination"], false),
  descriptor("files_copy", "Copy a file or directory recursively. Destination parent directories are created automatically.", {
    source: stringProperty("Path to copy from, relative to the files plugin root (user home by default)."),
    destination: stringProperty("Path to copy to, relative to the files plugin root (user home by default)."),
  }, ["source", "destination"], false),
  descriptor("files_delete", "Delete a file or directory. Directories require recursive=true if not empty.", {
    path: stringProperty("Path to delete relative to the files plugin root (user home by default)."),
    recursive: { type: "boolean", description: "Allow deleting non-empty directories.", default: false },
  }, ["path"], false),
  descriptor("files_search", "Search for files by name pattern (glob: * and ?). Supports exclude, type filter, and maxDepth.", {
    path: stringProperty("Search root directory relative to the files plugin root. Use empty string for root; \"/\" resolves to the OS filesystem root.", ""),
    pattern: stringProperty("Glob pattern (e.g. *.txt, config.*, *.test.js)."),
    exclude: { type: "array", items: { type: "string" }, description: "Glob patterns to exclude (e.g. [\"node_modules\", \".git\"]). Max 20." },
    type: { type: "string", enum: ["file", "dir", "any"], description: "Filter by entry type (default any).", default: "any" },
    maxDepth: integerProperty(1, 20, 10, "Maximum search depth (1-20)."),
  }, ["pattern"]),
  descriptor("files_info", "Get detailed file metadata (size, dates, permissions, type).", {
    path: stringProperty("File or directory path relative to the files plugin root (user home by default)."),
  }, ["path"]),
  descriptor("files_grep", "Search file contents for a regex pattern (like grep). Only text files are scanned. Supports context lines, ignoreCase, and exclude globs.", {
    path: stringProperty("Directory to search in, relative to the files plugin root. Use empty string for root; \"/\" resolves to the OS filesystem root.", ""),
    pattern: stringProperty("Regular expression pattern to match against file contents (e.g. 'function\\s+\\w+', 'TODO.*')."),
    glob: stringProperty("Optional file name glob filter to narrow search (e.g. '*.js', '*.ts'). If omitted, all text files are scanned."),
    before: integerProperty(0, 10, 0, "Context lines before each match (0-10)."),
    after: integerProperty(0, 10, 0, "Context lines after each match (0-10)."),
    ignoreCase: { type: "boolean", description: "Case-insensitive matching.", default: false },
    exclude: { type: "array", items: { type: "string" }, description: "Glob patterns to exclude (e.g. [\"node_modules\", \".git\"]). Max 20." },
    maxResults: integerProperty(1, 1000, 500, "Maximum number of results (1-1000)."),
  }, ["pattern"]),
  descriptor("files_patch", "Replace one or more string occurrences in a file. Supports replace_all and preview mode. Safer than files_write for small edits.", {
    path: stringProperty("File path relative to the files plugin root (user home by default)."),
    edits: {
      type: "object",
      description: "A single edit object or an array of edits. Each edit: { old_string, new_string, replace_all }.",
      properties: {
        old_string: { type: "string", description: "Exact string to find (must match exactly, including whitespace)." },
        new_string: { type: "string", description: "Replacement string." },
        replace_all: { type: "boolean", description: "Replace all occurrences (default: false = first only).", default: false },
      },
    },
    preview: { type: "boolean", description: "If true, return the patched content without writing to disk.", default: false },
  }, ["path", "edits"], false),
  descriptor("files_append", "Append content to the end of a file. Creates the file if it does not exist.", {
    path: stringProperty("File path relative to the files plugin root (user home by default)."),
    content: { type: "string", description: "Content to append (UTF-8 text, max 10 MB)." },
  }, ["path", "content"], false),
  descriptor("files_exists", "Check if a path exists. Returns { exists, isFile, isDir }. Does NOT throw on missing paths.", {
    path: stringProperty("File or directory path relative to the files plugin root (user home by default)."),
  }, ["path"]),
  descriptor("files_touch", "Create an empty file if it doesn't exist, or update its timestamps if it does.", {
    path: stringProperty("File path relative to the files plugin root (user home by default)."),
    createParents: { type: "boolean", description: "Create parent directories if needed (default true).", default: true },
    updateOnly: { type: "boolean", description: "Only update timestamps of an existing file; throw if it doesn't exist.", default: false },
  }, ["path"], false),
]);

if (FILES_TOOLS.map((tool) => tool.name).join(",") !== FILES_TOOL_NAMES.join(",")) {
  throw new Error("Files tool descriptors are out of sync with the canonical catalog");
}

export async function callFilesTool(service, name, rawArguments = {}) {
  const schema = schemas[name];
  if (!schema) throw new Error(`Unknown files tool: ${name}`);
  const input = schema.parse(rawArguments ?? {});

  switch (name) {
    case "files_list":
      return { path: input.path, items: await service.listDir(input.path) };
    case "files_tree":
      return { path: input.path, tree: await service.tree(input.path, input.depth, { exclude: input.exclude, includeFiles: input.includeFiles }) };
    case "files_read":
      return { path: input.path, ...(await service.readFile(input.path, {
        head: input.head,
        tail: input.tail,
        start: input.start,
        end: input.end,
        lineNumbers: input.lineNumbers,
        maxBytes: input.maxBytes,
      })) };
    case "files_write":
      return await service.writeFile(input.path, input.content);
    case "files_mkdir":
      return await service.makeDir(input.path);
    case "files_move":
      return await service.moveFile(input.source, input.destination);
    case "files_copy":
      return await service.copyFile(input.source, input.destination);
    case "files_delete":
      return await service.deleteFile(input.path, input.recursive);
    case "files_search": {
      const searchResult = await service.searchFiles(input.path, input.pattern, { exclude: input.exclude, type: input.type, maxDepth: input.maxDepth });
      return { path: input.path, pattern: input.pattern, results: searchResult.results, meta: searchResult.meta };
    }
    case "files_grep": {
      const grepResult = await service.grepFiles(input.path, input.pattern, {
        glob: input.glob,
        before: input.before,
        after: input.after,
        ignoreCase: input.ignoreCase,
        exclude: input.exclude,
        maxResults: input.maxResults,
      });
      return { path: input.path, pattern: input.pattern, ...(input.glob ? { glob: input.glob } : {}), results: grepResult.results, meta: grepResult.meta };
    }
    case "files_info":
      return await service.fileInfo(input.path);
    case "files_patch": {
      const edits = Array.isArray(input.edits) ? input.edits : [input.edits];
      return await service.patchFile(input.path, edits, input.preview);
    }
    case "files_append":
      return await service.appendFile(input.path, input.content);
    case "files_exists":
      return await service.existsFile(input.path);
    case "files_touch":
      return await service.touchFile(input.path, { createParents: input.createParents, updateOnly: input.updateOnly });
    default:
      throw new Error(`Unknown files tool: ${name}`);
  }
}

function descriptor(name, description, properties, required = [], readOnly = true) {
  return {
    name,
    description,
    annotations: {
      title: name,
      readOnlyHint: readOnly,
      destructiveHint: name === "files_delete",
      idempotentHint: readOnly || name === "files_write" || name === "files_move",
      openWorldHint: false,
    },
    inputSchema: {
      type: "object",
      properties,
      required,
      additionalProperties: false,
    },
  };
}

function stringProperty(description, defaultValue) {
  return {
    type: "string",
    description,
    ...(defaultValue ? { default: defaultValue } : {}),
  };
}

function integerProperty(minimum, maximum, defaultValue, description) {
  return {
    type: "integer",
    description,
    minimum,
    ...(maximum ? { maximum } : {}),
    ...(defaultValue ? { default: defaultValue } : {}),
  };
}
