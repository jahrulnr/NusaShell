# Telemetry

Aggregate token usage, spend, and caching across all conversations. Mirrors the OpenRouter Activity dashboard layout with summary metric cards and time-series charts.

**How to open:** Click the Telemetry item in the left sidebar.

## Header

Range selector (15m / 30m / 1h / 3h default / 1d / 2d / 1w / 1mo / 1y / All) and Refresh button.

- **Time range selector** (`#telemetry-range`):
  - Section: Telemetry header
  - Type: select
  - Notes: Lookback window: 15m, 30m, 1h, 3h (default), 1d, 2d, 1w, 1mo, 1y, or all time.

- **Refresh button** (`#telemetry-refresh-btn`):
  - Section: Telemetry header
  - Type: button
  - Notes: Reloads telemetry data from backend.

## Summary metrics

Four metric cards: Total spend (USD), Requests, Total tokens, Cache hit percent.

- **Total spend metric** (`#tm-spend`):
  - Section: Telemetry summary
  - Type: text
  - Notes: Aggregate USD spend for the selected period.

- **Total requests metric** (`#tm-requests`):
  - Section: Telemetry summary
  - Type: text
  - Notes: Total assistant turns with usage data.

- **Total tokens metric** (`#tm-tokens`):
  - Section: Telemetry summary
  - Type: text
  - Notes: Sum of input + output + cache tokens.

- **Cache hit percent** (`#tm-cache`):
  - Section: Telemetry summary
  - Type: text
  - Notes: Cache read tokens / (input + cache read) * 100.

## Usage by model

Stacked bar chart showing daily spend broken down by model.

- **Usage by model chart** (`#chart-usage-model`):
  - Section: Telemetry charts
  - Type: canvas
  - Notes: Stacked bar chart of daily spend per model.

## Top models

Ranked table of models by spend, with request and token counts.

- **Top models table** (`#tm-top-models`):
  - Section: Telemetry charts
  - Type: container
  - Notes: Ranked list of models by spend.

## Token breakdown

Stacked bar chart showing daily prompt, completion, and cache-read tokens.

- **Token breakdown chart** (`#chart-token-breakdown`):
  - Section: Telemetry charts
  - Type: canvas
  - Notes: Stacked bar chart of daily prompt/completion/cache tokens.

## Prompt token caching

Stacked bar chart showing daily cached vs uncached prompt tokens.

- **Prompt caching chart** (`#chart-caching`):
  - Section: Telemetry charts
  - Type: canvas
  - Notes: Stacked bar chart of cached vs uncached prompt tokens.

## Request volume by model

Stacked bar chart showing daily request counts broken down by model.

- **Request volume chart** (`#chart-requests`):
  - Section: Telemetry charts
  - Type: canvas
  - Notes: Stacked bar chart of daily requests per model.

## Top providers

Ranked table of providers by spend, with request and token counts.

- **Top providers table** (`#tm-top-providers`):
  - Section: Telemetry charts
  - Type: container
  - Notes: Ranked list of providers by spend.
