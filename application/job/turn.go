package job

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/jahrulnr/nusashell/infrastructure/domain/aggregate"
	"github.com/jahrulnr/nusashell/infrastructure/domain/entity"
	"github.com/jahrulnr/nusashell/infrastructure/domain/enum"
	"github.com/jahrulnr/nusashell/infrastructure/domain/event"
	dservice "github.com/jahrulnr/nusashell/infrastructure/domain/service"
	"github.com/jahrulnr/nusashell/infrastructure/domain/valueobject"
	apperror "github.com/jahrulnr/nusashell/infrastructure/error"
)

// Transient upstream failures earn one provider call plus this many
// retries; the wait stays short — a provider that stays down is a
// definitive failure, not a stall to sit out.
var sendRetryBackoff = []time.Duration{750 * time.Millisecond, 1500 * time.Millisecond}

// The longest Retry-After the turn itself will sit out. Provider-asked
// quiet is honored inside this window — beyond it the ask describes a
// dead provider, not a pause (free-tier quotas can name weeks): the 429
// surfaces to the user, whose own retry or provider change decides.
const maxRateLimitWait = 60 * time.Second

// runTurn is the agent loop shared by send, edit, and resume: provider
// call → persist → execute pending tool calls → provider call — until
// the model stops requesting tools or the step cap is hit. A resume
// may enter already sitting on unanswered calls; those finish before
// any new model call. steers carries mid-turn user input buffered by
// the worker's out-of-band path; the loop folds it at the top of each
// step — after the last tool result persisted, before the next
// provider call — so injected text never bisects a tool-call/result
// pair or interleaves with a streaming reply.
func (d Deps) runTurn(ctx context.Context, convID string, conv *aggregate.Conversation, driver EnvironmentDriver, prov *aggregate.Provider, model entity.Model, svc dservice.TextModelService, onEvent event.TurnEventHandler, steers *SteeringInput, origin valueobject.TurnOrigin) error {
	// Hand-built Deps may carry a zero or invalid config — normalize
	// clamps and tag defaults fill it.
	ccfg := d.Config
	// Directory-aware tools work under the turn's resolved workspace —
	// the room's binding, else the driver's default root.
	dir := TurnWorkspace(conv, driver)
	ctx = dservice.WithWorkspaceDir(ctx, dir)
	ctx = dservice.WithConversationID(ctx, convID)
	// Provider-capable tools resolve their backend per turn from the bound
	// provider — the provider id is the provider identity users name. A nil
	// provider (hand-built deps) binds nothing and tools run provider-free.
	if prov != nil {
		ctx = dservice.WithWebProvider(ctx, dservice.WebProviderOf(prov, model.ID()))
	}
	ccfg.Normalize()
	wireModel := model.ApplyUnknownContextDefaults()
	// The call surface is live config: a stored conversation can predate
	// the catalog, so apply the current set every turn — never trust what
	// the record hydrated. Without this the wire carries no tools and the
	// model invents names it was never offered. The catalog already
	// carries the harness's ToolPermissionSystem tools (e.g. workspace)
	// so the pin's call name resolves — they stay off the wire's list.
	conv.SetTools(driver.ToolSchemas())
	// A long turn can fill the window more than once, so it may fold
	// again — but only over history it added since the last fold: a fat
	// tail that still overflows right after folding is the provider's
	// call, and refolding it would summarize the previous checkpoint at
	// full cost for nothing. foldedAt is the history length the last fold
	// left; maxFolds bounds the summaries one turn pays for.
	const maxFolds = 8
	folds, foldedAt := 0, 0
	canFold := func() bool {
		return folds < maxFolds && conv.CanManualCompact(ccfg.CompactKeepTail) &&
			(folds == 0 || len(conv.Messages()) >= foldedAt+max(ccfg.CompactKeepTail, 1))
	}
	fold := func(m entity.Model) error {
		if err := d.compact(ctx, convID, conv, prov, m, svc, ccfg.CompactKeepTail, onEvent); err != nil {
			return err
		}
		folds++
		foldedAt = len(conv.Messages())
		return nil
	}
	// Set by the media gate below when a tool's image output had to be
	// withheld for the serving's per-request cap — the fold that frees
	// the count happens at the next step or turn end. A history that
	// arrives already over the cap — user attachments are not withheld
	// like tool output — asks for the same fold.
	hitMediaLimit := false
	if limit := model.MaxImages(); limit > 0 && conv.MediaCount(enum.ModalityImage) > limit {
		hitMediaLimit = true
	}
	// pinnedID is the record the last pin attempt left the tail on — a
	// kept tail ending on it must not take a second pin after a fold.
	// The dedup inside appendWorkspaceContext makes this a fast path:
	// an unchanged pin also skips the append there.
	pinnedID := ""
	for step := 0; step < ccfg.MaxTurnStep; step++ {
		if err := ctx.Err(); err != nil {
			return apperror.New(408, "turn canceled", err)
		}
		// A resumed turn can already be sitting on unanswered calls —
		// the provider asked, the previous attempt died before the
		// results were stored. Run those before any new model call or
		// a steer fold, so the history the model sees still contains
		// the work it already paid for.
		if last := conv.Last(); last != nil && len(pendingCalls(*last)) > 0 {
			if step == ccfg.MaxTurnStep-1 {
				return apperror.New(500, "tool loop limit reached", fmt.Errorf("%d steps without a final answer", ccfg.MaxTurnStep))
			}
			var perr error
			hitMediaLimit, perr = d.finishPending(ctx, convID, conv, driver, model, last, onEvent, hitMediaLimit)
			if perr != nil {
				return perr
			}
			continue
		}
		// Steering becomes an ordinary user message in history — the
		// model answers it on this call. Input arriving while the turn
		// ends without another step (no pending tools) never folds; the
		// caller decides what the undelivered text becomes. The claim is
		// one atomic drain; persistence marks each receipt by the
		// committed bool — even when the append also returned an error —
		// while claimed-but-uncommitted entries stay claimed so a
		// terminal snapshot reports them unconsumed.
		if steers != nil {
			for _, input := range steers.claim() {
				user, err := entity.NewUserMessage(input.input)
				if err != nil {
					return err
				}
				conv.Append(user)
				committed, err := d.Convs.AppendMessage(ctx, convID, user)
				if input.receipt != nil && committed {
					input.receipt.markPersisted()
				}
				if err != nil {
					return err
				}
				if onEvent != nil {
					onEvent(event.NewSteerEvent(input.input.Text, input.input.Attachments...))
				}
			}
		}
		compacted := false
		// The budget is a share of the model's window so headroom stays
		// reserved for the reply; a checkpoint that would drop zero
		// messages is noise in history, not headroom (canFold). The media
		// gate reuses this fold: a withheld image means the history is at
		// the serving's cap, so sealing it frees room for the next call
		// rather than failing on the replayed images.
		if (hitMediaLimit || conv.NeedCompaction(model.CompactionBudgetTokens(ccfg.CompactBudgetPct))) && canFold() {
			if err := fold(model); err != nil {
				return err
			}
			compacted = true
		}
		if last := conv.Last(); last != nil && (last.Editable() || compacted) {
			// The pin follows an editable line — the submitted message
			// or a folded steer — or a just-run fold: after compaction
			// the tail can end on a non-editable message and any
			// earlier workspace record may be sealed inside the
			// checkpoint, so re-pin the current path for the rest of
			// the turn. The record the provider reads cites the current
			// path, while the system prompt stays untouched.
			if err := d.appendWorkspaceContext(ctx, convID, conv, dir); err != nil {
				return err
			}
			pinnedID = conv.Last().ID()
		}
		// Where the turn came from is per-turn context, recorded next to
		// the workspace pin rather than in the system prompt: a prompt
		// edited per request loses its cache prefix. The same origin in
		// the live tail is not repeated; a fold that sealed it restores it.
		if err := d.appendOriginContext(ctx, convID, conv, origin); err != nil {
			return err
		}
		// The environment renders its system tool snapshots each step;
		// unchanged ones dedup away so only semantic change costs a
		// record, and a one-shot record the tail already carries for this
		// binding is skipped before its tool runs at all.
		if err := d.appendToolContexts(ctx, convID, conv, driver, dir); err != nil {
			return err
		}
		before := len(conv.Messages())
		var parts []entity.MessagePart
		captureEvent := func(ev event.TurnEvent) {
			switch ev.Kind() {
			case enum.TurnEventDelta, enum.TurnEventReasoning, enum.TurnEventReasoningSummary:
				kind := entity.MessagePartText
				if ev.Kind() == enum.TurnEventReasoning || ev.Kind() == enum.TurnEventReasoningSummary {
					kind = entity.MessagePartReasoning
				}
				if text := ev.Delta(); text != "" {
					if len(parts) > 0 && parts[len(parts)-1].Kind == kind {
						parts[len(parts)-1].Text += text
					} else {
						parts = append(parts, entity.MessagePart{Kind: kind, Text: text})
					}
				}
			case enum.TurnEventToolCall:
				parts = append(parts, entity.MessagePart{
					Kind: entity.MessagePartToolCall, ToolCallID: ev.ToolCall().ID(),
				})
			}
			if onEvent != nil {
				onEvent(ev)
			}
		}
		// Provider call — one attempt plus bounded retries for
		// transient upstream failures (rate limit, 5xx, transport).
		// A retry is judged by what the attempt *persisted*, not by what
		// the client already watched: streamed deltas are rebuilt per
		// attempt from that attempt's own stream, and a failed attempt
		// commits nothing, so retrying it adds no second copy to history.
		// Anti-duplication therefore has two owners — this loop persists
		// only the attempt that succeeds, and the client drops the failed
		// attempt's rendered segment when it sees the retry_wait status.
		// A client cancel, a non-transient status, or a call that already
		// appended a message still ends the retries.
		var added []entity.Message
		var err error
		var canceled bool
		// The cap holds on every wire leg, user attachments included —
		// they enter history uncounted by the tool gate, so a history
		// still over the limit after its fold chance must not send at
		// all rather than leak the excess upstream.
		if limit := model.MaxImages(); limit > 0 && conv.MediaCount(enum.ModalityImage) > limit {
			return apperror.New(400, "image limit", fmt.Errorf(
				"conversation carries %d images but model %q accepts at most %d per request — compact the conversation to seal earlier images",
				conv.MediaCount(enum.ModalityImage), model.ID(), limit))
		}
		for attempt := 0; ; attempt++ {
			// Output capture resets per attempt: a retried call rebuilds
			// parts from its own stream, so nothing the failed attempt
			// showed can splice into the answer this one commits.
			parts = nil
			err = svc.Send(ctx, conv, prov, wireModel, captureEvent)
			added = conv.Messages()[before:]
			canceled = err != nil && (ctx.Err() != nil || apperror.As(err).StatusCode() == 408)
			if err == nil || canceled {
				break
			}
			status := apperror.As(err).StatusCode()
			transient := status == 429 || status >= 500
			// Only a message the call already appended blocks the retry:
			// streamed output alone is not persistence, and the client
			// discards its segment on the retry_wait this loop announces.
			if !transient || attempt == len(sendRetryBackoff) || len(added) > 0 {
				break
			}
			// A provider-asked wait (Retry-After) wins over our own
			// backoff — but only inside maxRateLimitWait. Past that the
			// provider is dead for a duration no turn should sit out; the
			// error surfaces so the user's retry or provider change decides.
			wait := sendRetryBackoff[attempt]
			var delayed interface{ RetryAfter() time.Duration }
			if errors.As(err, &delayed) && delayed.RetryAfter() > wait {
				if delayed.RetryAfter() > maxRateLimitWait {
					break
				}
				wait = delayed.RetryAfter()
			}
			// The owner sees the pause for what it is — a provider asking
			// for quiet, not a stalled stream.
			if onEvent != nil {
				onEvent(event.NewStatusEvent(enum.TurnStatusRetryWait, wait))
			}
			select {
			case <-ctx.Done():
				return apperror.New(408, "turn canceled", ctx.Err())
			case <-time.After(wait):
			}
		}
		// A canceled stream may already have appended the text and
		// reasoning that arrived, plus tool calls whose argument deltas
		// finished. Persist that partial message and mark those calls
		// interrupted instead of dropping the turn from history.
		if err != nil && !canceled && apperror.As(err).StatusCode() == 413 && len(added) == 0 && len(parts) == 0 && canFold() {
			// The provider says overflow even though our estimate
			// disagreed — believe the provider once: compact and retry.
			// The fold-and-retry needs a call that left nothing behind:
			// an appended message or streamed segments mean this attempt
			// is the turn's outcome, and the failure surfaces instead
			// (or becomes the interrupted reply the resume path builds on).
			if err := fold(wireModel); err != nil {
				return err
			}
			// Same blind spot as the proactive fold: the kept tail can
			// end on a non-editable message the pin check skips —
			// re-pin the workspace for the retry unless the tail
			// already carries this turn's record.
			if last := conv.Last(); last != nil && !last.Editable() && last.ID() != pinnedID {
				if err := d.appendWorkspaceContext(ctx, convID, conv, dir); err != nil {
					return err
				}
				pinnedID = conv.Last().ID()
			}
			continue
		}
		if err != nil && !canceled {
			// A cancel landing mid-body-read can surface as an upstream
			// error from the adapter — normalize before it misroutes.
			if ctx.Err() != nil {
				return apperror.New(408, "turn canceled", ctx.Err())
			}
			// The failed call committed nothing, but the owner already
			// watched its text stream in. Keep that output as an
			// interrupted reply so the failure does not erase it, and so
			// a Retry (turn.resume) continues from it instead of
			// regenerating the answer from scratch.
			if len(added) == 0 {
				partial, ok, perr := interruptedReply(parts)
				if perr != nil {
					return perr
				}
				if ok {
					conv.Append(partial)
					if _, perr := d.Convs.AppendMessage(ctx, convID, partial); perr != nil {
						return perr
					}
					if perr := d.Convs.Save(ctx, conv); perr != nil {
						return perr
					}
				}
			}
			return err
		}
		if canceled && len(added) == 0 {
			if ctx.Err() != nil {
				// The reply the owner watched stream is still history: keep it
				// as an interrupted message before the turn reports the cancel,
				// sealed with the cause that cut it. A restart that erased the
				// partial would make the owner's last screen a lie.
				if partial, ok, perr := interruptedReply(parts); perr != nil {
					return perr
				} else if ok {
					if perr := stampInterrupted(ctx, &partial); perr != nil {
						return perr
					}
					if _, perr := d.Convs.AppendMessage(context.WithoutCancel(ctx), convID, partial); perr != nil {
						return perr
					}
					if perr := d.Convs.Save(context.WithoutCancel(ctx), conv); perr != nil {
						return perr
					}
				}
				return apperror.New(408, "turn canceled", ctx.Err())
			}
			return err
		}
		// The port contract is one assistant append per call — verify it
		// instead of trusting conv.Last(), so a misbehaving adapter can't
		// duplicate the user line or silently drop output.
		if len(added) == 0 {
			return apperror.New(500, "adapter produced no message", fmt.Errorf("service appended 0 messages"))
		}
		if len(parts) > 0 {
			conv.Last().SetParts(parts)
		}
		persistCtx := ctx
		if canceled || ctx.Err() != nil {
			if err := stampInterrupted(ctx, conv.Last()); err != nil {
				return err
			}
			// The partial reply and interrupted markers are history, not
			// speculative work — they must commit even though the turn
			// ctx just died; a ctx-honoring store would drop them.
			persistCtx = context.WithoutCancel(ctx)
		}
		for _, m := range added {
			if _, err := d.Convs.AppendMessage(persistCtx, convID, m); err != nil {
				return err
			}
		}
		// Meta (usage counters) travels with each step — a crash mid-loop
		// leaves messages and accounting consistent.
		if err := d.Convs.Save(persistCtx, conv); err != nil {
			return err
		}
		if canceled || ctx.Err() != nil {
			// The partial reply is already persisted. A canceled caller
			// must not read success or continue into tool execution.
			if ctx.Err() != nil {
				return apperror.New(408, "turn canceled", ctx.Err())
			}
			return err
		}

		msgs := conv.Messages()
		last := &msgs[len(msgs)-1]
		pending := pendingCalls(*last)
		if len(pending) == 0 {
			// finish_reason=tool_calls with zero executable calls is not
			// "the model is done" — the provider asked for tools but
			// every call it streamed was malformed or truncated and the
			// adapter dropped it. Fail the turn loudly instead of
			// letting an empty assistant reply settle the room.
			if last.FinishReason() == enum.FinishReasonToolCalls {
				return apperror.New(502, "provider requested tool calls but sent none", fmt.Errorf("finish_reason=tool_calls with zero calls"))
			}
			// The turn ends with images withheld and the history still
			// at the cap — fold now so the next user turn replays under
			// the limit. This only fires when the step-top fold above
			// couldn't (the tail just grew past keepTail on this step).
			if hitMediaLimit && canFold() {
				if err := fold(model); err != nil {
					return err
				}
			}
			return nil // the model is done
		}
		if step == ccfg.MaxTurnStep-1 {
			// The unanswered pending calls stay in history — a retry
			// replays them, which is honest: the model did ask.
			return apperror.New(500, "tool loop limit reached", fmt.Errorf("%d steps without a final answer", ccfg.MaxTurnStep))
		}
		var perr error
		hitMediaLimit, perr = d.finishPending(ctx, convID, conv, driver, model, last, onEvent, hitMediaLimit)
		if perr != nil {
			return perr
		}
	}
	return nil
}

