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
          "- exec: run one command and return bounded output.",
          "- open: open an interactive session.",
          "- write / read: send input and read buffered output.",
          "- resize: change PTY dimensions.",
          "- close / list: close or inspect sessions.",
          "",
          "Pass an absolute cwd when a specific directory matters; do not assume the conversation workspace is the process cwd. Commands execute with the user's shell permissions and can change files or access external systems. Use tool_schema for exact arguments and confirm destructive or irreversible commands before running them.",
        ].join("\n"),
      },
    }],
  };
}
