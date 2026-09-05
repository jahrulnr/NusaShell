package application

import (
	"fmt"
	"strings"

	"time"

	"nusashell/domain"
	clock "nusashell/pkg/time"
)

// MemoryService commits typed consolidator operations. It never writes
// user.md or soul.md.
type MemoryService struct {
	records MemoryRecordStore
	ops     LearningOpStore
}

func NewMemoryService(records MemoryRecordStore, ops LearningOpStore) *MemoryService {
	return &MemoryService{records: records, ops: ops}
}

func (s *MemoryService) Apply(op *domain.LearningOperation) error {
	if s == nil || s.records == nil || op == nil {
		return fmt.Errorf("memory service not configured")
	}
	if !domain.ValidLearningOpKind(op.Kind) {
		return fmt.Errorf("unknown operation %s", op.Kind)
	}
	now := clock.NewTime().Time()
	op.CreatedAt = now
	if op.ID == "" {
		op.ID = domain.NewULID(domain.IDPrefixLearnOp)
	}
	switch op.Kind {
	case domain.OpMemoryUpsert:
		rec, err := recordFromPayload(op, now)
		if err != nil {
			op.Status = domain.LearningOpRejected
			op.Reason = err.Error()
			s.saveOp(op)
			return err
		}
		if err := s.records.Save(rec); err != nil {
			op.Status = domain.LearningOpRejected
			op.Reason = err.Error()
			s.saveOp(op)
			return err
		}
		op.TargetID = rec.ID
		op.TargetType = "memory"
		op.Status = domain.LearningOpAccepted
	case domain.OpMemoryStrengthen:
		id := payloadString(op.Payload, "id")
		rec, err := s.records.Get(id)
		if err != nil {
			op.Status = domain.LearningOpRejected
			s.saveOp(op)
			return err
		}
		rec.EvidenceCount++
		rec.LastConfirmed = now
		if rec.Utility < 1 {
			rec.Utility += 0.1
		}
		rec.SupportingExperiences = append(rec.SupportingExperiences, op.Evidence...)
		// Do not NormalizeMemoryRecord here: that overwrites UpdatedAt and
		// would look like a confirmation if LastConfirmed were still zero.
		if err := s.records.Save(rec); err != nil {
			op.Status = domain.LearningOpRejected
			s.saveOp(op)
			return err
		}
		op.TargetID = rec.ID
		op.Status = domain.LearningOpAccepted
	case domain.OpMemoryRetire, domain.OpMemoryContradict:
		id := payloadString(op.Payload, "id")
		rec, err := s.records.Get(id)
		if err != nil {
			op.Status = domain.LearningOpRejected
			s.saveOp(op)
			return err
		}
		rec.Retire(now)
		if op.Kind == domain.OpMemoryContradict {
			rec.Status = domain.MemoryStatusSuperseded
			rec.Source = "consolidator"
		}
		if err := s.records.Save(rec); err != nil {
			op.Status = domain.LearningOpRejected
			s.saveOp(op)
			return err
		}
		op.TargetID = rec.ID
		op.Status = domain.LearningOpAccepted
	case domain.OpMemoryMerge:
		keepID := payloadString(op.Payload, "keep_id")
		dropID := payloadString(op.Payload, "drop_id")
		keep, err := s.records.Get(keepID)
		if err != nil {
			op.Status = domain.LearningOpRejected
			s.saveOp(op)
			return err
		}
		drop, err := s.records.Get(dropID)
		if err != nil {
			op.Status = domain.LearningOpRejected
			s.saveOp(op)
			return err
		}
		if drop.Body != "" && !strings.Contains(keep.Body, drop.Body) {
			keep.Body = strings.TrimSpace(keep.Body + "\n" + drop.Body)
		}
		keep.EvidenceCount += drop.EvidenceCount
		keep.SupportingExperiences = append(keep.SupportingExperiences, drop.SupportingExperiences...)
		drop.Status = domain.MemoryStatusSuperseded
		drop.UpdatedAt = now
		domain.NormalizeMemoryRecord(keep, now)
		_ = s.records.Save(drop)
		if err := s.records.Save(keep); err != nil {
			op.Status = domain.LearningOpRejected
			s.saveOp(op)
			return err
		}
		op.TargetID = keep.ID
		op.Status = domain.LearningOpAccepted
	default:
		op.Status = domain.LearningOpRejected
		op.Reason = "skill operations are handled by SkillService"
		s.saveOp(op)
		return fmt.Errorf("not a memory operation: %s", op.Kind)
	}
	s.saveOp(op)
	return nil
}

func (s *MemoryService) saveOp(op *domain.LearningOperation) {
	if s.ops != nil && op != nil {
		_ = s.ops.Save(op)
	}
}

func recordFromPayload(op *domain.LearningOperation, now time.Time) (*domain.MemoryRecord, error) {
	body := payloadString(op.Payload, "body")
	if body == "" {
		body = payloadString(op.Payload, "content")
	}
	if strings.TrimSpace(body) == "" {
		return nil, fmt.Errorf("memory upsert requires body")
	}
	id := payloadString(op.Payload, "id")
	if id == "" {
		id = domain.NewULID(domain.IDPrefixMem)
	}
	rec := &domain.MemoryRecord{
		ID:                    id,
		Type:                  payloadString(op.Payload, "type"),
		Subject:               payloadString(op.Payload, "subject"),
		Predicate:             payloadString(op.Payload, "predicate"),
		Object:                payloadString(op.Payload, "object"),
		Body:                  body,
		Scope:                 domain.MemoryScope{Level: payloadString(op.Payload, "scope"), Project: payloadString(op.Payload, "project")},
		Status:                domain.MemoryStatusLearned,
		Source:                "consolidator",
		SupportingExperiences: op.Evidence,
		EvidenceCount:         len(op.Evidence),
		Confidence:            0.7,
		Stability:             0.5,
		Utility:               0.5,
	}
	if rec.Scope.Level == "" {
		rec.Scope.Level = domain.MemoryScopeUser
	}
	domain.NormalizeMemoryRecord(rec, now)
	return rec, nil
}

func payloadString(payload map[string]any, key string) string {
	if payload == nil {
		return ""
	}
	v, ok := payload[key]
	if !ok || v == nil {
		return ""
	}
	s, _ := v.(string)
	return strings.TrimSpace(s)
}
