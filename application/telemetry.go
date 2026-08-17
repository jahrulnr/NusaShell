package application

import (
	"sort"
	"strings"
	"time"

	"nusashell/contracts"
	"nusashell/domain"
)

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
	for _, p := range a.Providers.List() {
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
	if req.Days > 0 {
		cutoff = time.Now().AddDate(0, 0, -req.Days)
	}

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
	type dayKey struct {
		year  int
		month int
		day   int
	}
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
	days := make(map[dayKey]*dayAgg)

	var totalSpend float64
	var totalRequests int
	var totalInput, totalOutput, totalCacheRead, totalCacheWrite int
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
			// Apply provider filter (resolve from price lookup).
			mp, _ := priceLookup[msg.Model]
			if req.ProviderID != "" && mp.providerID != req.ProviderID {
				continue
			}

			u := msg.Usage
			// Estimate cost: (input/1M * inputCost) + (output/1M * outputCost) + (cacheRead/1M * cacheReadCost).
			spend := float64(u.InputTokens)/1e6*mp.inputCost +
				float64(u.OutputTokens)/1e6*mp.outputCost +
				float64(u.CacheRead)/1e6*mp.cacheReadCost

			totalSpend += spend
			totalRequests++
			totalInput += u.InputTokens
			totalOutput += u.OutputTokens
			totalCacheRead += u.CacheRead
			totalCacheWrite += u.CacheWrite

			if earliest.IsZero() || t.Before(earliest) {
				earliest = t
			}
			if t.After(latest) {
				latest = t
			}

			// Model aggregation.
			ma, ok := models[msg.Model]
			if !ok {
				ma = &modelAgg{modelID: msg.Model, providerID: mp.providerID, providerName: mp.providerName}
				models[msg.Model] = ma
			}
			ma.spend += spend
			ma.requests++
			ma.tokens += u.InputTokens + u.OutputTokens
			ma.inputTokens += u.InputTokens
			ma.outputTokens += u.OutputTokens
			ma.cacheRead += u.CacheRead

			// Provider aggregation.
			pa, ok := providers[mp.providerID]
			if !ok {
				pa = &providerAgg{providerID: mp.providerID, providerName: mp.providerName}
				providers[mp.providerID] = pa
			}
			pa.spend += spend
			pa.requests++
			pa.tokens += u.InputTokens + u.OutputTokens

			// Day bucket.
			dk := dayKey{t.Year(), int(t.Month()), t.Day()}
			da, ok := days[dk]
			if !ok {
				da = &dayAgg{
					date:     t.Format("2006-01-02"),
					perModel: make(map[string]*dayModelAgg),
				}
				days[dk] = da
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

	// Build summary.
	cacheHitPercent := 0.0
	totalPromptTokens := totalInput + totalCacheRead
	if totalPromptTokens > 0 {
		cacheHitPercent = float64(totalCacheRead) / float64(totalPromptTokens) * 100
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
		summary.PeriodStart = earliest.UTC().Format(time.RFC3339)
		summary.PeriodEnd = latest.UTC().Format(time.RFC3339)
	}

	// Build daily series (sorted by date).
	series := make([]contracts.TelemetryDayBucketDTO, 0, len(days))
	dayKeys := make([]dayKey, 0, len(days))
	for k := range days {
		dayKeys = append(dayKeys, k)
	}
	sort.Slice(dayKeys, func(i, j int) bool {
		a, b := dayKeys[i], dayKeys[j]
		if a.year != b.year {
			return a.year < b.year
		}
		if a.month != b.month {
			return a.month < b.month
		}
		return a.day < b.day
	})
	for _, dk := range dayKeys {
		da := days[dk]
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
		// Sort per-model by spend descending for stable chart rendering.
		sort.Slice(bucket.PerModel, func(i, j int) bool {
			return bucket.PerModel[i].Spend > bucket.PerModel[j].Spend
		})
		series = append(series, bucket)
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

// (unused import guard: strings is used by future filter helpers)
var _ = strings.TrimSpace
