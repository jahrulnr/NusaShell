export const TERMINAL_PROMPTS = Object.freeze([
  {
    name: "howto",
    title: "Terminal plugin how-to",
    description: "How to run commands and manage interactive terminal sessions.",
  },
]);

export function getTerminalPrompt(name) {
  if (name !== "howto") throw new Error(`Unknown prompt: ${name}`);
  return {
    description: TERMINAL_PROMPTS[0].description,
    messages: [{
      role: "user",
      content: {
        type: "text",
        text: [
          "Use the Terminal plugin to run commands or maintain an interactive PTY session.",
          "",
          "Main tools:",
          "- terminal_exec: run one command and return bounded output.",
          "- terminal_open: open an interactive session.",
          "- terminal_write / terminal_read: send input and read buffered output.",
          "- terminal_resize: change PTY dimensions.",
          "- terminal_close / terminal_list: close or inspect sessions.",
          "",
          "Pass an absolute cwd when a specific directory matters; do not assume the conversation workspace is the process cwd. Commands execute with the user's shell permissions and can change files or access external systems. Use tool_schema for exact arguments and confirm destructive or irreversible commands before running them.",
        ].join("\n"),
      },
    }],
  };
}
