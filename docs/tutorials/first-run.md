# Install and open NusaShell

This tutorial takes you from a fresh machine to the NusaShell home screen. After this, continue with [connecting a model](connect-a-model.md) or [using an ACP agent](use-an-agent.md).

## What you need

- Linux or Windows.
- A provider account and API key if you plan to use a hosted model, or a local model endpoint such as Ollama. You can also start with an installed ACP agent instead.

## Install a release

On Linux:

```bash
curl -fsSL https://raw.githubusercontent.com/jahrulnr/NusaShell/master/scripts/install.sh | bash
```

On Windows, open PowerShell:

```powershell
irm https://raw.githubusercontent.com/jahrulnr/NusaShell/master/scripts/install.ps1 | iex
```

The installer downloads the latest release, checks its SHA-256 checksum, and installs the NusaShell service for your user. It starts the service after installation.

Open <http://127.0.0.1:10994> in a browser. The first page guides you through adding a model provider or detecting an agent CLI.

## Build from source

If you prefer to build the application yourself, install the Go version listed in `go.mod`, then run:

```bash
make build
./bin/nusashell
```

Open <http://127.0.0.1:10994>. With no runtime configured, NusaShell opens the setup flow.

## First-run checklist

1. Choose **Connect a provider** or **Add an agent**.
2. Complete the provider or agent setup.
3. Start a new conversation and send a short prompt.
4. Use the runtime picker above the composer to change the provider, model, or agent for a turn.

## If the page does not open

- Check that the service is running: `nusashell serve status`.
- If another application uses port `10994`, change the port in Settings → General and restart NusaShell.
- On Linux, check service output with `journalctl --user -u nusashell.service -n 60 --no-pager`.

Next: [connect an API model](connect-a-model.md) or [use an ACP agent](use-an-agent.md).
