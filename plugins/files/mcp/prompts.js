export const FILES_PROMPTS = Object.freeze([
  {
    name: "howto",
    title: "Files plugin how-to",
    description: "How to inspect and modify files within the Files root.",
  },
  {
    name: "explore-workflow",
    title: "Explore-then-edit workflow",
    description: "Recommended tool sequence for safely exploring and editing a codebase.",
  },
]);

const HOWTO_TEXT = [
  "Use the Files plugin for bounded filesystem operations.",
  "",
  "Main tools:",
  "- files_list / files_tree: inspect directories. tree supports exclude globs and includeFiles=false for dirs-only.",
  "- files_read: read text files. Use start/end for a line range, head/tail for first/last N lines, lineNumbers=true for line-prefixed output. Binary files are rejected with a helpful error.",
  "- files_write / files_append / files_patch: change text files. files_patch accepts an edits array (with replace_all) and a preview mode that returns the patched content without writing.",
  "- files_mkdir / files_move / files_copy / files_delete / files_touch: manage entries. files_touch creates an empty file or updates timestamps.",
  "- files_search / files_grep / files_info / files_exists: locate and inspect entries. files_grep supports before/after context, ignoreCase, and exclude globs. files_search supports exclude, type filter, and maxDepth.",
  "",
  "Path resolution: empty path = the Files root; `/` and absolute paths resolve to the OS filesystem root; relative paths resolve against the Files root; `../` traversal is allowed (no containment jail). Security is the user/AI provider's responsibility.",
  "",
  "All mutating operations are atomic (write-to-temp-then-rename) so a crash never leaves a partial file. Search/grep results include a `meta.truncated` flag when the result cap is hit.",
].join("\n");

const EXPLORE_WORKFLOW_TEXT = [
  "Recommended workflow for exploring and editing an unfamiliar codebase with the Files plugin:",
  "",
  "1. Map the territory: call files_tree with exclude=[\"node_modules\", \".git\", \"dist\", \"build\"] and includeFiles=false to get a dirs-only overview.",
  "2. Find candidates: call files_search with a glob pattern (e.g. \"*.ts\") and exclude globs to narrow the result set. Use type=\"file\" to skip directories.",
  "3. Inspect before editing: call files_read with start/end to read a specific line range, or lineNumbers=true to make follow-up patches unambiguous. Use files_info for metadata without reading the body.",
  "4. Locate usages: call files_grep with a regex pattern, before=2 and after=2 for context, and exclude=[\"node_modules\", \".git\"] to skip noise. Use ignoreCase=true when the casing is uncertain.",
  "5. Verify existence: call files_exists before reading or patching a path you are not sure about — it never throws on missing paths.",
  "6. Patch safely: call files_patch with preview=true first to see the patched content, then call again with preview=false to apply. Pass an edits array for multiple replacements in one call; use replace_all=true only when you intentionally want every occurrence.",
  "7. Confirm: re-read the patched region with files_read (start/end) to verify the change landed as expected.",
  "",
  "Destructive operations (files_delete, files_move over an existing destination) are irreversible — confirm the path with files_exists or files_info first.",
].join("\n");

export function getFilesPrompt(name) {
  if (name === "howto") {
    return {
      description: FILES_PROMPTS[0].description,
      messages: [{ role: "user", content: { type: "text", text: HOWTO_TEXT } }],
    };
  }
  if (name === "explore-workflow") {
    return {
      description: FILES_PROMPTS[1].description,
      messages: [{ role: "user", content: { type: "text", text: EXPLORE_WORKFLOW_TEXT } }],
    };
  }
  throw new Error(`Unknown prompt: ${name}`);
}
