package telemetry

import (
	"testing"
	"time"

	"nusashell/contracts"
	"nusashell/domain"
	clock "nusashell/pkg/time"
)

type fakeConvStore struct {
	convs map[string]*domain.Conversation
}

func (f *fakeConvStore) List() []*domain.Conversation {
	out := make([]*domain.Conversation, 0, len(f.convs))
	for _, c := range f.convs {
		out = append(out, c)
	}
	return out
}

type fakeProviderStore struct {
	items map[string]*domain.Provider
}

func (f *fakeProviderStore) List() []*domain.Provider {
	out := make([]*domain.Provider, 0, len(f.items))
	for _, p := range f.items {
		out = append(out, p)
	}
	return out
}

func testService(convs *fakeConvStore, provs *fakeProviderStore) *Service {
	return New(Deps{Conversations: convs, Providers: provs})
}

func TestHandleReportAggregatesUsage(t *testing.T) {
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
					Usage:      &domain.Usage{InputTokens: 1600, OutputTokens: 800, CacheRead: 400},
				},
				{Role: domain.RoleUser, Model: "", CreatedAt: now},
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
	svc := testService(convStore, provStore)

	resp, rpcErr := svc.handleReport(contracts.TelemetryReportRequest{Minutes: 0})
	if rpcErr != nil {
		t.Fatalf("unexpected rpc error: %v", rpcErr)
	}
	result, ok := resp.(contracts.TelemetryReportResult)
	if !ok {
		t.Fatalf("resp = %#v", resp)
	}

	if result.Summary.TotalRequests != 2 {
		t.Fatalf("total_requests = %d, want 2", result.Summary.TotalRequests)
	}
	if result.Summary.InputTokens != 2600 {
		t.Fatalf("input_tokens = %d, want 2600", result.Summary.InputTokens)
	}
	if result.Summary.OutputTokens != 1300 {
		t.Fatalf("output_tokens = %d, want 1300", result.Summary.OutputTokens)
	}
	if result.Summary.CacheReadTokens != 600 {
		t.Fatalf("cache_read = %d, want 600", result.Summary.CacheReadTokens)
	}
	if result.Summary.CacheWriteTokens != 100 {
		t.Fatalf("cache_write = %d, want 100", result.Summary.CacheWriteTokens)
	}
	if result.Summary.CacheHitPercent < 18.1 || result.Summary.CacheHitPercent > 18.2 {
		t.Fatalf("cache_hit_percent = %.2f, want ~18.18", result.Summary.CacheHitPercent)
	}
	if result.Summary.TotalSpend < 0.022 || result.Summary.TotalSpend > 0.024 {
		t.Fatalf("total_spend = %.6f, want ~0.0227", result.Summary.TotalSpend)
	}

	if len(result.TopModels) != 2 {
		t.Fatalf("top_models len = %d, want 2", len(result.TopModels))
	}
	if result.TopModels[0].ModelID != "claude-opus-5" {
		t.Fatalf("top model[0] = %s, want claude-opus-5", result.TopModels[0].ModelID)
	}

	if len(result.TopProviders) != 1 {
		t.Fatalf("top_providers len = %d, want 1", len(result.TopProviders))
	}
	if result.TopProviders[0].ProviderName != "OpenRouter" {
		t.Fatalf("top provider name = %s, want OpenRouter", result.TopProviders[0].ProviderName)
	}

	if len(result.Series) < 2 {
		t.Fatalf("series len = %d, want >= 2", len(result.Series))
	}
	if result.Series[0].Requests == 0 {
		t.Fatalf("first bucket should have data, got 0 requests")
	}
	if result.Series[len(result.Series)-1].Requests == 0 {
		t.Fatalf("last bucket should have data, got 0 requests")
	}
}

func TestHandleReportDaysFilter(t *testing.T) {
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
	svc := testService(convStore, provStore)

	resp, _ := svc.handleReport(contracts.TelemetryReportRequest{Minutes: 7 * 24 * 60})
	result := resp.(contracts.TelemetryReportResult)
	if result.Summary.TotalRequests != 1 {
		t.Fatalf("total_requests = %d, want 1 (7-day filter)", result.Summary.TotalRequests)
	}
	if result.Summary.InputTokens != 1000 {
		t.Fatalf("input_tokens = %d, want 1000 (7-day filter)", result.Summary.InputTokens)
	}

	resp, _ = svc.handleReport(contracts.TelemetryReportRequest{Minutes: 0})
	result = resp.(contracts.TelemetryReportResult)
	if result.Summary.TotalRequests != 2 {
		t.Fatalf("total_requests = %d, want 2 (all-time)", result.Summary.TotalRequests)
	}
}

