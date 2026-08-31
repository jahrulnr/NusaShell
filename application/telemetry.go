package application

import (
	"encoding/json"
	"sort"
	"strings"
	"time"

	"nusashell/contracts"
	"nusashell/domain"
	clock "nusashell/pkg/time"
)

// dispatchTelemetry routes telemetry.* RPC methods to their handlers.
// Called by App.Dispatch for any method whose first segment is "telemetry".
func (a *App) dispatchTelemetry(method string, payload json.RawMessage) (any, *contracts.RPCError) {
	switch method {
	case contracts.MethodTelemetryReport:
		var req contracts.TelemetryReportRequest
		if rpcErr := contracts.DecodePayload(payload, &req); rpcErr != nil {
			return nil, rpcErr
		}
		return a.handleTelemetryReport(req)
	default:
		return nil, &contracts.RPCError{Code: contracts.CodeValidation, Message: "unknown telemetry method: " + method}
	}
}

// handleTelemetryReport aggregates Usage data from all conversations and
// returns a dashboard payload: summary metrics, daily time-series, and
// per-model / per-provider breakdowns. Cost is estimated from stored
// provider pricing (InputCost/OutputCost/CacheReadCost per 1M tokens).
func (a *App) handleTelemetryReport(req contracts.TelemetryReportRequest) (any, *contracts.RPCError) {
	// Build a model-pricing lookup from the provider store.
	type modelPrice struct {
		providerID    string
		providerName  string
		inputCost     float64 // USD per 1M
		outputCost    float64
		cacheReadCost float64
	}
	priceLookup := make(map[string]modelPrice)
	providerNameLookup := make(map[string]string)
	for _, p := range a.Providers.List() {
		providerNameLookup[p.ID] = p.Name
		for _, m := range p.Models {
			priceLookup[m.ID] = modelPrice{
				providerID:    p.ID,
				providerName:  p.Name,
				inputCost:     m.InputCost,
				outputCost:    m.OutputCost,
				cacheReadCost: m.CacheReadCost,
			}
		}
	}

	// Determine the cutoff date for the lookback window.
	var cutoff time.Time
	if req.Minutes > 0 {
		cutoff = clock.NewTime().Time().Add(-time.Duration(req.Minutes) * time.Minute)
	}

	// Determine bucket size based on the lookback window. Shorter windows
	// get finer buckets so the chart shows meaningful granularity.
	bucketSize := chooseBucketSize(req.Minutes)

	// Aggregate.
	type modelAgg struct {
		modelID      string
		providerID   string
		providerName string
		spend        float64
		requests     int
		tokens       int
		inputTokens  int
		outputTokens int
		cacheRead    int
	}
	type providerAgg struct {
		providerID   string
		providerName string
		spend        float64
		requests     int
		tokens       int
	}
	type bucketKey int64 // unix seconds truncated to bucket boundary
	type dayModelAgg struct {
		modelID  string
		spend    float64
		requests int
		tokens   int
	}
	type dayAgg struct {
		date         string
		spend        float64
		requests     int
		inputTokens  int
		outputTokens int
		cacheRead    int
		cacheWrite   int
		perModel     map[string]*dayModelAgg
	}

	models := make(map[string]*modelAgg)
	providers := make(map[string]*providerAgg)
	buckets := make(map[bucketKey]*dayAgg)

	var totalSpend float64
	var totalRequests int
	var totalInput, totalOutput, totalCacheRead, totalCacheWrite int
	// totalCacheHitPrompt is the sum of per-message prompt totals that carry
	// cache information. Messages without cache fields are excluded so they
	// do not drag the hit rate denominator down (e.g. providers that never
	// populate cache tokens).
	var totalCacheHitPrompt int
	var earliest, latest time.Time

	for _, conv := range a.Conversations.List() {
		for _, msg := range conv.Messages {
			if msg.Role != domain.RoleAssistant || msg.Usage == nil {
				continue
			}
			if msg.Model == "" {
				continue
			}
			// Apply model filter.
			if req.ModelID != "" && msg.Model != req.ModelID {
				continue
			}
			// Apply date filter.
			t := msg.CreatedAt
			if !cutoff.IsZero() && t.Before(cutoff) {
				continue
			}
			// Apply provider filter. Use the message's ProviderID when
			// available; fall back to priceLookup for legacy messages.
			msgProvID := msg.ProviderID
			if msgProvID == "" {
				if mp, ok := priceLookup[msg.Model]; ok {
					msgProvID = mp.providerID
				} else {
					msgProvID = "unknown"
				}
			}
			if req.ProviderID != "" && msgProvID != req.ProviderID {
				continue
			}

			u := msg.Usage
			// Estimate cost from model pricing (input/output/cache per 1M).
			mp := priceLookup[msg.Model]
			spend := float64(u.InputTokens)/1e6*mp.inputCost +
				float64(u.OutputTokens)/1e6*mp.outputCost +
				float64(u.CacheRead)/1e6*mp.cacheReadCost

			totalSpend += spend
			totalRequests++
			totalInput += u.InputTokens
			totalOutput += u.OutputTokens
			totalCacheRead += u.CacheRead
			totalCacheWrite += u.CacheWrite

			// Prompt total for the cache hit rate. After Option A
			// normalization, InputTokens is the UNCACHED input for all
			// providers (OpenAI adapters subtract cached_tokens from
			// prompt_tokens; Anthropic reports input_tokens as uncached
			// already). The full prompt is therefore InputTokens +
			// CacheRead + CacheWrite for any provider. Messages with no
			// cache info are excluded from the hit-rate math entirely.
			switch {
			case u.CacheWrite > 0, u.CacheRead > 0:
				totalCacheHitPrompt += u.InputTokens + u.CacheWrite + u.CacheRead
			}

			if earliest.IsZero() || t.Before(earliest) {
				earliest = t
			}
			if t.After(latest) {
				latest = t
			}

			// Resolve provider name for this message's provider.
			provName := providerNameLookup[msgProvID]
			if provName == "" {
				provName = "Unknown"
			}

			// Model aggregation.
			ma, ok := models[msg.Model]
			if !ok {
				ma = &modelAgg{modelID: msg.Model, providerID: msgProvID, providerName: provName}
				models[msg.Model] = ma
			}
			ma.spend += spend
			ma.requests++
			ma.tokens += u.InputTokens + u.OutputTokens
			ma.inputTokens += u.InputTokens
			ma.outputTokens += u.OutputTokens
			ma.cacheRead += u.CacheRead

			// Provider aggregation.
			pa, ok := providers[msgProvID]
			if !ok {
				pa = &providerAgg{providerID: msgProvID, providerName: provName}
				providers[msgProvID] = pa
			}
			pa.spend += spend
			pa.requests++
			pa.tokens += u.InputTokens + u.OutputTokens

			// Bucket by dynamic size.
			bucketStart := t.Truncate(bucketSize)
			bk := bucketKey(clock.NewTime(bucketStart).Epoch())
			da, ok := buckets[bk]
			if !ok {
				da = &dayAgg{
					date:     formatBucketLabel(bucketStart, bucketSize),
					perModel: make(map[string]*dayModelAgg),
				}
				buckets[bk] = da
			}
			da.spend += spend
			da.requests++
			da.inputTokens += u.InputTokens
			da.outputTokens += u.OutputTokens
			da.cacheRead += u.CacheRead
			da.cacheWrite += u.CacheWrite
			dm, ok := da.perModel[msg.Model]
			if !ok {
				dm = &dayModelAgg{modelID: msg.Model}
				da.perModel[msg.Model] = dm
			}
			dm.spend += spend
			dm.requests++
			dm.tokens += u.InputTokens + u.OutputTokens
		}
	}

	// Build summary. The hit rate uses the per-message prompt totals that
	// carry cache information (totalCacheHitPrompt), not input+cacheRead:
	// OpenAI-style input_tokens already includes cached tokens, so adding
	// CacheRead back double counts the hits and halves the reported rate.
	cacheHitPercent := 0.0
	if totalCacheHitPrompt > 0 {
		cacheHitPercent = float64(totalCacheRead) / float64(totalCacheHitPrompt) * 100
	}
	summary := contracts.TelemetrySummaryDTO{
		TotalSpend:       roundCost(totalSpend),
		TotalRequests:    totalRequests,
		TotalTokens:      totalInput + totalOutput + totalCacheRead + totalCacheWrite,
		InputTokens:      totalInput,
		OutputTokens:     totalOutput,
		CacheReadTokens:  totalCacheRead,
		CacheWriteTokens: totalCacheWrite,
		CacheHitPercent:  roundCost(cacheHitPercent),
	}
	if !earliest.IsZero() {
		summary.PeriodStart = clock.NewTime(earliest).RFC3339()
		summary.PeriodEnd = clock.NewTime(latest).RFC3339()
	}

	// Build series. Fill empty buckets between cutoff and now so the chart
	// always shows the full selected range, not just active periods.
	now := clock.NewTime().Time()
	var startBucket time.Time
	if !cutoff.IsZero() {
		startBucket = cutoff.Truncate(bucketSize)
	} else if !earliest.IsZero() {
		startBucket = earliest.Truncate(bucketSize)
	}
	// End bucket is always now (the chart should show up to current time,
	// not just the last usage timestamp).
	endBucket := now.Truncate(bucketSize)

	series := make([]contracts.TelemetryDayBucketDTO, 0)
	if !startBucket.IsZero() {
		for bt := startBucket; !bt.After(endBucket); bt = bt.Add(bucketSize) {
			bk := bucketKey(clock.NewTime(bt).Epoch())
			da := buckets[bk]
			bucket := contracts.TelemetryDayBucketDTO{
				Date: formatBucketLabel(bt, bucketSize),
			}
			if da != nil {
				bucket.Spend = roundCost(da.spend)
				bucket.Requests = da.requests
				bucket.InputTokens = da.inputTokens
				bucket.OutputTokens = da.outputTokens
				bucket.CacheRead = da.cacheRead
				bucket.CacheWrite = da.cacheWrite
				for _, dm := range da.perModel {
					bucket.PerModel = append(bucket.PerModel, contracts.TelemetryModelDayDTO{
						ModelID:  dm.modelID,
						Spend:    roundCost(dm.spend),
						Requests: dm.requests,
						Tokens:   dm.tokens,
					})
				}
				sort.Slice(bucket.PerModel, func(i, j int) bool {
					return bucket.PerModel[i].Spend > bucket.PerModel[j].Spend
				})
			}
			series = append(series, bucket)
		}
	} else {
		// No data at all — return empty series.
		bucketKeys := make([]bucketKey, 0, len(buckets))
		for k := range buckets {
			bucketKeys = append(bucketKeys, k)
		}
		sort.Slice(bucketKeys, func(i, j int) bool {
			return bucketKeys[i] < bucketKeys[j]
		})
		for _, bk := range bucketKeys {
			da := buckets[bk]
			bucket := contracts.TelemetryDayBucketDTO{
				Date:         da.date,
				Spend:        roundCost(da.spend),
				Requests:     da.requests,
				InputTokens:  da.inputTokens,
				OutputTokens: da.outputTokens,
				CacheRead:    da.cacheRead,
				CacheWrite:   da.cacheWrite,
			}
			for _, dm := range da.perModel {
				bucket.PerModel = append(bucket.PerModel, contracts.TelemetryModelDayDTO{
					ModelID:  dm.modelID,
					Spend:    roundCost(dm.spend),
					Requests: dm.requests,
					Tokens:   dm.tokens,
				})
			}
			sort.Slice(bucket.PerModel, func(i, j int) bool {
				return bucket.PerModel[i].Spend > bucket.PerModel[j].Spend
			})
			series = append(series, bucket)
		}
	}

	// Build top models (sorted by spend descending).
	topModels := make([]contracts.TelemetryModelBreakdownDTO, 0, len(models))
	for _, ma := range models {
		topModels = append(topModels, contracts.TelemetryModelBreakdownDTO{
			ModelID:      ma.modelID,
			ProviderID:   ma.providerID,
			ProviderName: ma.providerName,
			Spend:        roundCost(ma.spend),
			Requests:     ma.requests,
			Tokens:       ma.tokens,
			InputTokens:  ma.inputTokens,
			OutputTokens: ma.outputTokens,
			CacheRead:    ma.cacheRead,
		})
	}
	sort.Slice(topModels, func(i, j int) bool {
		return topModels[i].Spend > topModels[j].Spend
	})

	// Build top providers.
	topProviders := make([]contracts.TelemetryProviderBreakdownDTO, 0, len(providers))
	for _, pa := range providers {
		topProviders = append(topProviders, contracts.TelemetryProviderBreakdownDTO{
			ProviderID:   pa.providerID,
			ProviderName: pa.providerName,
			Spend:        roundCost(pa.spend),
			Requests:     pa.requests,
			Tokens:       pa.tokens,
		})
	}
	sort.Slice(topProviders, func(i, j int) bool {
		return topProviders[i].Spend > topProviders[j].Spend
	})

	return contracts.TelemetryReportResult{
		Summary:      summary,
		Series:       series,
		TopModels:    topModels,
		TopProviders: topProviders,
	}, nil
}

