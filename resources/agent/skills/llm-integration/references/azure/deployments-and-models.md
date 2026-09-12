# Azure OpenAI - Deployments, models, and the Foundry catalog

Azure OpenAI does **not** expose a public `GET /models` that lists every model
by id the way public OpenAI does. Model identity is mediated by **deployments**
(control plane), and the Foundry catalog is the discovery surface. Read this
before assuming `model` = model id.

Verified 2026-09-11 (Azure OpenAI reference; control-plane GA `2025-06-01`,
preview `2025-07-01-preview`; Responses how-to supported-models list updated
2026-08-18; LiteLLM `llms/azure/`).

## Endpoint + auth (control plane)

Deployments are managed via the **control plane** (Azure Resource Manager), not
the data plane:

```bash
# List deployments on a resource (control plane)
GET https://management.azure.com/subscriptions/{sub}/resourceGroups/{rg}/providers/Microsoft.CognitiveServices/accounts/{account}/deployments?api-version=2025-06-01
Authorization: Bearer $ARM_TOKEN

# Data-plane list of deployments on a resource
GET https://{resource}.openai.azure.com/openai/deployments?api-version=2024-10-21
api-key: $AZURE_OPENAI_API_KEY
```

- Control-plane auth = Entra ID ARM token (RBAC), not the `api-key` data-plane
  key. Use `az` CLI or `DefaultAzureCredential`.
- The data-plane `GET /openai/deployments` returns your deployments (name,
  model, version, capacity), not the full catalog.

## Request contract (create a deployment)

```json
{
  "sku": {"name": "standard", "capacity": 10},
  "properties": {
    "model": {"format": "OpenAI", "name": "gpt-5", "version": "2025-08-07"}
  }
}
```

- `sku.name`: `standard` (token-based, pay-as-you-go) or `DataProvisionedManaged`
  (PTU - provisioned throughput units, reserved capacity).
- `properties.model.name` + `version` bind the deployment to a specific model
  snapshot. The deployment name is your own alias.

## Response contract (deployment)

```json
{
  "id": ".../deployments/my-deploy",
  "name": "my-deploy",
  "properties": {"model": {"name": "gpt-5", "version": "2025-08-07"}, "provisioningState": "succeeded"},
  "sku": {"name": "standard", "capacity": 10}
}
```

## Workflow

1. Discover available models in the Foundry catalog (portal or
   `https://ai.azure.com`) or the Responses how-to supported-models list.
2. Create a deployment (control plane) binding a model + version to a name.
3. In data-plane calls, use the **deployment name** in the URL path (date style)
   or as `model` (v1 style).
4. Pin the model `version` in production; use `-latest` aliases only in dev.

## Deployment != model semantics

| Concept | What it is | Where it appears |
|---|---|---|
| Model | the underlying OpenAI model + version (e.g. `gpt-5` `2025-08-07`) | deployment `properties.model` |
| Deployment | your named alias bound to a model, with quota/SKU | URL path `/deployments/{deployment}/...` or `model` field |
| Foundry catalog | the discoverable list of models available to deploy | portal / `ai.azure.com` |

- One deployment = one model version. To upgrade, create a new deployment or
  update the deployment's model version (control plane).
- The same model can have multiple deployments with different SKUs (e.g. one
  standard, one PTU) for different traffic classes.
- `gpt-5-chat*` models are excluded from the reasoning path but still need
  `max_completion_tokens` (LiteLLM `requires_max_completion_tokens`).

## PTU vs standard

| SKU | Billing | When |
|---|---|---|
| `standard` | per-token, pay-as-you-go, shared capacity | variable load, getting started |
| `DataProvisionedManaged` (PTU) | reserved throughput units, fixed cost | predictable high volume, latency SLAs |

- PTU quota is separate from standard token quota; 429 on a PTU deployment means
  throughput exhausted, not token quota - scale PTU or overflow to standard.
- Standard deployments have per-deployment TPM/RPM limits; PTU deployments have
  throughput-unit limits. Both return `Retry-After` on 429.

## Edge cases

- **Model availability is regional**: not every model is deployable in every
  region; the Responses API itself is region-restricted (see `responses.md`).
- **Version retirement**: model versions are retired on a schedule; pinning a
  version without a retirement plan causes outages. Monitor Azure notifications.
- **Foundry non-OpenAI models**: Foundry hosts models from other vendors on
  different endpoints (`*.services.ai.azure.com`); this tree covers only the
  OpenAI-compatible endpoints on `*.openai.azure.com`.
- **No public global model list**: unlike `GET /v1/models` on public OpenAI,
  there is no single data-plane endpoint returning all deployable models - use
  the catalog/portal or the control-plane deployment list.

## Error handling

Control-plane errors use ARM error envelopes (`error.code`, `error.message`,
`error.details`); data-plane errors use the OpenAI-shaped envelope in
`errors.md`. Deployment creation can be async (`provisioningState` polling) -
retry only transient ARM failures (429/5xx), not validation 400s.
