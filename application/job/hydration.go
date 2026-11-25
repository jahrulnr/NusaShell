package job

import (
	"context"
	"encoding/json"
	"slices"

	"github.com/jahrulnr/nusashell/infrastructure/domain/aggregate"
	"github.com/jahrulnr/nusashell/infrastructure/domain/entity"
	"github.com/jahrulnr/nusashell/infrastructure/domain/enum"
	dservice "github.com/jahrulnr/nusashell/infrastructure/domain/service"
	"github.com/jahrulnr/nusashell/infrastructure/domain/valueobject"
)

// appendToolContexts is the discovery phase before a provider call. The
// selected driver owns discovery and hands the turn the records it
// rendered; the application persists them verbatim, never importing a
// concrete tool adapter.
//
// The application owns the cadence, because it is the only layer that
// sees the room's history. A record whose producer declares it one-shot
// is orientation: once the live tail carries one for this binding, the
// step renders nothing for that slot at all — the name joins the driver's
// skip set, so neither the probe nor the build is paid. The record's own
// input is the evidence, so a workspace rebind voids the slot and the new
// tree is mapped once. A root the gate refuses — the fallback home, or one
// without a project marker — renders no one-shot record at all; a
// provisional render is never persisted, so the record is dropped and the
// slot stays open for the next step. Every other record keeps value dedup
// against the newest live injection call carrying the same name and input
// — comparing Dedup-normalized output when the record declares a
// normalizer — so volatile churn writes nothing and a fold that sealed a
// record restores it. Every kept record lands as its own
// TooltypeInjection call on a single system message.
func (d Deps) appendToolContexts(ctx context.Context, convID string, conv *aggregate.Conversation, driver EnvironmentDriver, root string) error {
	// One backward pass anchors every slot: the newest output per
	// name+input drives the value dedup, and the newest input per name is
	// what a one-shot slot tests its binding against.
	msgs := conv.Messages()
	latest := map[string]string{}
	newest := map[string]string{}
	for _, msg := range slices.Backward(msgs) {
		for _, prior := range msg.ToolCalls() {
			if prior.Type() != enum.TooltypeInjection || !prior.HasOutput() {
				continue
			}
			key := prior.Name() + "\x00" + prior.Input()
			if _, ok := latest[key]; !ok {
				latest[key] = prior.Output()
			}
			if _, ok := newest[prior.Name()]; !ok {
				newest[prior.Name()] = prior.Input()
			}
		}
	}

	// The one-shot slots this binding already filled. The record's own
	// input is the evidence, so a rebind voids the slot.
	oneshot := oneShotNames(driver)
	filled := map[string]bool{}
	for _, name := range oneshot {
		if input, ok := newest[name]; ok && citesWorkspace(input, root, dservice.ExtraFolders(ctx)) {
			filled[name] = true
		}
	}
	// The gate rules only on an open slot: a room that already carries its
	// orientation is not re-checked, and a room with no one-shot tool reads
	// no root at all. A root that fails — the fallback home, or one with no
	// project marker — puts its slot in the same skip set a filled one
	// uses, and the room remembers the refusal for that root, so the step
	// pays neither a probe nor a build for a map it must not show.
	gated := len(oneshot) > len(filled) && !d.hydrateGate(ctx, conv, driver, root)
	skip := map[string]bool{}
	for _, name := range oneshot {
		if gated || filled[name] {
			skip[name] = true
		}
	}
	contexts, err := driver.ToolContexts(ctx, skip)
	if err != nil {
		return err
	}

	msg, err := entity.NewMessage(enum.RoleSystem, "")
	if err != nil {
		return err
	}
	seen := map[string]bool{}
	for _, record := range contexts {
		// A provisional render says the map does not exist yet — keeping
		// it would freeze a placeholder into the room's orientation, so
		// the slot stays open and the next step retries.
		if record.Provisional {
			continue
		}
		// The cadence and the gate outrank the driver: a one-shot record
		// never lands twice for the same binding, and never at all for a
		// gated root, whatever the driver chose to render.
		if record.Once && (filled[record.Name] || gated) {
			continue
		}
		dedup := record.Dedup
		if dedup == nil {
			dedup = func(s string) string { return s }
		}
		key := record.Name + "\x00" + record.Input
		normalized := dedup(record.Output)
		if prior, ok := latest[key]; ok && dedup(prior) == normalized {
			continue
		}
		if seen[key] {
			continue
		}
		seen[key] = true

		id, err := valueobject.NewID("call", "")
		if err != nil {
			return err
		}
		call, err := entity.NewToolCall(id.String(), record.Name, record.Input, enum.TooltypeInjection)
		if err != nil {
			return err
		}
		call.SetStoredOutput(record.Output, "")
		if err := msg.AddToolCall(call); err != nil {
			return err
		}
	}
	// The room's unread finishes ride the same message as one record. They are
	// data in the shape every other system record takes, so the model reads a
	// status list — never words — and the room learns what finished without a
	// turn of its own. A turn that was already running when the exit was reported
	// renders it here, on this step: no job rides behind it.
	rendered := d.appendCompletionRecord(convID, &msg)
	if len(msg.ToolCalls()) == 0 {
		return nil
	}
	conv.Append(msg)
	if _, err := d.Convs.AppendMessage(ctx, convID, msg); err != nil {
		return err
	}
	if len(rendered) > 0 {
		d.Completions.ClearCompletions(convID, rendered)
	}
	return nil
}

