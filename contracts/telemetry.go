package contracts

// Telemetry RPC methods.
const (
	MethodTelemetryReport = "telemetry.report"
)

// TelemetryReportRequest filters the aggregation. Empty/zero values mean
// "no filter" (all-time, all models, all providers).
type TelemetryReportRequest struct {
	ProviderID string `json:"provider_id,omitempty"`
	ModelID    string `json:"model_id,omitempty"`
	// Minutes is the lookback window in minutes (0 = all-time).
	// The UI sends this from the range selector (15m, 30m, 1h, 3h, 1d, etc).
	Minutes int `json:"minutes,omitempty"`
}

// TelemetryReportResult is the full payload returned by telemetry.report.
// It mirrors the OpenRouter Activity dashboard: summary metrics + time-series
// charts broken down by model and token type.
type TelemetryReportResult struct {
	// Summary metrics for the selected period.
	Summary TelemetrySummaryDTO `json:"summary"`
	// Daily series for charts. Each entry is one day bucket.
	Series []TelemetryDayBucketDTO `json:"series"`
	// Top models by spend (descending).
	TopModels []TelemetryModelBreakdownDTO `json:"top_models"`
	// Top providers by spend (descending).
	TopProviders []TelemetryProviderBreakdownDTO `json:"top_providers"`
}

// TelemetrySummaryDTO holds aggregate metrics for the period.
type TelemetrySummaryDTO struct {
	TotalSpend       float64 `json:"total_spend"`
	TotalRequests    int     `json:"total_requests"`
	TotalTokens      int     `json:"total_tokens"`
	InputTokens      int     `json:"input_tokens"`
	OutputTokens     int     `json:"output_tokens"`
	CacheReadTokens  int     `json:"cache_read_tokens"`
	CacheWriteTokens int     `json:"cache_write_tokens"`
	CacheHitPercent  float64 `json:"cache_hit_percent"`
	// PeriodStart and PeriodEnd are the actual date range covered
	// (RFC3339). Empty when no data.
	PeriodStart string `json:"period_start,omitempty"`
	PeriodEnd   string `json:"period_end,omitempty"`
}

// TelemetryDayBucketDTO is one day of aggregated usage. Used for all
// time-series charts (usage by model, token breakdown, caching, requests).
type TelemetryDayBucketDTO struct {
	Date         string  `json:"date"` // YYYY-MM-DD
	Spend        float64 `json:"spend"`
	Requests     int     `json:"requests"`
	InputTokens  int     `json:"input_tokens"`
	OutputTokens int     `json:"output_tokens"`
	CacheRead    int     `json:"cache_read"`
	CacheWrite   int     `json:"cache_write"`
	// PerModel breaks down this day's spend and requests by model.
	PerModel []TelemetryModelDayDTO `json:"per_model,omitempty"`
}

// TelemetryModelDayDTO is one model's contribution to a single day.
type TelemetryModelDayDTO struct {
	ModelID  string  `json:"model_id"`
	Spend    float64 `json:"spend"`
	Requests int     `json:"requests"`
	Tokens   int     `json:"tokens"`
}

// TelemetryModelBreakdownDTO is the aggregate for one model across the period.
type TelemetryModelBreakdownDTO struct {
	ModelID      string  `json:"model_id"`
	ProviderID   string  `json:"provider_id,omitempty"`
	ProviderName string  `json:"provider_name,omitempty"`
	Spend        float64 `json:"spend"`
	Requests     int     `json:"requests"`
	Tokens       int     `json:"tokens"`
	InputTokens  int     `json:"input_tokens"`
	OutputTokens int     `json:"output_tokens"`
	CacheRead    int     `json:"cache_read"`
}

// TelemetryProviderBreakdownDTO is the aggregate for one provider.
type TelemetryProviderBreakdownDTO struct {
	ProviderID   string  `json:"provider_id"`
	ProviderName string  `json:"provider_name"`
	Spend        float64 `json:"spend"`
	Requests     int     `json:"requests"`
	Tokens       int     `json:"tokens"`
}
