import { z } from "zod";
import { FILES_TOOL_NAMES } from "./tool-catalog.js";

const filePath = z.string().trim().min(1).max(4096);
const depth = z.number().int().min(1).max(10).default(3);
const head = z.number().int().min(1).max(100000).optional();
const tail = z.number().int().min(1).max(100000).optional();
const recursive = z.boolean().default(false);
const pattern = z.string().trim().min(1).max(500);

const schemas = {
  files_list: z.object({ path: filePath.default("/") }).strict(),
  files_tree: z.object({ path: filePath.default("/"), depth }).strict(),
  files_read: z.object({ path: filePath, head, tail }).strict(),
  files_write: z.object({ path: filePath, content: z.string().max(10 * 1024 * 1024) }).strict(),
  files_mkdir: z.object({ path: filePath }).strict(),
  files_move: z.object({ source: filePath, destination: filePath }).strict(),
  files_delete: z.object({ path: filePath, recursive }).strict(),
  files_search: z.object({ path: filePath.default("/"), pattern }).strict(),
  files_info: z.object({ path: filePath }).strict(),
};

export const FILES_TOOLS = Object.freeze([
  descriptor("files_list", "List directory contents with file metadata (name, size, modified, type).", {
    path: stringProperty("Directory path relative to root. Defaults to root.", "/"),
  }),
  descriptor("files_tree", "Recursive directory tree up to a depth limit.", {
    path: stringProperty("Directory path relative to root. Defaults to root.", "/"),
    depth: integerProperty(1, 10, 3, "Maximum tree depth (1-10)."),
  }),
  descriptor("files_read", "Read a text file. Use head or tail to limit output.", {
    path: stringProperty("File path relative to root."),
    head: integerProperty(1, 100000, undefined, "Number of lines from the top."),
    tail: integerProperty(1, 100000, undefined, "Number of lines from the bottom."),
  }, ["path"]),
  descriptor("files_write", "Create or overwrite a file. Parent directories are created automatically.", {
    path: stringProperty("File path relative to root."),
    content: { type: "string", description: "File content (UTF-8 text, max 10 MB)." },
  }, ["path", "content"], false),
  descriptor("files_mkdir", "Create an empty directory. Missing parent directories are created automatically.", {
    path: stringProperty("Directory path relative to root."),
  }, ["path"], false),
  descriptor("files_move", "Move or rename a file or directory.", {
    source: stringProperty("Current path relative to root."),
    destination: stringProperty("New path relative to root."),
  }, ["source", "destination"], false),
  descriptor("files_delete", "Delete a file or directory. Directories require recursive=true if not empty.", {
    path: stringProperty("Path to delete relative to root."),
    recursive: { type: "boolean", description: "Allow deleting non-empty directories.", default: false },
  }, ["path"], false),
  descriptor("files_search", "Search for files by name pattern (glob: * and ?).", {
    path: stringProperty("Search root directory. Defaults to root.", "/"),
    pattern: stringProperty("Glob pattern (e.g. *.txt, config.*, *.test.js)."),
  }, ["pattern"]),
  descriptor("files_info", "Get detailed file metadata (size, dates, permissions, type).", {
    path: stringProperty("File or directory path relative to root."),
  }, ["path"]),
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
      return { path: input.path, tree: await service.tree(input.path, input.depth) };
    case "files_read":
      return { path: input.path, ...(await service.readFile(input.path, input.head, input.tail)) };
    case "files_write":
      return await service.writeFile(input.path, input.content);
    case "files_mkdir":
      return await service.makeDir(input.path);
    case "files_move":
      return await service.moveFile(input.source, input.destination);
    case "files_delete":
      return await service.deleteFile(input.path, input.recursive);
    case "files_search":
      return { path: input.path, pattern: input.pattern, results: await service.searchFiles(input.path, input.pattern) };
    case "files_info":
      return await service.fileInfo(input.path);
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
