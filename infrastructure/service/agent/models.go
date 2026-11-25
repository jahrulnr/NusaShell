package agent

import (
	"encoding/json"
	"slices"
	"strconv"
	"strings"

	"github.com/jahrulnr/nusashell/infrastructure/service/agent/acp"

	dservice "github.com/jahrulnr/nusashell/infrastructure/domain/service"
)

// modelOption reads the session config option in the "model" category: its
// config id, current value, and offered models. Options may be flat or
// grouped. id is "" when the agent offers no model choice.
func modelOption(raw json.RawMessage) (id, current string, models []dservice.AgentModel) {
	var opts []struct {
		ID       string          `json:"id"`
		Category string          `json:"category"`
		Current  json.RawMessage `json:"currentValue"`
		Options  json.RawMessage `json:"options"`
	}
	if len(raw) == 0 || json.Unmarshal(raw, &opts) != nil {
		return "", "", nil
	}
	type choice struct {
		Value string `json:"value"`
		Name  string `json:"name"`
	}
	for _, o := range opts {
		if o.Category != "model" {
			continue
		}
		_ = json.Unmarshal(o.Current, &current)
		var flat []choice
		_ = json.Unmarshal(o.Options, &flat)
		var groups []struct {
			Options []choice `json:"options"`
		}
		if json.Unmarshal(o.Options, &groups) == nil {
			for _, g := range groups {
				flat = append(flat, g.Options...)
			}
		}
		for _, c := range flat {
			if c.Value == "" {
				continue
			}
			name := c.Name
			if name == "" {
				name = c.Value
			}
			models = append(models, dservice.AgentModel{ID: c.Value, Name: name, Current: c.Value == current})
		}
		return o.ID, current, models
	}
	return "", "", nil
}

// absorbConfigResponse folds a set_config_option response into the session's
// catalog. A response is not the offered list — it names what the write changed,
// and an agent may answer with only that entry. An option the response omits
// keeps what the session knew; an entry that carries none of its defining fields
// (name, type, choices, current value) says nothing and keeps the stored one too.
//
// The asymmetry is deliberate: a config_option_update notification carries the
// authoritative offered list and replaces (see the handler in runtime.go), while
// a response absorbs.
func absorbConfigResponse(stored, update json.RawMessage) json.RawMessage {
	var base, next []map[string]json.RawMessage
	if json.Unmarshal(update, &next) != nil {
		return stored
	}
	_ = json.Unmarshal(stored, &base)
	index := map[string]int{}
	for i, o := range base {
		var id string
		_ = json.Unmarshal(o["id"], &id)
		index[id] = i
	}
	for _, o := range next {
		var id string
		_ = json.Unmarshal(o["id"], &id)
		if id == "" {
			continue
		}
		if !carriesOption(o) {
			continue
		}
		if i, ok := index[id]; ok {
			base[i] = o
		} else {
			base = append(base, o)
		}
	}
	out, err := json.Marshal(base)
	if err != nil {
		return stored
	}
	return out
}

// carriesOption reports whether an option entry defines anything — a bare id is
// not a definition, and must not erase what the session already knows.
func carriesOption(o map[string]json.RawMessage) bool {
	for _, key := range []string{"name", "type", "options", "currentValue"} {
		raw, ok := o[key]
		if !ok {
			continue
		}
		trimmed := strings.TrimSpace(string(raw))
		if trimmed != "" && trimmed != "null" && trimmed != `""` && trimmed != "[]" {
			return true
		}
	}
	return false
}

// legacyModeID is the synthetic option id for agents that offer modes
// through session/set_mode instead of a config option.
const legacyModeID = "mode"

// sessionOptions reads the session's options other than the model: config
// options, plus legacy modes as a "mode" option when no config option
// covers them.
func sessionOptions(raw json.RawMessage, modes *acp.SessionModeState) []dservice.AgentOption {
	var opts []struct {
		ID          string          `json:"id"`
		Name        string          `json:"name"`
		Description string          `json:"description"`
		Category    string          `json:"category"`
		Type        string          `json:"type"`
		Current     json.RawMessage `json:"currentValue"`
		Options     json.RawMessage `json:"options"`
	}
	_ = json.Unmarshal(raw, &opts)
	out := []dservice.AgentOption{}
	hasMode := false
	for _, o := range opts {
		if o.Category == "model" || o.ID == "" {
			continue
		}
		if o.Category == "mode" {
			hasMode = true
		}
		opt := dservice.AgentOption{ID: o.ID, Name: o.Name, Description: o.Description, Category: o.Category, Type: o.Type}
		if opt.Name == "" {
			opt.Name = o.ID
		}
		var b bool
		var str string
		switch {
		case json.Unmarshal(o.Current, &str) == nil:
			opt.Current = str
		case json.Unmarshal(o.Current, &b) == nil:
			opt.Current = strconv.FormatBool(b)
			opt.Type = "boolean"
		}
		if opt.Type == "" {
			opt.Type = "select"
		}
		type choice struct {
			Value       json.RawMessage `json:"value"`
			Name        string          `json:"name"`
			Description string          `json:"description"`
			Options     []struct {
				Value       json.RawMessage `json:"value"`
				Name        string          `json:"name"`
				Description string          `json:"description"`
			} `json:"options"`
		}
		var list []choice
		_ = json.Unmarshal(o.Options, &list)
		add := func(v json.RawMessage, name, desc string) {
			var s string
			if json.Unmarshal(v, &s) != nil || s == "" {
				return
			}
			if name == "" {
				name = s
			}
			opt.Choices = append(opt.Choices, dservice.OptionChoice{Value: s, Name: name, Description: desc})
		}
		for _, c := range list {
			if len(c.Options) > 0 {
				for _, g := range c.Options {
					add(g.Value, g.Name, g.Description)
				}
				continue
			}
			add(c.Value, c.Name, c.Description)
		}
		out = append(out, opt)
	}
	if !hasMode && modes != nil && len(modes.AvailableModes) > 0 {
		opt := dservice.AgentOption{ID: legacyModeID, Name: "Mode", Category: "mode", Type: "select", Current: modes.CurrentModeID}
		for _, m := range modes.AvailableModes {
			opt.Choices = append(opt.Choices, dservice.OptionChoice{Value: m.ID, Name: m.Name, Description: m.Description})
		}
		out = append([]dservice.AgentOption{opt}, out...)
	}
	// Modes first: they matter most.
	slices.SortStableFunc(out, func(a, b dservice.AgentOption) int {
		if (a.Category == "mode") == (b.Category == "mode") {
			return 0
		}
		if a.Category == "mode" {
			return -1
		}
		return 1
	})
	return out
}