// finishPending runs origin calls that still have no output and patches
// each result onto the message. hitMediaLimit comes back set when an
// image result was withheld for the serving's cap.
func (d Deps) finishPending(ctx context.Context, convID string, conv *aggregate.Conversation, driver EnvironmentDriver, model entity.Model, last *entity.Message, onEvent event.TurnEventHandler, hitMediaLimit bool) (bool, error) {
	for _, call := range pendingCalls(*last) {
		if err := ctx.Err(); err != nil {
			if err := d.persistInterrupted(ctx, convID, last); err != nil {
				return hitMediaLimit, err
			}
			return hitMediaLimit, apperror.New(408, "turn canceled", err)
		}
		// Resolve and run one call, then record the result twice: on
		// the in-memory message (so the next provider call sees it)
		// and as a patch record in the repository (so a reload
		// restores it). Executor failures become tool output — the
		// model deserves honest results — while context
		// cancellation aborts the whole turn.
		err := func() error {
			var out string
			failed := false
			var outAtts []valueobject.Attachment
			permitted := false
			for _, s := range conv.Tools() {
				if s.Name() == call.Name() && s.CallableByAgent() {
					permitted = true
					break
				}
			}
			// Live chunks flow to the frontend scoped by call id —
			// they are a preview, never history; only the Execute
			// result lands in output. What the client already saw is
			// kept as a bounded head, so the settled tail can be sent
			// as the rest instead of a second copy — and a command that
			// streams a hundred megabytes does not sit in this turn's
			// memory twice.
			emitted := false
			streamedOver := false
			var streamed strings.Builder
			emit := func(chunk string) {
				emitted = true
				if !streamedOver {
					if streamed.Len()+len(chunk) > streamedHeadBytes {
						// Past the head the prefix check can no longer be
						// made; drop what we held rather than grow.
						streamedOver = true
						streamed.Reset()
					} else {
						streamed.WriteString(chunk)
					}
				}
				if onEvent != nil {
					onEvent(event.NewToolOutputEvent(call, chunk))
				}
			}
			tool, registered := driver.Tool(call.Name())
			switch {
			case !permitted:
				out, failed = "error: tool not permitted", true
			case !registered:
				out, failed = "error: unknown tool "+call.Name(), true
			default:
				res, err := tool.Execute(ctx, call, emit)
				switch {
				case err == nil:
					out, outAtts = res.Text, res.Attachments
				case ctx.Err() != nil && res.Text != "":
					// The tool kept what ran before the stop — its own words
					// say who stopped it. That text is the call's outcome, so
					// it is recorded here rather than replaced by the blind
					// marker; the turn ends canceled once it is persisted.
					out, outAtts = res.Text, res.Attachments
				case ctx.Err() != nil:
					return apperror.New(408, "turn canceled", ctx.Err())
				default:
					out, failed = "error: "+err.Error(), true
				}
			}
			// The serving's per-request image cap counts every image
			// in the replayed history, so a tool result that would
			// push the live count over it is withheld — the recorded
			// history stays intact (prompt cache survives) and the
			// note tells the model why, while hitMediaLimit folds the
			// history so a later step can see new images again. A 0
			// cap means the serving declared none — fail open.
			if limit := model.MaxImages(); limit > 0 {
				room := limit - conv.MediaCount(enum.ModalityImage)
				var kept []valueobject.Attachment
				dropped := 0
				for _, a := range outAtts {
					if a.Modality() == enum.ModalityImage {
						if room <= 0 {
							dropped++
							continue
						}
						room--
					}
					kept = append(kept, a)
				}
				if dropped > 0 {
					outAtts = kept
					hitMediaLimit = true
					out += fmt.Sprintf(" (%d image output(s) withheld: this serving accepts at most %d images per request and that limit is now reached — new images cannot be shown until this conversation is compacted)", dropped, limit)
				}
			}
			// One-shot tools never stream — without a chunk the live
			// card settles blank, so the settled output crosses as a
			// single late chunk carrying the same text the record
			// persists (error results included; the owner reads what
			// the model reads).
			if !emitted && out != "" {
				emit(out)
			}
			// A tool that streamed its own output still settles with text
			// the stream never carried — the exit status, the line naming
			// who stopped it, a withheld-media note. Send exactly that
			// extension, so the live row ends with the record's text and a
			// stopped run does not read as a clean success until reload.
			if emitted && !streamedOver && len(out) > streamed.Len() && strings.HasPrefix(out, streamed.String()) {
				emit(out[streamed.Len():])
			}
			if err := last.SetToolOutput(call.ID(), out); err != nil {
				return apperror.New(500, "tool output lost", err)
			}
			// Attachments land alongside the recorded result.
			if err := last.SetToolOutputAttachments(call.ID(), outAtts); err != nil {
				return apperror.New(500, "tool output lost", err)
			}
			if err := last.SetToolFailed(call.ID(), failed); err != nil {
				return apperror.New(500, "tool outcome lost", err)
			}
			// The terminal outcome is its own event: a frontend must not
			// have to guess success from the output's wording, and an
			// empty result still has to settle the live row.
			call.SetFailed(failed)
			// The media the call produced rides the event, so the owner sees
			// what the model is shown without waiting for the turn to end.
			call.SetOutputAttachments(outAtts)
			if onEvent != nil {
				onEvent(event.NewToolResultEvent(call, failed))
			}
			for _, tc := range last.ToolCalls() {
				if tc.ID() == call.ID() {
					// The result is already computed. A ctx that died between
					// the in-memory record and this patch must not drop
					// it: persistInterrupted skips calls that HasOutput.
					return d.Convs.UpdateToolCallOutput(context.WithoutCancel(ctx), convID, tc)
				}
			}
			return apperror.New(500, "tool output lost", fmt.Errorf("call %s vanished after update", call.ID()))
		}()
		if err != nil {
			if ctx.Err() != nil {
				if stampErr := d.persistInterrupted(ctx, convID, last); stampErr != nil {
					return hitMediaLimit, stampErr
				}
				return hitMediaLimit, apperror.New(408, "turn canceled", ctx.Err())
			}
			return hitMediaLimit, err
		}
		// A tool that reported its result while the turn was canceled has
		// already written it above; the turn still ends canceled — with the
		// captured output in history instead of a marker over it.
		if ctx.Err() != nil {
			if err := d.persistInterrupted(ctx, convID, last); err != nil {
				return hitMediaLimit, err
			}
			return hitMediaLimit, apperror.New(408, "turn canceled", ctx.Err())
		}
	}
	return hitMediaLimit, nil
}