// roundCost rounds a float to 6 decimal places to avoid floating-point
// noise in JSON output (e.g. 0.0000001 → 0).
func roundCost(v float64) float64 {
	return float64(int64(v*1e6+0.5)) / 1e6
}

// chooseBucketSize picks a time bucket duration based on the lookback window.
// Shorter windows get finer buckets so charts show meaningful granularity:
//   - ≤30m  → 1m buckets
//   - ≤1h   → 2m
//   - ≤3h   → 5m
//   - ≤12h  → 15m
//   - ≤2d   → 1h
//   - ≤1w   → 6h
//   - ≤1mo  → 1d
//   - ≤1y   → 1d
//   - all-time → 1d
func chooseBucketSize(minutes int) time.Duration {
	switch {
	case minutes <= 0:
		return 24 * time.Hour // all-time: daily
	case minutes <= 30:
		return time.Minute
	case minutes <= 60:
		return 2 * time.Minute
	case minutes <= 180:
		return 5 * time.Minute
	case minutes <= 720: // 12h
		return 15 * time.Minute
	case minutes <= 2880: // 2d
		return time.Hour
	case minutes <= 10080: // 1w
		return 6 * time.Hour
	default: // 1mo, 1y
		return 24 * time.Hour
	}
}

// formatBucketLabel renders a bucket start time as a human-readable label
// appropriate for the bucket size. Sub-day buckets show HH:MM, daily
// buckets show YYYY-MM-DD.
func formatBucketLabel(t time.Time, bucketSize time.Duration) string {
	// Truncate to bucket boundary (already done by caller, but be safe).
	t = t.Truncate(bucketSize)
	if bucketSize >= 24*time.Hour {
		return clock.NewTime(t).Format("2006-01-02")
	}
	// Sub-day: show HH:MM.
	return clock.NewTime(t).Format("15:04")
}

// (unused import guard: strings is used by future filter helpers)
var _ = strings.TrimSpace
