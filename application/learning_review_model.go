package application

import (
	"context"
	"strings"
)

func (r *BackgroundReviewAgent) applyReviewModelOverride(ctx context.Context, adapter ProviderContext, model string) (ProviderContext, string) {
	if r.app == nil || r.app.Settings == nil {
		return adapter, model
	}
	rm := strings.TrimSpace(r.app.Settings.Get().ReviewModel)
	if rm == "" {
		return adapter, model
	}
	rProvider, rBare, rKey, rErr := r.app.resolveModel(rm)
	if rErr != nil || rProvider == nil {
		r.app.log("warn", "learning", "review model %q could not be resolved, falling back to conversation model: %v", rm, rErr)
		r.recordReviewModelResolution(rm, "fallback:conv_model", model)
		return adapter, model
	}
	rAdapter, fErr := r.app.Factory(ctx, rProvider, rKey)
	if fErr != nil {
		r.app.log("warn", "learning", "review model %q adapter build failed, falling back to conversation model: %v", rm, fErr)
		r.recordReviewModelResolution(rm, "fallback:conv_model", model)
		return adapter, model
	}
	rPC := NewProviderContext(rProvider, rAdapter)
	// Record the override only when it actually changes the model. When the
	// override resolves to the same model the conversation already uses, the
	// event carries no information — recording it every run turned the
	// learning log into repeated "ok" noise. Fallbacks are always recorded:
	// they mean the configured override did NOT apply.
	if rBare != model {
		r.app.log("info", "learning", "review using override model %s", rm)
		r.recordReviewModelResolution(rm, "ok", rBare)
	}
	return rPC, rBare
}

// recordReviewModelResolution writes a trajectory event recording whether
// the review model override fell back to the conversation model or applied
// a different one. Same-model overrides are not recorded: they are a no-op
// and would only repeat as "ok" noise in the learning log. This keeps
// override failures visible in the learning log instead of silently
// logging a warning that is easy to miss.
func (r *BackgroundReviewAgent) recordReviewModelResolution(requested, status, resolved string) {
	if r.app == nil || r.app.Trajectory == nil {
		return
	}
	r.app.Trajectory.Record("review_model", map[string]interface{}{
		"requested": requested,
		"status":    status,
		"resolved":  resolved,
	})
}