// resolveProvider picks the effective provider/model — job overrides win, then
// the stored binding — and resolves them to live objects: the Provider
// aggregate, the catalog Model, and the wire adapter for the style.
func (d Deps) resolveProvider(ctx context.Context, convID string, conv *aggregate.Conversation, driver EnvironmentDriver, providerID, modelID string) (
	prov *aggregate.Provider, model entity.Model, svc dservice.TextModelService, err error,
) {
	if providerID == "" {
		providerID = conv.ProviderID()
	}
	if providerID == "" {
		return nil, model, nil, apperror.New(400, "no provider bound", fmt.Errorf("conversation %s has no provider", convID))
	}
	if modelID == "" {
		modelID = conv.ModelID()
	}
	if modelID == "" {
		return nil, model, nil, apperror.New(400, "no model bound", fmt.Errorf("conversation %s has no model", convID))
	}
	prov, err = d.Providers.Load(ctx, providerID)
	if err != nil {
		return nil, model, nil, err // 404 = dangling providerID — the caller picks another
	}
	m, ok := prov.Model(modelID)
	if !ok {
		return nil, model, nil, apperror.NewNotFoundError("model "+modelID, fmt.Errorf("provider %s catalog has no such model", providerID))
	}
	// The effective style is the model's own pin when set, else the
	// provider's — one endpoint may serve models across paradigms.
	style := m.WireStyle(prov.Style())
	svc, ok = driver.TextService(style)
	if !ok {
		// The provider config is fine; the server simply wired no adapter
		// for this style — that's a deployment defect, not a user error.
		return nil, model, nil, apperror.New(500, "no adapter for wire style", fmt.Errorf("style %s unregistered", style))
	}
	return prov, m, svc, nil
}

