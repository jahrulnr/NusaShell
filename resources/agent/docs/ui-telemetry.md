# Telemetry

Usage and cost telemetry: period presets plus provider/model filters drive four stat cards (spend, requests, tokens, cache hit, blended $/1M), a spend-trend chart, a token-mix stack, and model/provider breakdowns.

**How to open:** Click the Telemetry item in the left rail, or open #telemetry.

## Header

Period presets (1h to All), provider and model filter menus, refresh, and an error banner with retry; the period copy summarizes the loaded report.

- **Telemetry sync state** (`#telemetry-sync-state`):
  - Type: status

- **Refresh telemetry** (`#telemetry-refresh`):
  - Type: button

- **Report time range** (`[data-telemetry-range]`):
  - Type: button group
  - Action: Selects the report period: 1h, 3h, 24h, 7d, 30d, or All.

- **Provider filter** (`#telemetry-provider-filter`):
  - Type: button

- **Provider filter menu** (`#telemetry-provider-menu`):
  - Type: menu

- **Model filter** (`#telemetry-model-filter`):
  - Type: button

- **Model filter menu** (`#telemetry-model-menu`):
  - Type: menu

- **Telemetry error banner** (`#telemetry-error`):
  - Type: status

- **Error message** (`#telemetry-error-message`):
  - Type: text

- **Retry telemetry** (`#telemetry-retry`):
  - Type: button

- **Period summary** (`#telemetry-period-copy`):
  - Type: text

## Summary metrics

Headline cards for the selected period.

- **Estimated spend** (`#telemetry-stat-spend`):
  - Section: Telemetry summary
  - Type: text
  - Notes: Aggregate USD spend for the selected period.

- **Requests** (`#telemetry-stat-requests`):
  - Section: Telemetry summary
  - Type: text
  - Notes: Total assistant turns with usage data.

- **Tokens recorded** (`#telemetry-stat-tokens`):
  - Section: Telemetry summary
  - Type: text
  - Notes: Sum of input + output + cache tokens.

- **Cache hit rate** (`#telemetry-stat-cache`):
  - Section: Telemetry summary
  - Type: text
  - Notes: Cache read tokens / (input + cache read) * 100.

- **Blended $/1M** (`telemetry-stat-blended`):
  - Section: Telemetry summary
  - Type: text
  - Notes: total_spend / (total_tokens / 1e6) for the period.

## Spend trend

Estimated spend over time in the selected period.

- **Spend trend chart** (`#telemetry-spend-chart`):
  - Type: canvas

- **Chart state** (`#telemetry-chart-state`):
  - Type: status

## Token mix

Input, output, cache-read, and cache-write token mix with a legend and total.

- **Token total** (`#telemetry-token-total`):
  - Type: text

- **Token mix stack** (`#telemetry-token-stack`):
  - Type: visualization

## By model

Usage concentration by model.

- **Model breakdown** (`#telemetry-model-list`):
  - Type: list

## By provider

Usage routing by provider.

- **Provider breakdown** (`#telemetry-provider-list`):
  - Type: list
