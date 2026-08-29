package application

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"nusashell/application/service/modeloverrides"
	"nusashell/domain"
)

// model_override is a LOCAL tool available only to the background review
// agent. It lets the agent correct a model's catalog metadata (vision,
// context window, etc.) for a specific provider+model pair. Corrections are
// stored in the manual override registry and applied at resolve time AFTER
// learned 400-adaptations, so they always win and survive catalog re-imports.
//
// It is NOT registered in Toolbox and NOT dispatched via Toolbox.Execute —
// runReviewLoop executes it directly, the same way it handles
// review_transcript. This keeps the capability scoped to the review agent
// and lets it touch the modelOverridesCache without exposing a general RPC.
//
// Ops:
//   - set:    create/merge an override (at least one field required)
//   - remove: delete the override for provider+model
//   - list:   return all current overrides (read-only, for the agent's context)

const modelOverrideToolName = "model_override"

// modelOverrideToolDef is the tool definition sent to the review LLM.
var modelOverrideToolDef = ToolDef{
	Name: modelOverrideToolName,
	Description: "Correct a model's catalog metadata for a specific provider+model pair. " +
		"Use when the catalog is wrong (e.g. a model marked text-only actually supports images, " +
		"or the context window is misreported) and a learned 400-adaptation is not the right fix. " +
		"Overrides are assertive and bidirectional: they win over both the catalog and learned rules, " +
		"and survive catalog re-imports. Set only the fields you are correcting; omitted fields are left alone. " +
		"Always provide a short reason. Use op=list first to see existing overrides.",
	InputSchema: map[string]any{
		"type": "object",
		"properties": map[string]any{
			"op": map[string]any{
				"type":        "string",
				"enum":        []string{"set", "remove", "list"},
				"description": "set = create/merge an override; remove = delete it; list = show all current overrides.",
			},
			"provider": map[string]any{
				"type":        "string",
				"description": "Provider ID (e.g. \"tokenrouter\"). Required for set and remove.",
			},
			"model": map[string]any{
				"type":        "string",
				"description": "Model ID as it appears on the provider (e.g. \"deepseek/deepseek-v4-flash\"). Required for set and remove.",
			},
			"vision":            map[string]any{"type": "boolean", "description": "Override image-input support."},
			"audio":             map[string]any{"type": "boolean", "description": "Override audio-input support."},
			"video":             map[string]any{"type": "boolean", "description": "Override video-input support."},
			"document":          map[string]any{"type": "boolean", "description": "Override PDF/document-input support."},
			"reasoning":         map[string]any{"type": "boolean", "description": "Override reasoning/thinking-mode support."},
			"tool_call":         map[string]any{"type": "boolean", "description": "Override tool/function-calling support."},
			"structured_output": map[string]any{"type": "boolean", "description": "Override structured/JSON-output support."},
			"context":           map[string]any{"type": "integer", "description": "Override the context window in tokens."},
			"max_output":        map[string]any{"type": "integer", "description": "Override the max completion tokens."},
			"reason": map[string]any{
				"type":        "string",
				"description": "Short justification for the correction, stored for audit.",
			},
		},
		"required": []string{"op"},
	},
}

// modelOverrideToolArgs is the decoded argument shape for model_override.
type modelOverrideToolArgs struct {
	Op               string `json:"op"`
	Provider         string `json:"provider"`
	Model            string `json:"model"`
	Vision           *bool  `json:"vision"`
	Audio            *bool  `json:"audio"`
	Video            *bool  `json:"video"`
	Document         *bool  `json:"document"`
	Reasoning        *bool  `json:"reasoning"`
	ToolCall         *bool  `json:"tool_call"`
	StructuredOutput *bool  `json:"structured_output"`
	Context          *int   `json:"context"`
	MaxOutput        *int   `json:"max_output"`
	Reason           string `json:"reason"`
}