// stampInterrupted marks origin calls that never received a result, and a
// reply the provider never closed with the interrupted finish — an empty
// finish would read as a complete answer to a later reload. Incomplete
// argument streams are not on the message — adapters omit them before
// appending.
//
// The short marker names its cause (ADR-0004 §7): a restart that cuts a call
// must not blame the owner. Three causes share two lengths — the owner's Stop,
// the restart, and a caller that simply left; the canceled cause reads short
// here and long in a tool result that had output
// (`(canceled after N.Ns — partial output above)`). An agent-canceled call has
// no short form yet: that mechanism is not landed, so a fourth form is not
// invented here.
func stampInterrupted(ctx context.Context, msg *entity.Message) error {
	if msg == nil {
		return nil
	}
	if msg.FinishReason() == "" {
		msg.SetFinishReason(enum.FinishReasonInterrupted)
	}
	cause := context.Cause(ctx)
	marker := ""
	switch {
	case errors.Is(cause, dservice.ErrInterruptedByUser):
		marker = "interrupted by user"
	case errors.Is(cause, dservice.ErrServerRestarted):
		marker = "interrupted by a nusashell restart"
	case errors.Is(cause, context.Canceled):
		// The caller went away — a closed tab, a dropped socket. Nobody
		// decided this, so the marker must not name the owner; it uses the
		// canceled form the tool vocabulary already has.
		marker = "(canceled — the caller went away)"
	case errors.Is(cause, dservice.ErrInterruptedByAgent):
		// Deliberately unnamed: no short form exists for an agent's own
		// per-call abort until that mechanism lands and the ADR names it.
		// The placeholder below says so rather than borrowing the owner's
		// words for a stop the owner never made.
	default:
		// An unrecognized cause must never fall through to a name. This arm
		// exists to keep a future mechanism from silently accusing the owner
		// the day it starts canceling turns.
	}
	if marker == "" {
		marker = "(interrupted — cause not named)"
	}
	for _, tc := range msg.ToolCalls() {
		if tc.Type() != enum.TooltypeOrigin || tc.HasOutput() {
			continue
		}
		if err := msg.SetToolOutput(tc.ID(), marker); err != nil {
			return err
		}
		if err := msg.SetToolFailed(tc.ID(), true); err != nil {
			return err
		}
	}
	return nil
}

