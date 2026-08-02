export const FILES_PROMPTS = Object.freeze([
  {
    name: "howto",
    title: "Files plugin how-to",
    description: "How to inspect and modify files within the Files root.",
  },
]);

export function getFilesPrompt(name) {
  if (name !== "howto") throw new Error(`Unknown prompt: ${name}`);
  return {
    description: FILES_PROMPTS[0].description,
    messages: [{
      role: "user",
      content: {
        type: "text",
        text: [
          "Use the Files plugin for bounded filesystem operations.",
          "",
          "Main tools:",
          "- files_list / files_tree: inspect directories.",
          "- files_read: read text files, optionally using head or tail.",
          "- files_write / files_append / files_patch: change text files.",
          "- files_mkdir / files_move / files_copy / files_delete: manage entries.",
          "- files_search / files_grep / files_info: locate and inspect entries.",
          "",
          "All paths are relative to the Files root unless an absolute path is explicitly accepted. `/` and an empty path mean the Files root, not the OS filesystem root. The shell may update this root from the conversation workspace through MCP Roots. Use tool_schema for exact arguments and confirm before destructive operations.",
        ].join("\n"),
      },
    }],
  };
}
