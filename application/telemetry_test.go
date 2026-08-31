package application

import (
	"testing"
	"time"

	"nusashell/contracts"
	"nusashell/domain"
	clock "nusashell/pkg/time"
)

func TestHandleTelemetryReportAggregatesUsage(t *testing.T) {
	now := clock.NewTime().Time()
	convStore := &fakeConvStore{convs: map[string]*domain.Conversation{
		"conv_1": {
			ID: "conv_1",
			Messages: []domain.Message{
				{
					Role:       domain.RoleAssistant,
					Model:      "gpt-5.6-luna",
					ProviderID: "prov_1",
					CreatedAt:  now,
					Usage:      &domain.Usage{InputTokens: 1000, OutputTokens: 500, CacheRead: 200, CacheWrite: 100},
				},
				{
					Role:       domain.RoleAssistant,
					Model:      "claude-opus-5",
					ProviderID: "prov_1",
					CreatedAt:  now.Add(-2 * 24 * time.Hour),
					// After Option A normalization, InputTokens is UNCACHED
					// (total 2000 - cached 400 = 1600), matching the
					// convention used by Anthropic. CacheRead is the cached
					// subset.
					Usage: &domain.Usage{InputTokens: 1600, OutputTokens: 800, CacheRead: 400},
				},
				// User messages are skipped.
				{Role: domain.RoleUser, Model: "", CreatedAt: now},
				// Assistant without usage is skipped.
				{Role: domain.RoleAssistant, Model: "gpt-5.6-luna", CreatedAt: now},
			},
		},
	}}
	provStore := &fakeProviderStore{items: map[string]*domain.Provider{
		"prov_1": {
			ID:   "prov_1",
			Name: "OpenRouter",
			Models: []domain.Model{
				{ID: "gpt-5.6-luna", InputCost: 1.0, OutputCost: 3.0, CacheReadCost: 0.1},
				{ID: "claude-opus-5", InputCost: 5.0, OutputCost: 15.0, CacheReadCost: 0.5},
			},
		},
	}}
	app := &App{Conversations: convStore, Providers: provStore, Logs: &fakeLogStore{}, Bus: NewBus()}

	resp, rpcErr := app.handleTelemetryReport(contracts.TelemetryReportRequest{Minutes: 0})
	if rpcErr != nil {
		t.Fatalf("unexpected rpc error: %v", rpcErr)
	}
	result, ok := resp.(contracts.TelemetryReportResult)
	if !ok {
		t.Fatalf("resp = %#v", resp)
	}

	// 2 assistant messages with usage.
	if result.Summary.TotalRequests != 2 {
		t.Fatalf("total_requests = %d, want 2", result.Summary.TotalRequests)
	}
	// Input (uncached): 1000 + 1600 = 2600
	if result.Summary.InputTokens != 2600 {
		t.Fatalf("input_tokens = %d, want 2600", result.Summary.InputTokens)
	}
	// Output: 500 + 800 = 1300
	if result.Summary.OutputTokens != 1300 {
		t.Fatalf("output_tokens = %d, want 1300", result.Summary.OutputTokens)
	}
	// CacheRead: 200 + 400 = 600
	if result.Summary.CacheReadTokens != 600 {
		t.Fatalf("cache_read = %d, want 600", result.Summary.CacheReadTokens)
	}
	// CacheWrite: 100 + 0 = 100
	if result.Summary.CacheWriteTokens != 100 {
		t.Fatalf("cache_write = %d, want 100", result.Summary.CacheWriteTokens)
	}
	// Cache hit % — after Option A, all providers use InputTokens + CacheRead + CacheWrite:
	//   gpt-5.6-luna: 1000 + 200 + 100 = 1300 → 200 hit
	//   claude-opus-5: 1600 + 400 + 0 = 2000 → 400 hit
	//   total = 3300, hit = 600 → 18.18%
	if result.Summary.CacheHitPercent < 18.1 || result.Summary.CacheHitPercent > 18.2 {
		t.Fatalf("cache_hit_percent = %.2f, want ~18.18", result.Summary.CacheHitPercent)
	}
	// Spend (after Option A, InputTokens is uncached so no double-charge):
	//        gpt = 1000/1M*1 + 500/1M*3 + 200/1M*0.1 = 0.001 + 0.0015 + 0.00002 = 0.00252
	//        claude = 1600/1M*5 + 800/1M*15 + 400/1M*0.5 = 0.008 + 0.012 + 0.0002 = 0.0202
	// Total ≈ 0.02272
	if result.Summary.TotalSpend < 0.022 || result.Summary.TotalSpend > 0.024 {
		t.Fatalf("total_spend = %.6f, want ~0.0227", result.Summary.TotalSpend)
	}

	// Top models: 2 entries, sorted by spend descending (claude > gpt).
	if len(result.TopModels) != 2 {
		t.Fatalf("top_models len = %d, want 2", len(result.TopModels))
	}
	if result.TopModels[0].ModelID != "claude-opus-5" {
		t.Fatalf("top model[0] = %s, want claude-opus-5", result.TopModels[0].ModelID)
	}

	// Top providers: 1 entry (both models under prov_1).
	if len(result.TopProviders) != 1 {
		t.Fatalf("top_providers len = %d, want 1", len(result.TopProviders))
	}
	if result.TopProviders[0].ProviderName != "OpenRouter" {
		t.Fatalf("top provider name = %s, want OpenRouter", result.TopProviders[0].ProviderName)
	}

	// Series: daily buckets spanning from 2 days ago to today (filled).
	// With all-time (minutes=0), bucketSize=1d, so we get 3 buckets:
	// 2-days-ago, 1-day-ago (empty), today.
	if len(result.Series) < 2 {
		t.Fatalf("series len = %d, want >= 2", len(result.Series))
	}
	// First and last should have data; middle may be empty (filled).
	if result.Series[0].Requests == 0 {
		t.Fatalf("first bucket should have data, got 0 requests")
	}
	if result.Series[len(result.Series)-1].Requests == 0 {
		t.Fatalf("last bucket should have data, got 0 requests")
	}
}

