package job

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/jahrulnr/nusashell/infrastructure/domain/aggregate"
	"github.com/jahrulnr/nusashell/infrastructure/domain/entity"
	"github.com/jahrulnr/nusashell/infrastructure/domain/enum"
	"github.com/jahrulnr/nusashell/infrastructure/domain/event"
	dservice "github.com/jahrulnr/nusashell/infrastructure/domain/service"
	"github.com/jahrulnr/nusashell/infrastructure/domain/valueobject"
	apperror "github.com/jahrulnr/nusashell/infrastructure/error"
	"github.com/jahrulnr/nusashell/infrastructure/prompt"
)

// compactAttempts bounds the checkpoint dialogue — a model that cannot
// produce the handoff in a few tries fails the compaction, not the turn
// forever.
const compactAttempts = 3

// compactInputShare is the part of the model's input allowance the
// handoff request may fill (percent); the rest absorbs the estimate's
// error. The fold runs when the history nears the window — and after the
// provider refused it as too large — so the full history may not fit the
// very call that summarizes it.
const compactInputShare = 85

// compactOutputClips are the per-call output budgets (bytes) the handoff
// input steps down through when the history does not fit: the checkpoint
// needs what each tool call did and found, not every byte it printed.
var compactOutputClips = []int{8000, 2000, 500}

// fitCompactionInput returns the history the handoff call reads, made to
// fit by fits: whole when it fits; else with tool outputs clipped step by
// step; else, at the tightest clip, without its oldest messages — a
// leading checkpoint stays, since it carries everything before it — and
// a note saying how many were left out. Messages are whole units: a tool
// output rides inside its call's message, so no pair is split.
func fitCompactionInput(history []entity.Message, fits func([]entity.Message) bool) ([]entity.Message, error) {
	if fits(history) {
		return history, nil
	}
	var clipped []entity.Message
	for _, n := range compactOutputClips {
		clipped = make([]entity.Message, len(history))
		for i, m := range history {
			clipped[i] = m.WithToolOutputBudget(n)
		}
		if fits(clipped) {
			return clipped, nil
		}
	}
	head := 0
	if len(clipped) > 0 && clipped[0].Role() == enum.RoleUser && clipped[0].UserType() == enum.LLMUserTypeSystem {
		head = 1 // an earlier checkpoint
	}
	for drop := 1; head+drop < len(clipped); drop++ {
		note, err := entity.NewMessage(enum.RoleUser, fmt.Sprintf(
			"[%d earlier messages are left out of this request because the conversation no longer fits the context window. Write the checkpoint from what is shown.]", drop))
		if err != nil {
			return nil, err
		}
		note.SetUserType(enum.LLMUserTypeSystem)
		fitted := append(append(slices.Clone(clipped[:head]), note), clipped[head+drop:]...)
		if fits(fitted) {
			return fitted, nil
		}
	}
	// Nothing smaller is left to try: send the newest message alone and
	// let the provider decide.
	return clipped[len(clipped)-1:], nil
}

