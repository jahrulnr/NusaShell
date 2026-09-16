package agent

import (
	"fmt"
	"strings"
	"time"
)

const (
	conversationKeyMaxLen = 128
	conversationKeyTTL    = 10 * time.Minute
)

type lazyTurnRecord struct {
	ConversationID string
	RunID          string
	CreatedAt      time.Time
}

func validateConversationKey(key string) error {
	key = strings.TrimSpace(key)
	if key == "" {
		return fmt.Errorf("conversation_key is required when conversation_id is omitted")
	}
	if len(key) > conversationKeyMaxLen {
		return fmt.Errorf("conversation_key is too long")
	}
	return nil
}

func (a *Service) lazyTurnForKey(key string) (lazyTurnRecord, bool) {
	if a == nil || key == "" {
		return lazyTurnRecord{}, false
	}
	now := time.Now()
	a.lazyTurnsMu.Lock()
	defer a.lazyTurnsMu.Unlock()
	for candidate, record := range a.lazyTurns {
		if now.Sub(record.CreatedAt) > conversationKeyTTL {
			delete(a.lazyTurns, candidate)
		}
	}
	record, ok := a.lazyTurns[key]
	return record, ok
}

func (a *Service) rememberLazyTurn(key string, record lazyTurnRecord) {
	if a == nil || key == "" {
		return
	}
	a.lazyTurnsMu.Lock()
	a.lazyTurns[key] = record
	a.lazyTurnsMu.Unlock()
}