// executeModelOverride runs a model_override call against the App's override
// cache. It returns the tool-result content string, a mutation snippet for
// the Learning log (empty when the call did not mutate the registry), and an
// error. It is the local handler invoked by runReviewLoop — NOT dispatched
// via Toolbox.Execute.
func (r *BackgroundReviewAgent) executeModelOverride(argsJSON []byte) (output, snippet string, err error) {
	var args modelOverrideToolArgs
	if err := json.Unmarshal(argsJSON, &args); err != nil {
		return fmt.Sprintf("error: invalid model_override arguments: %v", err), "", err
	}
	cache := r.app.modelOverrides
	if cache == nil {
		return "error: model override store is not configured", "", fmt.Errorf("model override store not configured")
	}

	switch strings.ToLower(strings.TrimSpace(args.Op)) {
	case "list":
		return r.formatOverrideList(cache), "", nil

	case "remove":
		if strings.TrimSpace(args.Provider) == "" || strings.TrimSpace(args.Model) == "" {
			return "error: remove requires provider and model", "", fmt.Errorf("remove missing provider/model")
		}
		if cache.Remove(args.Provider, args.Model) {
			return fmt.Sprintf("removed override for %s/%s", args.Provider, args.Model),
				fmt.Sprintf("removed %s/%s", args.Provider, args.Model), nil
		}
		return fmt.Sprintf("no override found for %s/%s (nothing removed)", args.Provider, args.Model), "", nil

	case "set":
		o := &domain.ModelOverride{
			Provider:         args.Provider,
			Model:            args.Model,
			Vision:           args.Vision,
			Audio:            args.Audio,
			Video:            args.Video,
			Document:         args.Document,
			Reasoning:        args.Reasoning,
			ToolCall:         args.ToolCall,
			StructuredOutput: args.StructuredOutput,
			Context:          args.Context,
			MaxOutput:        args.MaxOutput,
			Source:           "review-agent",
			Reason:           strings.TrimSpace(args.Reason),
		}
		if err := cache.Set(o); err != nil {
			return fmt.Sprintf("error: %v", err), "", err
		}
		stored := cache.Get(args.Provider, args.Model)
		return fmt.Sprintf("saved override for %s/%s: %s", args.Provider, args.Model, describeOverride(stored)),
			fmt.Sprintf("%s/%s %s", stored.Provider, stored.Model, describeOverride(stored)), nil

	default:
		return fmt.Sprintf("error: unknown op %q (use set, remove, or list)", args.Op), "", fmt.Errorf("unknown op %q", args.Op)
	}
}

// formatOverrideList renders all current overrides as a compact, readable
// table for the review agent's context.
func (r *BackgroundReviewAgent) formatOverrideList(cache *modeloverrides.Cache) string {
	list := cache.List()
	if len(list) == 0 {
		return "no model overrides currently set"
	}
	sort.Slice(list, func(i, j int) bool {
		if list[i].Provider != list[j].Provider {
			return list[i].Provider < list[j].Provider
		}
		return list[i].Model < list[j].Model
	})
	var sb strings.Builder
	fmt.Fprintf(&sb, "%d model override(s):\n", len(list))
	for _, o := range list {
		fmt.Fprintf(&sb, "- %s/%s: %s", o.Provider, o.Model, describeOverride(o))
		if o.Reason != "" {
			fmt.Fprintf(&sb, " (%s)", o.Reason)
		}
		sb.WriteString("\n")
	}
	return sb.String()
}

// describeOverride renders the non-nil fields of an override as a short
// "key=value" summary for logs and tool output.
func describeOverride(o *domain.ModelOverride) string {
	if o == nil {
		return "(none)"
	}
	var parts []string
	addBool := func(name string, v *bool) {
		if v != nil {
			parts = append(parts, fmt.Sprintf("%s=%v", name, *v))
		}
	}
	addInt := func(name string, v *int) {
		if v != nil {
			parts = append(parts, fmt.Sprintf("%s=%d", name, *v))
		}
	}
	addBool("vision", o.Vision)
	addBool("audio", o.Audio)
	addBool("video", o.Video)
	addBool("document", o.Document)
	addBool("reasoning", o.Reasoning)
	addBool("tool_call", o.ToolCall)
	addBool("structured_output", o.StructuredOutput)
	addInt("context", o.Context)
	addInt("max_output", o.MaxOutput)
	if len(parts) == 0 {
		return "(no fields)"
	}
	return strings.Join(parts, " ")
}