// streamedHeadBytes bounds what a streaming tool's live output keeps in
// memory for the "send only the tail" check — the same reasoning as the
// output budget, applied to the turn's own copy.
const streamedHeadBytes = 64 << 10

// interruptedReply is the reply a failed call streamed before it broke:
// the text and reasoning the owner already saw, in arrival order. Tool-call
// parts are dropped — their calls never reached history. Without answer
// text there is nothing for a Retry to replay, so no reply is kept (ok=false).
func interruptedReply(parts []entity.MessagePart) (entity.Message, bool, error) {
	var text strings.Builder
	kept := make([]entity.MessagePart, 0, len(parts))
	for _, p := range parts {
		switch p.Kind {
		case entity.MessagePartText:
			text.WriteString(p.Text)
		case entity.MessagePartToolCall:
			continue
		}
		kept = append(kept, p)
	}
	if text.Len() == 0 {
		return entity.Message{}, false, nil
	}
	msg, err := entity.NewMessage(enum.RoleAssistant, text.String())
	if err != nil {
		return entity.Message{}, false, err
	}
	msg.SetParts(kept)
	msg.SetFinishReason(enum.FinishReasonInterrupted)
	return msg, true, nil
}

// persistInterrupted writes the interrupted marker for calls that are
// already in history with an empty output — a stop during execution.
// The turn ctx is dead by definition here (its cancel is the stop), so
// the writes ride a detached ctx — cancellation must not take the
// cleanup down with it.
func (d Deps) persistInterrupted(ctx context.Context, convID string, msg *entity.Message) error {
	ctx = context.WithoutCancel(ctx)
	if msg == nil {
		return nil
	}
	pending := pendingCalls(*msg)
	if err := stampInterrupted(ctx, msg); err != nil {
		return err
	}
	for _, pc := range pending {
		for _, cur := range msg.ToolCalls() {
			if cur.ID() == pc.ID() {
				if err := d.Convs.UpdateToolCallOutput(ctx, convID, cur); err != nil {
					return err
				}
				break
			}
		}
	}
	return nil
}

