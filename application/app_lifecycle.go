package application

import (
	"context"
	"encoding/json"
	"time"

	"nusashell/contracts"
	"nusashell/domain"
)

const mcpAutostartTimeout = 20 * time.Second

// StartAutoUpdateLoop periodically checks catalog updates and upgrades
// plugins with AutoUpdate enabled. Interval defaults to 6h. Safe no-op when
// installer or store are unavailable.
func (a *App) StartAutoUpdateLoop(ctx context.Context, interval time.Duration) {
	if a.Plugins == nil || a.PluginInstaller == nil {
		return
	}
	if interval <= 0 {
		interval = 6 * time.Hour
	}
	a.goSafe("autoupdate", func() {
		a.runAutoUpdateOnce(ctx)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				a.runAutoUpdateOnce(ctx)
			}
		}
	})
	a.log("info", "plugin", "auto-update loop started (interval=%s)", interval)
}

// StartMCPAutostart connects every plugin whose manifest has mcp.autostart.
// It runs synchronously so automations and the agent toolbox see those
// tools before the first FireDue tick. A failed connect is logged and
// skipped; the process still starts.
func (a *App) StartMCPAutostart(ctx context.Context) {
	if a.Plugins == nil || a.MCPToolbox == nil {
		return
	}
	list, err := a.Plugins.List()
	if err != nil {
		a.log("warn", "plugin", "autostart list: %v", err)
		return
	}
	for _, p := range list {
		if p == nil || !p.Manifest.MCP.Autostart {
			continue
		}
		if err := a.connectPluginMCP(ctx, p); err != nil {
			a.log("warn", "plugin", "autostart connect %s: %v", p.Manifest.ID, err)
			continue
		}
		a.log("info", "plugin", "autostart connected: %s", p.Manifest.ID)
	}
}

func (a *App) connectPluginMCP(ctx context.Context, p *domain.Plugin) error {
	if a.MCPToolbox == nil || p == nil {
		return nil
	}
	connectCtx, cancel := context.WithTimeout(ctx, mcpAutostartTimeout)
	defer cancel()
	_, err := a.MCPToolbox.Connect(connectCtx, p)
	return err
}

func (a *App) runAutoUpdateOnce(ctx context.Context) {
	installed, err := a.Plugins.List()
	if err != nil {
		a.log("warn", "autoupdate", "list plugins: %v", err)
		return
	}
	var targets []*domain.Plugin
	for _, p := range installed {
		if p.Manifest.AutoUpdate {
			targets = append(targets, p)
		}
	}
	if len(targets) == 0 {
		return
	}
	checkCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	updates, err := a.PluginInstaller.CheckUpdates(checkCtx, installed)
	if err != nil {
		a.log("warn", "autoupdate", "check updates: %v", err)
		return
	}
	byID := map[string]domain.PluginCatalogEntry{}
	for _, u := range updates {
		byID[u.PluginID] = u
	}
	for _, p := range targets {
		entry, ok := byID[p.Manifest.ID]
		if !ok {
			continue
		}
		updateCtx, cancelUpd := context.WithTimeout(ctx, 5*time.Minute)
		updated, err := a.PluginInstaller.Update(updateCtx, entry.ID)
		cancelUpd()
		if err != nil {
			a.log("warn", "autoupdate", "update %s: %v", p.Manifest.ID, err)
			continue
		}
		a.MCPToolbox.Drop("plugin:" + updated.Manifest.ID)
		a.log("info", "autoupdate", "auto-updated %s → v%s", updated.Manifest.Name, updated.Manifest.Version)
	}
}

// StartLifecycle starts the lifecycle (decay/prune) loop and the
// compaction-triggered learning review subscriber. Safe to call once at
// server startup. No-op if no lifecycle manager is configured.
func (a *App) StartLifecycle() {
	if a.lifecycle == nil {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	a.lifecycleCancel = cancel
	a.goSafe("learning", func() { a.lifecycle.Run(ctx) })
	a.log("info", "learning", "lifecycle manager started (decay=%s prune=%s)", domain.DefaultLifecycleConfig().DecayInterval, domain.DefaultLifecycleConfig().PruneInterval)

	// Subscribe to compaction events: when a conversation is compacted,
	// flush the learning review for that conversation. Compaction is a
	// natural checkpoint — the full context is being summarized, so
	// extracting learnings at the same time is free context-wise.
	if a.ReviewAgent != nil && a.Bus != nil {
		a.goSafe("learning", func() { a.subscribeCompactionReview(ctx) })
	}
}

// subscribeCompactionReview listens for compaction events and triggers
// a learning review for the compacted conversation. Exits when ctx is
// cancelled.
func (a *App) subscribeCompactionReview(ctx context.Context) {
	_, events, unsubscribe := a.Bus.Subscribe()
	defer unsubscribe()
	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-events:
			if !ok {
				return
			}
			if ev.Type != contracts.EventCompacted {
				continue
			}
			var ce contracts.CompactedEvent
			if err := json.Unmarshal(ev.Payload, &ce); err != nil {
				continue
			}
			a.flushLearningReview(ce.ConversationID, "compaction")
		}
	}
}

// CloseLifecycle stops the background decay/prune loop. Safe to call
// at server shutdown. No-op if not started.
func (a *App) CloseLifecycle() {
	if a.lifecycleCancel != nil {
		a.lifecycleCancel()
		a.lifecycleCancel = nil
	}
	// Persist turn counters so review thresholds survive restarts.
	a.saveTurnCounters()
}
