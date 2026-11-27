package job

import (
	"context"
	"fmt"
	"github.com/jahrulnr/nusashell/infrastructure/domain/valueobject"
	"log/slog"
	"slices"
	"strings"
	"time"

	"github.com/jahrulnr/nusashell/infrastructure/domain/aggregate"
	"github.com/jahrulnr/nusashell/infrastructure/domain/enum"
	"github.com/jahrulnr/nusashell/infrastructure/domain/event"
	repo "github.com/jahrulnr/nusashell/infrastructure/domain/repository"
	dservice "github.com/jahrulnr/nusashell/infrastructure/domain/service"
	apperror "github.com/jahrulnr/nusashell/infrastructure/error"
)

// PendingOptionPicks checks a room's session-option picks against the catalog its
// agent reports now, answering the ones it still offers and a reason for each one
// it does not. One rule serves both moments: a pick is checked when it arrives
// (where a refusal is the answer) and again when the turn it waited for starts
// (where the turn must run anyway). A catalog that cannot be read is an error,
// never "anything goes".
func PendingOptionPicks(ctx context.Context, d Deps, conv *aggregate.Conversation, picks map[string]string) (kept map[string]string, dropped map[string]string, err error) {
	kept = map[string]string{}
	dropped = map[string]string{}
	if len(picks) == 0 {
		return kept, dropped, nil
	}
	if conv.AgentID() == "" {
		for id := range picks {
			dropped[id] = "the room runs no agent"
		}
		return kept, dropped, nil
	}
	if d.Agents == nil || d.AgentRuntime == nil {
		return nil, nil, apperror.New(503, "agent options are unavailable", nil)
	}
	agent, err := d.Agents.Load(ctx, conv.AgentID())
	if err != nil {
		if apperror.As(err).StatusCode() == apperror.StatusNotFound {
			for id := range picks {
				dropped[id] = "the agent is gone"
			}
			return kept, dropped, nil
		}
		return nil, nil, err
	}
	opts, _, _, err := d.AgentRuntime.Options(ctx, agent, conv.ID())
	if err != nil {
		return nil, nil, apperror.New(503, "agent options are unavailable", err)
	}
	// An option the agent will offer later is still a pick it knows: the stored
	// catalog is the memory that keeps a deferred pick alive until it
	// materializes, exactly as it does at accept time.
	var stored []dservice.AgentOption
	if d.AgentCatalog != nil {
		if cat, err := d.AgentCatalog.Catalog(ctx, conv.AgentID()); err == nil {
			stored = cat.Options
		}
	}
	ids := make([]string, 0, len(picks))
	for id := range picks {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	for _, id := range ids {
		value := picks[id]
		i := slices.IndexFunc(opts, func(o dservice.AgentOption) bool { return o.ID == id })
		j := slices.IndexFunc(stored, func(o dservice.AgentOption) bool { return o.ID == id })
		switch {
		case i >= 0 && value != "" && !opts[i].Offers(value):
			dropped[id] = fmt.Sprintf("option %q does not offer %q", id, value)
		case i < 0 && j < 0:
			dropped[id] = fmt.Sprintf("the agent offers no option %q", id)
		case i < 0 && value != "" && !stored[j].Offers(value):
			dropped[id] = fmt.Sprintf("option %q does not offer %q", id, value)
		default:
			kept[id] = value
		}
	}
	return kept, dropped, nil
}

// ApplyPendingSelection folds a room's parked pick into the conversation a turn
// just loaded: the runtime binding first (a turn runs on it), then effort and
// upstream, then the session-option picks. Every part is checked again here —
// the agent may have been upgraded, deleted, or narrowed between the pick and
// this turn — and a part that no longer holds is dropped with a notice instead
// of failing the turn. A room whose pick is stale must still answer its owner.
func ApplyPendingSelection(ctx context.Context, d Deps, conv *aggregate.Conversation, sel repo.PendingSelection, onEvent event.TurnEventHandler) {
	notice := func(text string) {
		slog.Warn("pending selection dropped", "conversation", conv.ID(), "why", text)
		if onEvent != nil {
			onEvent(event.NewNoticeEvent(enum.NoticeInfo, "Pick not applied", text))
		}
	}
	if sel.AgentID != "" || sel.ProviderID != "" || sel.ModelID != "" {
		b := RuntimeBinding{AgentID: sel.AgentID, AgentModel: sel.AgentModel, ProviderID: sel.ProviderID, ModelID: sel.ModelID}
		if err := BindRuntime(ctx, d, conv, b); err != nil {
			notice("the runtime you picked is no longer available: " + userReason(err))
		}
	}
	// Effort and upstream are a provider call's picks, and an agent applies
	// neither. The park path refuses such a pick at the source, so a row that
	// reaches this turn was parked before that rule: an agent room drops the
	// pick with the notice a refused pick gets — applying it would store a value
	// nothing honours, and dropping it silently would hide the refusal from the
	// owner. An unset marker is no pick and is stored as the room's own.
	dropped := agentPickFields(conv.Runtime() == enum.RuntimeAgent, sel.Effort, sel.Upstream)
	if len(dropped) > 0 {
		notice(agentPickMessage(dropped) + ", so it is not applied")
	}
	if sel.Effort != nil && !slices.Contains(dropped, "effort") {
		conv.SetEffort(*sel.Effort)
	}
	if sel.Upstream != nil && !slices.Contains(dropped, "upstream") {
		conv.SetUpstream(*sel.Upstream)
	}
	if len(sel.Options) > 0 {
		kept, dropped, err := PendingOptionPicks(ctx, d, conv, sel.Options)
		if err != nil {
			notice(userReason(err))
			return
		}
		ids := make([]string, 0, len(kept))
		for id := range kept {
			ids = append(ids, id)
		}
		slices.Sort(ids)
		for _, id := range ids {
			if err := conv.SetAgentOption(id, kept[id]); err != nil {
				notice(fmt.Sprintf("option %q: %v", id, err))
			}
		}
		droppedIDs := make([]string, 0, len(dropped))
		for id := range dropped {
			droppedIDs = append(droppedIDs, id)
		}
		slices.Sort(droppedIDs)
		for _, id := range droppedIDs {
			notice(dropped[id])
		}
	}
}

// effortPick reports whether an effort names a pick. The unset markers ("" and
// "default") return a room to its own setting, so they are not picks.
func effortPick(e *enum.Effort) bool {
	return e != nil && !e.IsUnset()
}

// upstreamPick reports whether an upstream names a pin. "" returns the room to
// the provider's own choice, so it is not a pick.
func upstreamPick(u *string) bool {
	return u != nil && strings.TrimSpace(*u) != ""
}

// agentPickFields names the provider picks an agent room cannot take, in the
// order the owner reads them. A provider room refuses none.
func agentPickFields(agent bool, effort *enum.Effort, upstream *string) []string {
	var what []string
	if agent && effortPick(effort) {
		what = append(what, "effort")
	}
	if agent && upstreamPick(upstream) {
		what = append(what, "upstream")
	}
	return what
}

// agentPickMessage is the sentence a refused pick and a dropped pick share.
func agentPickMessage(what []string) string {
	return "this room runs an agent, which takes no " + strings.Join(what, " or ") + " pick"
}

// AgentPickRefusal refuses an effort or upstream pick that would land on an
// agent room: the agent takes neither, so the pick would sit in the record where
// no turn reads it. Every write path that can carry a pick asks here, before it
// stores anything. The unset markers pass, because they store nothing an agent
// room lacks.
func AgentPickRefusal(agent bool, effort *enum.Effort, upstream *string) error {
	what := agentPickFields(agent, effort, upstream)
	if len(what) == 0 {
		return nil
	}
	return apperror.New(400, agentPickMessage(what)+": effort and upstream are settings of a provider room", nil)
}

// userReason is the public half of an app error — what a notice may say without
// leaking a cause.
func userReason(err error) string {
	if e := apperror.As(err); e != nil && e.Error() != "" {
		return e.Error()
	}
	return err.Error()
}

// applyPendingSelection folds the room's parked pick into conv — the turn that is
// starting is the one it waited for — and tells the projection it is in play. It
// answers whether anything was taken.
//
// It reads the store directly, never a projection accessor: a filtered read would
// report "nothing pending" to the very turn that has to apply it.
// It answers the generation it took (the pick's acceptedAt) so the caller can
// clear exactly that one after its save — a pick parked while this turn ran is a
// newer generation and must survive.
func applyPendingSelection(ctx context.Context, d Deps, conv *aggregate.Conversation, onEvent event.TurnEventHandler) (consumedAt time.Time, applied bool, err error) {
	sel, ok, err := d.Convs.PendingSelection(ctx, conv.ID())
	if err != nil || !ok {
		return time.Time{}, false, err
	}
	ApplyPendingSelection(ctx, d, conv, sel, onEvent)
	if d.Turns != nil {
		d.Turns.ConsumePending(conv.ID(), sel.AcceptedAt)
	}
	return sel.AcceptedAt, true, nil
}

// clearConsumedPending drops the parked file once the turn's own save landed.
// Callers invoke it only on success: a failed save leaves the file, and the claim
// release makes the pick visible again — both honest.
func clearConsumedPending(ctx context.Context, d Deps, convID string, consumedAt time.Time, applied bool) {
	if !applied {
		return
	}
	if err := d.Convs.ClearPendingSelection(context.WithoutCancel(ctx), convID, consumedAt); err != nil {
		slog.Warn("pending selection not cleared", "conversation", convID, "error", err)
	}
}

// liveTurn answers the handler a turn job runs with: its own when it already has
// one — a socket admitted turn carries the journal the hub opened — and the
// live-turn surface's when it has none. The seal it answers runs on every exit,
// including a failed turn: a journal left open holds a pool slot and its watchers
// wait for a terminal that never comes. Without a surface both answers are
// passthroughs, which is the CLI's composition.
func liveTurn(d Deps, convID, kind string, origin valueobject.TurnOrigin, onEvent event.TurnEventHandler) (event.TurnEventHandler, func(persisted bool, err error)) {
	if onEvent != nil || d.TurnSurface == nil {
		return onEvent, func(bool, error) {}
	}
	handler, seal := d.TurnSurface.Open(convID, kind, origin)
	if handler == nil {
		return onEvent, func(bool, error) {}
	}
	return handler, seal
}