func TestHandleTelemetryReportDaysFilter(t *testing.T) {
	now := time.Now().UTC()
	convStore := &fakeConvStore{convs: map[string]*domain.Conversation{
		"conv_1": {
			ID: "conv_1",
			Messages: []domain.Message{
				{
					Role:      domain.RoleAssistant,
					Model:     "gpt-5.6-luna",
					CreatedAt: now,
					Usage:     &domain.Usage{InputTokens: 1000, OutputTokens: 500},
				},
				{
					Role:      domain.RoleAssistant,
					Model:     "gpt-5.6-luna",
					CreatedAt: now.Add(-10 * 24 * time.Hour),
					Usage:     &domain.Usage{InputTokens: 2000, OutputTokens: 800},
				},
			},
		},
	}}
	provStore := &fakeProviderStore{items: map[string]*domain.Provider{
		"prov_1": {ID: "prov_1", Name: "Test", Models: []domain.Model{
			{ID: "gpt-5.6-luna", InputCost: 1.0, OutputCost: 3.0},
		}},
	}}
	app := &App{Conversations: convStore, Providers: provStore, Logs: &fakeLogStore{}, Bus: NewBus()}

	// 7-day window: only the recent message should be included.
	resp, _ := app.handleTelemetryReport(contracts.TelemetryReportRequest{Minutes: 7 * 24 * 60})
	result := resp.(contracts.TelemetryReportResult)
	if result.Summary.TotalRequests != 1 {
		t.Fatalf("total_requests = %d, want 1 (7-day filter)", result.Summary.TotalRequests)
	}
	if result.Summary.InputTokens != 1000 {
		t.Fatalf("input_tokens = %d, want 1000 (7-day filter)", result.Summary.InputTokens)
	}

	// All-time: both messages.
	resp, _ = app.handleTelemetryReport(contracts.TelemetryReportRequest{Minutes: 0})
	result = resp.(contracts.TelemetryReportResult)
	if result.Summary.TotalRequests != 2 {
		t.Fatalf("total_requests = %d, want 2 (all-time)", result.Summary.TotalRequests)
	}
}

