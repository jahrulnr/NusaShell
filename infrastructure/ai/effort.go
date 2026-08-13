package ai

import "strings"

// validEfforts is the canonical set of reasoning effort levels understood by
// the application, mirroring NusaShell Electron's ReasoningEffort type.
// "auto" is the sentinel for "omit on the wire" and is never advertised.
var validEfforts = map[string]bool{
	"none":    true,
	"minimal": true,
	"low":     true,
	"medium":  true,
	"high":    true,
	"xhigh":   true,
	"max":     true,
}

// effortAliases maps provider-specific spellings to the canonical set.
var effortAliases = map[string]string{
	"off":      "none",
	"min":      "minimal",
	"med":      "medium",
	"x-high":   "xhigh",
	"extra":    "xhigh",
	"maximum":  "max",
	"default":  "auto",
}

// normalizeEffort canonicalizes a single effort level. Unknown values map to
// "auto" (omit on the wire).
func normalizeEffort(raw string) string {
	s := strings.ToLower(strings.TrimSpace(raw))
	if s == "" {
		return "auto"
	}
	if alias, ok := effortAliases[s]; ok {
		s = alias
	}
	if s == "auto" {
		return "auto"
	}
	if validEfforts[s] {
		return s
	}
	return "auto"
}

// normalizeEfforts deduplicates a list of effort levels, dropping "auto" and
// unknown values. Returns nil when the input is empty or contains no valid
// levels — callers treat nil as "reasoning effort not supported".
func normalizeEfforts(raws []string) []string {
	if len(raws) == 0 {
		return nil
	}
	seen := map[string]bool{}
	var out []string
	for _, raw := range raws {
		eff := normalizeEffort(raw)
		if eff == "auto" || seen[eff] {
			continue
		}
		seen[eff] = true
		out = append(out, eff)
	}
	return out
}
