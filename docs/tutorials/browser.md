# Use the built-in browser

NusaShell provides a browser session that conversations can use through browser tools. Each conversation has its own tab, and the web interface can show that tab while the agent is working.

## Set up the browser

1. Open **Settings → Tools → Browser**.
2. Check which Chrome or Chromium executable NusaShell found.
3. If it is missing, use the browser install action or follow the [browser CLI guide](../cli-guide.md#the-threads-browser-nusashell-browser).
4. Start a conversation and open its **Browser** side pane.

The browser tool is available to model conversations through NusaShell's built-in MCP server. ACP agents receive enabled MCP servers when a new agent session starts.

## Follow browser activity

The Browser pane shows the conversation's page. Use it to watch navigation and page changes while a browser-enabled runtime works. Browser recordings and annotations can be attached to the conversation when those controls are available.

## Troubleshooting

- **Browser unavailable:** check the executable path and browser status in **Settings → Tools**.
- **The agent does not use browser tools:** check that the browser MCP server is enabled for the runtime; ACP agents may need a fresh session after MCP settings change.
- **The page fails to load:** try opening the URL in the browser pane first and inspect the browser console for page errors.

See [adapters and tools](../adapters.md) for the built-in tool catalog.