func TestHandleReportCacheHitRateStyles(t *testing.T) {
	now := time.Now().UTC()
	convStore := &fakeConvStore{convs: map[string]*domain.Conversation{
		"conv_1": {
			ID: "conv_1",
			Messages: []domain.Message{
				{
					Role:       domain.RoleAssistant,
					Model:      "gpt-5.6-luna",
					ProviderID: "prov_1",
					CreatedAt:  now,
					Usage:      &domain.Usage{InputTokens: 80, OutputTokens: 300, CacheRead: 920},
				},
				{
					Role:       domain.RoleAssistant,
					Model:      "claude-opus-5",
					ProviderID: "prov_1",
					CreatedAt:  now,
					Usage:      &domain.Usage{InputTokens: 200, OutputTokens: 100, CacheRead: 800, CacheWrite: 100},
				},
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
	svc := testService(convStore, provStore)

	resp, rpcErr := svc.handleReport(contracts.TelemetryReportRequest{Minutes: 0})
	if rpcErr != nil {
		t.Fatalf("unexpected rpc error: %v", rpcErr)
	}
	result := resp.(contracts.TelemetryReportResult)

	if result.Summary.CacheReadTokens != 1720 {
		t.Fatalf("cache_read = %d, want 1720", result.Summary.CacheReadTokens)
	}
	if result.Summary.CacheHitPercent < 81.8 || result.Summary.CacheHitPercent > 82.0 {
		t.Fatalf("cache_hit_percent = %.2f, want ~81.9 (1720/2100)", result.Summary.CacheHitPercent)
	}
}

func TestHandleReportEmptyStore(t *testing.T) {
	convStore := &fakeConvStore{convs: map[string]*domain.Conversation{}}
	provStore := &fakeProviderStore{items: map[string]*domain.Provider{}}
	svc := testService(convStore, provStore)

	resp, rpcErr := svc.handleReport(contracts.TelemetryReportRequest{})
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

func TestHandleReportLegacyMessageUnknownProvider(t *testing.T) {
	now := time.Now().UTC()
	convStore := &fakeConvStore{convs: map[string]*domain.Conversation{
		"conv_1": {
			ID: "conv_1",
			Messages: []domain.Message{
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
	svc := testService(convStore, provStore)

	resp, _ := svc.handleReport(contracts.TelemetryReportRequest{})
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
		{0, 24 * time.Hour},
		{15, time.Minute},
		{30, time.Minute},
		{60, 2 * time.Minute},
		{180, 5 * time.Minute},
		{720, 15 * time.Minute},
		{1440, time.Hour},
		{2880, time.Hour},
		{10080, 6 * time.Hour},
		{43200, 24 * time.Hour},
		{525600, 24 * time.Hour},
	}
	for _, c := range cases {
		got := chooseBucketSize(c.minutes)
		if got != c.want {
			t.Fatalf("chooseBucketSize(%d) = %v, want %v", c.minutes, got, c.want)
		}
	}
}

func TestFormatBucketLabel(t *testing.T) {
	daily := chooseBucketSize(0)
	dailyAt := clock.NewTime(time.Date(2026, 8, 17, 14, 30, 0, 0, time.UTC)).Time()
	got := formatBucketLabel(dailyAt, daily)
	if got != "2026-08-17" {
		t.Fatalf("daily label = %q, want 2026-08-17", got)
	}
	fiveMin := chooseBucketSize(180)
	fiveMinAt := clock.NewTime(time.Date(2026, 8, 17, 14, 32, 0, 0, time.UTC)).Time()
	got = formatBucketLabel(fiveMinAt, fiveMin)
	wantFiveMin := clock.NewTime(fiveMinAt.Truncate(fiveMin)).Format("15:04")
	if got != wantFiveMin {
		t.Fatalf("5m label = %q, want %s", got, wantFiveMin)
	}
}