// appendCompletionRecord adds the room's pending finishes to the hydration message
// and answers the keys it rendered, so the caller drops exactly what the model
// read — a finish reported after this point keeps waiting.
func (d Deps) appendCompletionRecord(convID string, msg *entity.Message) []string {
	if d.Completions == nil {
		return nil
	}
	pending := d.Completions.PendingCompletions(convID)
	if len(pending) == 0 {
		return nil
	}
	keys := make([]string, 0, len(pending))
	cited := make([]completionCitation, 0, len(pending))
	for _, c := range pending {
		keys = append(keys, c.Key())
		cited = append(cited, completionCitation{Source: c.Source, ID: c.ID})
	}
	input, err := json.Marshal(struct {
		Records []completionCitation `json:"records"`
	}{Records: cited})
	if err != nil {
		return nil
	}
	id, err := valueobject.NewID("call", "")
	if err != nil {
		return nil
	}
	call, err := entity.NewToolCall(id.String(), CompletionRecord, string(input), enum.TooltypeInjection)
	if err != nil {
		return nil
	}
	call.SetStoredOutput(RenderCompletions(pending), "")
	if err := msg.AddToolCall(call); err != nil {
		return nil
	}
	return keys
}

// completionCitation is what a rendered record names: the finish it carries, so a
// reader can tell which exit a record belongs to.
type completionCitation struct {
	Source string `json:"source"`
	ID     string `json:"id"`
}

// oneShotNames lists the tools whose record the cadence governs: the
// registered, system-callable tools that declare themselves one-shot. An
// unregistered tool renders nothing, so it has no cadence to apply.
func oneShotNames(driver EnvironmentDriver) []string {
	var names []string
	for _, schema := range driver.ToolSchemas() {
		if !schema.CallableBySystem() {
			continue
		}
		tool, ok := driver.Tool(schema.Name())
		if !ok {
			continue
		}
		if decl, ok := tool.(OneShotHydration); ok && decl.OneShotHydration() {
			names = append(names, schema.Name())
		}
	}
	return names
}

// citesWorkspace reports whether a record's input names this turn's
// binding — the workspace path plus the project's extra folders. The
// one-shot record's own input is the evidence: a record citing another
// tree is void, not merely stale.
func citesWorkspace(input, root string, extras []string) bool {
	var cited struct {
		Path    string   `json:"path"`
		Folders []string `json:"folders"`
	}
	if err := json.Unmarshal([]byte(input), &cited); err != nil {
		return false
	}
	return cited.Path == root && slices.Equal(cited.Folders, extras)
}