// TestHandleTelemetryReportCacheHitRateStyles pins the per-message prompt
// total used for the cache hit rate. After Option A normalization,
// InputTokens is the UNCACHED input for all providers (OpenAI adapters
// subtract cached_tokens at the handler boundary; Anthropic reports
// input_tokens as uncached already). The full prompt is therefore
// InputTokens + CacheRead + CacheWrite uniformly; messages with no cache
// info (e.g. providers that never populated the fields) must not drag the
// denominator down.
func TestHandleTelemetryReportCacheHitRateStyles(t *testing.T) {
	now := time.Now().UTC()
	convStore := &fakeConvStore{convs: map[string]*domain.Conversation{
		"conv_1": {
			ID: "conv_1",
			Messages: []domain.Message{
				// OpenAI Responses style (Luna), post-Option A normalization:
				// InputTokens is UNCACHED (total 1000 - cached 920 = 80).
				{
					Role:       domain.RoleAssistant,
					Model:      "gpt-5.6-luna",
					ProviderID: "prov_1",
					CreatedAt:  now,
					Usage:      &domain.Usage{InputTokens: 80, OutputTokens: 300, CacheRead: 920},
				},
				// Anthropic style: cache fields separate from input.
				{
					Role:       domain.RoleAssistant,
					Model:      "claude-opus-5",
					ProviderID: "prov_1",
					CreatedAt:  now,
					Usage:      &domain.Usage{InputTokens: 200, OutputTokens: 100, CacheRead: 800, CacheWrite: 100},
				},
				// No cache info at all: must not be counted in the denominator.
				{
					Role:       domain.RoleAssistant,
					Model:      "deepseek/deepseek-v4",
					ProviderID: "prov_1",
					CreatedAt:  now,
					Usage:      &domain.Usage{InputTokens: 500, OutputTokens: 100},
				},
			},
		},
	}}
	provStore := &fakeProviderStore{items: map[string]*domain.Provider{
		"prov_1": {
			ID:   "prov_1",
			Name: "OpenRouter",
			Models: []domain.Model{
				{ID: "gpt-5.6-luna", InputCost: 1.0, OutputCost: 3.0, CacheReadCost: 0.1},
				{ID: "claude-opus-5", InputCost: 5.0, OutputCost: 15.0, CacheReadCost: 0.5},
				{ID: "deepseek/deepseek-v4", InputCost: 1.0, OutputCost: 2.0},
			},
		},
	}}
	app := &App{Conversations: convStore, Providers: provStore, Logs: &fakeLogStore{}, Bus: NewBus()}

	resp, rpcErr := app.handleTelemetryReport(contracts.TelemetryReportRequest{Minutes: 0})
	if rpcErr != nil {
		t.Fatalf("unexpected rpc error: %v", rpcErr)
	}
	result := resp.(contracts.TelemetryReportResult)

	// Hit tokens: 920 + 800 = 1720.
	// Prompt totals (post-Option A, all providers: InputTokens + CacheRead + CacheWrite):
	//   Luna 80 + 920 = 1000, Claude 200 + 100 + 800 = 1100 → 2100.
	// deepseek message (no cache info) is excluded from the hit-rate math.
	if result.Summary.CacheReadTokens != 1720 {
		t.Fatalf("cache_read = %d, want 1720", result.Summary.CacheReadTokens)
	}
	if result.Summary.CacheHitPercent < 81.8 || result.Summary.CacheHitPercent > 82.0 {
		t.Fatalf("cache_hit_percent = %.2f, want ~81.9 (1720/2100)", result.Summary.CacheHitPercent)
	}
}

