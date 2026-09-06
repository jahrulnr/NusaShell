package telemetry

import (
	"sort"
	"time"

	"nusashell/contracts"
	"nusashell/domain"
	clock "nusashell/pkg/time"
)

// handleReport aggregates Usage data from all conversations and
// returns a dashboard payload: summary metrics, daily time-series, and
// per-model / per-provider breakdowns. Cost is estimated from stored
// provider pricing (InputCost/OutputCost/CacheReadCost per 1M tokens).
func (s *Service) handleReport(req contracts.TelemetryReportRequest) (any, *contracts.RPCError) {
	type modelPrice struct {
		providerID    string
		providerName  string
		inputCost     float64
		outputCost    float64
		cacheReadCost float64
	}
	priceLookup := make(map[string]modelPrice)
	providerNameLookup := make(map[string]string)
	if s.providers != nil {
		for _, p := range s.providers.List() {
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
	}

	var cutoff time.Time
	if req.Minutes > 0 {
		cutoff = clock.NewTime().Time().Add(-time.Duration(req.Minutes) * time.Minute)
	}

	bucketSize := chooseBucketSize(req.Minutes)

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
	type bucketKey int64
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
	var totalCacheHitPrompt int
	var earliest, latest time.Time

	var convs []*domain.Conversation
	if s.conversations != nil {
		convs = s.conversations.List()
	}
	for _, conv := range convs {
		for _, msg := range conv.Messages {
			if msg.Role != domain.RoleAssistant || msg.Usage == nil {
				continue
			}
			if msg.Model == "" {
				continue
			}
			if req.ModelID != "" && msg.Model != req.ModelID {
				continue
			}
			t := msg.CreatedAt
			if !cutoff.IsZero() && t.Before(cutoff) {
				continue
			}
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

			provName := providerNameLookup[msgProvID]
			if provName == "" {
				provName = "Unknown"
			}

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

			pa, ok := providers[msgProvID]
			if !ok {
				pa = &providerAgg{providerID: msgProvID, providerName: provName}
				providers[msgProvID] = pa
			}
			pa.spend += spend
			pa.requests++
			pa.tokens += u.InputTokens + u.OutputTokens

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

	now := clock.NewTime().Time()
	var startBucket time.Time
	if !cutoff.IsZero() {
		startBucket = cutoff.Truncate(bucketSize)
	} else if !earliest.IsZero() {
		startBucket = earliest.Truncate(bucketSize)
	}
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

func roundCost(v float64) float64 {
	return float64(int64(v*1e6+0.5)) / 1e6
}

func chooseBucketSize(minutes int) time.Duration {
	switch {
	case minutes <= 0:
		return 24 * time.Hour
	case minutes <= 30:
		return time.Minute
	case minutes <= 60:
		return 2 * time.Minute
	case minutes <= 180:
		return 5 * time.Minute
	case minutes <= 720:
		return 15 * time.Minute
	case minutes <= 2880:
		return time.Hour
	case minutes <= 10080:
		return 6 * time.Hour
	default:
		return 24 * time.Hour
	}
}

func formatBucketLabel(t time.Time, bucketSize time.Duration) string {
	t = t.Truncate(bucketSize)
	if bucketSize >= 24*time.Hour {
		return clock.NewTime(t).Format("2006-01-02")
	}
	return clock.NewTime(t).Format("15:04")
}