// compact folds the live history into a checkpoint: the model writes the
// handoff on a scratch conversation (its own transcript plus the summary
// tool — never persisted), then the sealed prefix drops and the continue
// template becomes the new head of the live tail.
// onEvent is the turn's stream when the fold runs inside a turn — the
// fold can take a whole provider call, so the owner is told it is
// happening. A manual compaction passes nil.
func (d Deps) compact(ctx context.Context, convID string, conv *aggregate.Conversation, prov *aggregate.Provider, model entity.Model, svc dservice.TextModelService, keepTail int, onEvent event.TurnEventHandler) error {
	if onEvent != nil {
		onEvent(event.NewStatusEvent(enum.TurnStatusCompacting, 0))
	}
	summary := d.SummaryTool
	if summary == nil {
		return apperror.New(500, "compaction unavailable", fmt.Errorf("no summary tool injected"))
	}
	prefix := conv.Messages()
	if sanitizer, ok := svc.(dservice.CompactionSanitizer); ok {
		prefix = sanitizer.SanitizeCompactedTail(prefix)
	}
	if len(prefix) != len(conv.Messages()) {
		return apperror.New(500, "compaction sanitizer changed history length",
			fmt.Errorf("got %d messages, want %d", len(prefix), len(conv.Messages())))
	}
	instruction, err := entity.NewMessage(enum.RoleUser, prompt.UserPrompt("compaction"))
	if err != nil {
		return err
	}
	instruction.SetUserType(enum.LLMUserTypeSystem)
	// The handoff is a scratch conversation (its own transcript plus the
	// summary tool), sized to the model: a budget of zero (no known
	// window) sends the history whole.
	wire := model.ApplyUnknownContextDefaults()
	budget := wire.CompactionInputAllowance() * compactInputShare / 100
	scratchOf := func(history []entity.Message) (*aggregate.Conversation, error) {
		s, err := aggregate.NewConversation(convID)
		if err != nil {
			return nil, err
		}
		s.SetSystemPrompt(conv.SystemPrompt())
		s.SetWorkspace(conv.Workspace())
		if err := s.SetEnvironment(conv.Environment()); err != nil {
			return nil, err
		}
		for _, m := range history {
			s.Append(m)
		}
		s.Append(instruction)
		s.SetTools([]valueobject.ToolSchema{summary.Schema()})
		return s, nil
	}
	fitted := func() (*aggregate.Conversation, error) {
		history, err := fitCompactionInput(prefix, func(h []entity.Message) bool {
			s, err := scratchOf(h)
			return err != nil || budget <= 0 || s.EstimatedTokens() <= budget
		})
		if err != nil {
			return nil, err
		}
		return scratchOf(history)
	}
	scratch, err := fitted()
	if err != nil {
		return err
	}

	// The scratch dialogue should answer with a single summary tool call,
	// but a plain-text answer is accepted — a model that ignores the
	// mandate still produced a checkpoint, which beats failing into a
	// 413 loop.
	checkpoint, err := func() (string, error) {
		for range compactAttempts {
			if err := ctx.Err(); err != nil {
				return "", apperror.New(408, "compaction canceled", err)
			}
			before := len(scratch.Messages())
			if err := svc.Send(ctx, scratch, prov, wire, nil); err != nil {
				// The provider counts differently than the estimate:
				// believe it, and send a smaller handoff.
				if apperror.As(err).StatusCode() == 413 && budget > 1 {
					budget /= 2
					if scratch, err = fitted(); err != nil {
						return "", err
					}
					continue
				}
				return "", err
			}
			msgs := scratch.Messages()
			if len(msgs) == before {
				return "", apperror.New(500, "compaction adapter produced no message", fmt.Errorf("service appended 0 messages"))
			}
			last := &msgs[len(msgs)-1]
			pending := pendingCalls(*last)
			if len(pending) == 0 {
				if text := strings.TrimSpace(last.Content()); text != "" {
					return text, nil
				}
				continue // an empty message deserves another attempt, not a checkpoint
			}
			for _, call := range pending {
				if call.Name() != summary.Schema().Name() {
					_ = last.SetToolOutput(call.ID(), "error: only the summary tool is available")
					continue
				}
				if res, err := summary.Execute(ctx, call, nil); err == nil {
					return res.Text, nil
				}
				_ = last.SetToolOutput(call.ID(), "error: the text field must hold the checkpoint")
			}
		}
		return "", apperror.New(500, "compaction failed", fmt.Errorf("model produced no checkpoint in %d attempts", compactAttempts))
	}()
	if err != nil {
		return err
	}

	next, err := entity.NewMessage(enum.RoleUser,
		strings.Replace(prompt.UserPrompt("compacted-continue"), "{{compacted_summaries}}", checkpoint, 1))
	if err != nil {
		return err
	}
	next.SetUserType(enum.LLMUserTypeSystem)

	// Keep the most recent messages live; the checkpoint is written for
	// exactly this tail plus it. Tool outputs ride inside their call's
	// message in this domain, so a message boundary cannot split a
	// call/result pair.
	dropBefore := len(conv.Messages()) - keepTail
	if dropBefore <= 0 {
		// Callers guard foldability, but a checkpoint that seals nothing
		// is noise — refuse rather than write an empty segment.
		return apperror.New(500, "nothing to compact", fmt.Errorf("dropBefore %d over %d messages", dropBefore, len(conv.Messages())))
	}
	tail := conv.Messages()[dropBefore:]
	if sanitizer, ok := svc.(dservice.CompactionSanitizer); ok {
		tail = sanitizer.SanitizeCompactedTail(tail)
	}
	if len(tail) != len(conv.Messages())-dropBefore {
		return apperror.New(500, "compaction sanitizer changed retained tail",
			fmt.Errorf("got %d messages, want %d", len(tail), len(conv.Messages())-dropBefore))
	}
	sealed := slices.Clone(conv.Messages()[:dropBefore])
	if err := d.Convs.Compact(ctx, convID, next, dropBefore, tail); err != nil {
		return err
	}
	conv.Compact(next, dropBefore, tail)
	// Handoff spend merges only after the fold commits — a failed
	// Compact must not persist usage against an unchanged history.
	conv.AddUsage(scratch.Usage())
	if err := d.Convs.Save(ctx, conv); err != nil {
		return err
	}
	if d.OnCompacted != nil {
		d.OnCompacted(convID, sealed)
	}
	return nil
}
