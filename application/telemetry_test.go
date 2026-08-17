package application

import (
	"testing"
	"time"

	"nusashell/contracts"
	"nusashell/domain"
)

func TestHandleTelemetryReportAggregatesUsage(t *testing.T) {
	now := time.Now().UTC()
	convStore := &fakeConvStore{convs: map[string]*domain.Conversation{
		"conv_1": {
			ID: "conv_1",
			Messages: []domain.Message{
				{
					Role:      domain.RoleAssistant,
					Model:     "gpt-5.6-luna",
					CreatedAt: now,
					Usage:     &domain.Usage{InputTokens: 1000, OutputTokens: 500, CacheRead: 200, CacheWrite: 100},
				},
				{
					Role:      domain.RoleAssistant,
					Model:     "claude-opus-5",
					CreatedAt: now.Add(-2 * 24 * time.Hour),
					Usage:     &domain.Usage{InputTokens: 2000, OutputTokens: 800, CacheRead: 400},
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
	// Input: 1000 + 2000 = 3000
	if result.Summary.InputTokens != 3000 {
		t.Fatalf("input_tokens = %d, want 3000", result.Summary.InputTokens)
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
	// Cache hit % = 600 / (3000 + 600) * 100 = 16.67%
	if result.Summary.CacheHitPercent < 16.6 || result.Summary.CacheHitPercent > 16.7 {
		t.Fatalf("cache_hit_percent = %.2f, want ~16.67", result.Summary.CacheHitPercent)
	}
	// Spend: gpt = 1000/1M*1 + 500/1M*3 + 200/1M*0.1 = 0.001 + 0.0015 + 0.00002 = 0.00252
	//        claude = 2000/1M*5 + 800/1M*15 + 400/1M*0.5 = 0.01 + 0.012 + 0.0002 = 0.0222
	// Total ≈ 0.02472
	if result.Summary.TotalSpend < 0.024 || result.Summary.TotalSpend > 0.026 {
		t.Fatalf("total_spend = %.6f, want ~0.0247", result.Summary.TotalSpend)
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
	got := formatBucketLabel(time.Date(2026, 8, 17, 14, 30, 0, 0, time.UTC), daily)
	if got != "2026-08-17" {
		t.Fatalf("daily label = %q, want 2026-08-17", got)
	}
	// 5-min bucket → HH:MM
	fiveMin := chooseBucketSize(180)
	got = formatBucketLabel(time.Date(2026, 8, 17, 14, 32, 0, 0, time.UTC), fiveMin)
	// 14:32 truncated to 5-min bucket = 14:30
	if got != "14:30" {
		t.Fatalf("5m label = %q, want 14:30", got)
	}
}