func TestHandleTelemetryReportEmptyStore(t *testing.T) {
	convStore := &fakeConvStore{convs: map[string]*domain.Conversation{}}
	provStore := &fakeProviderStore{items: map[string]*domain.Provider{}}
	app := &App{Conversations: convStore, Providers: provStore, Logs: &fakeLogStore{}, Bus: NewBus()}

	resp, rpcErr := app.handleTelemetryReport(contracts.TelemetryReportRequest{})
	if rpcErr != nil {
		t.Fatalf("unexpected rpc error: %v", rpcErr)
	}
	result := resp.(contracts.TelemetryReportResult)
	if result.Summary.TotalRequests != 0 {
		t.Fatalf("total_requests = %d, want 0", result.Summary.TotalRequests)
	}
	if len(result.TopModels) != 0 {
		t.Fatalf("top_models len = %d, want 0", len(result.TopModels))
	}
	if len(result.Series) != 0 {
		t.Fatalf("series len = %d, want 0", len(result.Series))
	}
}

func TestHandleTelemetryReportLegacyMessageUnknownProvider(t *testing.T) {
	now := time.Now().UTC()
	convStore := &fakeConvStore{convs: map[string]*domain.Conversation{
		"conv_1": {
			ID: "conv_1",
			Messages: []domain.Message{
				// Legacy message: no ProviderID, model not in any provider catalog.
				{
					Role:      domain.RoleAssistant,
					Model:     "some-unknown-model",
					CreatedAt: now,
					Usage:     &domain.Usage{InputTokens: 1000, OutputTokens: 500},
				},
			},
		},
	}}
	provStore := &fakeProviderStore{items: map[string]*domain.Provider{}}
	app := &App{Conversations: convStore, Providers: provStore, Logs: &fakeLogStore{}, Bus: NewBus()}

	resp, _ := app.handleTelemetryReport(contracts.TelemetryReportRequest{})
	result := resp.(contracts.TelemetryReportResult)
	if len(result.TopProviders) != 1 {
		t.Fatalf("top_providers len = %d, want 1", len(result.TopProviders))
	}
	if result.TopProviders[0].ProviderName != "Unknown" {
		t.Fatalf("provider name = %s, want Unknown", result.TopProviders[0].ProviderName)
	}
}

func TestChooseBucketSize(t *testing.T) {
	cases := []struct {
		minutes int
		want    time.Duration
	}{
		{0, 24 * time.Hour},      // all-time
		{15, time.Minute},        // 15m → 1m
		{30, time.Minute},        // 30m → 1m
		{60, 2 * time.Minute},    // 1h → 2m
		{180, 5 * time.Minute},   // 3h → 5m
		{720, 15 * time.Minute},  // 12h → 15m
		{1440, time.Hour},        // 1d → 1h
		{2880, time.Hour},        // 2d → 1h
		{10080, 6 * time.Hour},   // 1w → 6h
		{43200, 24 * time.Hour},  // 1mo → 1d
		{525600, 24 * time.Hour}, // 1y → 1d
	}
	for _, c := range cases {
		got := chooseBucketSize(c.minutes)
		if got != c.want {
			t.Fatalf("chooseBucketSize(%d) = %v, want %v", c.minutes, got, c.want)
		}
	}
}

func TestFormatBucketLabel(t *testing.T) {
	// Daily bucket → YYYY-MM-DD
	daily := chooseBucketSize(0)
	dailyAt := clock.NewTime(time.Date(2026, 8, 17, 14, 30, 0, 0, time.UTC)).Time()
	got := formatBucketLabel(dailyAt, daily)
	if got != "2026-08-17" {
		t.Fatalf("daily label = %q, want 2026-08-17", got)
	}
	// 5-min bucket → HH:MM
	fiveMin := chooseBucketSize(180)
	fiveMinAt := clock.NewTime(time.Date(2026, 8, 17, 14, 32, 0, 0, time.UTC)).Time()
	got = formatBucketLabel(fiveMinAt, fiveMin)
	// 14:32 truncated to 5-min bucket = 14:30 in machine time.
	wantFiveMin := clock.NewTime(fiveMinAt.Truncate(fiveMin)).Format("15:04")
	if got != wantFiveMin {
		t.Fatalf("5m label = %q, want %s", got, wantFiveMin)
	}
}
