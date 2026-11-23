# Use an ACP agent

NusaShell can run an external coding agent that implements the [Agent Client Protocol (ACP)](https://agentclientprotocol.com/). The agent process runs on the machine where NusaShell is installed; NusaShell provides the conversation, project folders, transcript, and approval interface.

## Before you start

Install an ACP-compatible agent CLI on the same machine and complete its own sign-in. NusaShell can detect supported agent commands on `PATH`; you can also register a command manually.

## Add an agent

1. On first run, choose **Add an agent**, or open **Settings → Agents**.
2. Run agent detection, then select the CLI you installed. If it is not detected, add its executable command and arguments manually.
3. Set the agent's working directory if it needs a default folder.
4. Save the agent and select it from the runtime picker in a conversation.

An agent reports its available models and session options through ACP. Sign-in and model access are handled by the agent's own CLI.

## Handle approval requests

When the agent asks permission to perform an action, its request appears in the conversation. Review the action and approve or reject it there. The agent's configured approval policy is managed in **Settings → Agents**.

## Troubleshooting

- **Agent not detected:** confirm the command works in a terminal opened as the same user that runs NusaShell. A background service may have a different `PATH` from your interactive shell.
- **Agent needs sign-in:** run that agent's own login command locally, then retry its model list in NusaShell.
- **No model appears:** check the agent's CLI configuration and its ACP support.

Next: [choose between models and agents](connect-a-model.md#choose-a-model), or give a conversation a [project and working folders](projects-and-folders.md).