// appendWorkspaceContext records the turn's working directory as one
// injection-typed `workspace` call on a system-role message, appended
// right after the editable line it describes — a mid-conversation system
// turn marking harness-injected context, not the system prompt (that
// layer is separate). Adapters emit it as the completed tool_call +
// result pair it records, so the evidence rides at tool-output trust;
// renderers show it as a context line, not a user quote or tool card.
// The empty latency marks it as a stored record, not a measured
// execution.
//
// A pin already sitting in the live tail for the same directories is not
// repeated — re-pinning on every editable line spends tokens without
// telling the model anything new. A different path, or no pin in the live
// tail (e.g. the last one was folded into a compaction checkpoint),
// still records.
func (d Deps) appendWorkspaceContext(ctx context.Context, convID string, conv *aggregate.Conversation, dir string) error {
	// A project's other folders ride the pin: the file tools reach them
	// by absolute path.
	extra := dservice.ExtraFolders(ctx)
	// Newest workspace pin wins: identical → the tail already cites this
	// directory; different or absent → record this turn's.
scan:
	for _, v := range slices.Backward(conv.Messages()) {
		for _, tc := range v.ToolCalls() {
			if tc.Type() != enum.TooltypeInjection || tc.Name() != "workspace" {
				continue
			}
			var prior struct {
				Path    string   `json:"path"`
				Folders []string `json:"folders"`
			}
			if err := json.Unmarshal([]byte(tc.Input()), &prior); err == nil && prior.Path == dir && slices.Equal(prior.Folders, extra) {
				return nil
			}
			break scan // newest pin cites a different directory — re-pin
		}
	}
	shown := dir
	if shown == "" {
		shown = "(unset)"
	}
	raw, err := json.Marshal(struct {
		Path    string   `json:"path"`
		Folders []string `json:"folders,omitempty"`
	}{Path: dir, Folders: extra})
	if err != nil {
		return apperror.New(500, "workspace context marshal failed", err)
	}
	if len(extra) > 0 {
		shown += "; also reachable by absolute path: " + strings.Join(extra, ", ")
	}
	msg, err := entity.NewMessage(enum.RoleSystem, "")
	if err != nil {
		return err
	}
	callID, err := valueobject.NewID("call", "")
	if err != nil {
		return err
	}
	call, err := entity.NewToolCall(callID.String(), "workspace", string(raw), enum.TooltypeInjection)
	if err != nil {
		return err
	}
	call.SetStoredOutput("workspace changed to "+shown, "")
	if err := msg.AddToolCall(call); err != nil {
		return err
	}
	conv.Append(msg)
	_, err = d.Convs.AppendMessage(ctx, convID, msg)
	return err
}

// appendOriginContext records where the turn came from as one
// injection-typed `origin` call on a system-role message, appended before
// the provider call it informs. It stays out of the system prompt so the
// cached prefix never changes with the transport. The record carries the
// structured origin as input and a one-line description as output; an
// origin already sitting in the live tail is not repeated, while a
// different one — or a sealed one after compaction — is.
func (d Deps) appendOriginContext(ctx context.Context, convID string, conv *aggregate.Conversation, origin valueobject.TurnOrigin) error {
	origin, ok := origin.Normalized()
	if !ok {
		return nil
	}
	raw, err := json.Marshal(origin)
	if err != nil {
		return apperror.New(500, "turn origin marshal failed", err)
	}
	input := string(raw)
scan:
	for _, v := range slices.Backward(conv.Messages()) {
		for _, tc := range v.ToolCalls() {
			if tc.Type() != enum.TooltypeInjection || tc.Name() != "origin" {
				continue
			}
			if tc.Input() == input {
				return nil
			}
			break scan // newest origin record names a different place — re-record
		}
	}
	msg, err := entity.NewMessage(enum.RoleSystem, "")
	if err != nil {
		return err
	}
	callID, err := valueobject.NewID("call", "")
	if err != nil {
		return err
	}
	call, err := entity.NewToolCall(callID.String(), "origin", input, enum.TooltypeInjection)
	if err != nil {
		return err
	}
	call.SetStoredOutput("turn origin: "+origin.Description(), "")
	if err := msg.AddToolCall(call); err != nil {
		return err
	}
	conv.Append(msg)
	_, err = d.Convs.AppendMessage(ctx, convID, msg)
	return err
}

// pendingCalls returns the model-originated calls still awaiting output on
// a message — injection entries and already-answered calls are skipped.
func pendingCalls(m entity.Message) []entity.ToolCall {
	var out []entity.ToolCall
	for _, tc := range m.ToolCalls() {
		if tc.Type() == enum.TooltypeOrigin && !tc.HasOutput() {
			out = append(out, tc)
		}
	}
	return out
}
