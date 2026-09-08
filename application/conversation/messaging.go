package conversation

import (
	"fmt"
	"sort"
	"strings"

	"nusashell/domain"
)

// SummaryDTO is the compact room card used by the conversation tool.
type SummaryDTO struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	Summary   string `json:"summary,omitempty"`
	Status    string `json:"status,omitempty"`
	UpdatedAt string `json:"updated_at"`
	Match     string `json:"match,omitempty"` // title|id|summary|message
}

// ListRooms returns visible conversation rooms (excluding self and pipeline
// rooms), sorted by UpdatedAt descending, with pagination support.
func (s *Service) ListRooms(currentConvID string, limit, offset int) (int, []SummaryDTO, error) {
	if s.store == nil {
		return 0, nil, fmt.Errorf("conversation store not available")
	}

	all := s.store.List()
	visible := make([]*domain.Conversation, 0, len(all))
	for _, c := range all {
		if c == nil || c.HiddenFromRoomList() {
			continue
		}
		if currentConvID != "" && c.ID == currentConvID {
			continue
		}
		visible = append(visible, c)
	}

	sort.Slice(visible, func(i, j int) bool {
		return visible[i].UpdatedAt.After(visible[j].UpdatedAt)
	})

	total := len(visible)
	if offset < 0 {
		offset = 0
	}
	if offset >= total {
		return total, []SummaryDTO{}, nil
	}

	end := offset + limit
	if limit <= 0 {
		end = offset + 20
	}
	if end > total {
		end = total
	}

	paged := visible[offset:end]
	dtos := make([]SummaryDTO, 0, len(paged))
	for _, c := range paged {
		dtos = append(dtos, SummaryDTO{
			ID:        c.ID,
			Title:     c.Title,
			Summary:   c.Summary,
			Status:    c.Status,
			UpdatedAt: c.UpdatedAt.Format("2006-01-02T15:04:05Z07:00"),
		})
	}

	return total, dtos, nil
}

// SearchRooms searches visible conversation rooms by id, title, summary, or
// visible user/assistant message text (case-insensitive substring), sorted by
// UpdatedAt descending. Match names the highest-priority field that hit
// (id > title > summary > message).
func (s *Service) SearchRooms(currentConvID, query string, limit, offset int) (int, []SummaryDTO, error) {
	if s.store == nil {
		return 0, nil, fmt.Errorf("conversation store not available")
	}

	q := strings.ToLower(strings.TrimSpace(query))
	all := s.store.List()
	type hit struct {
		c     *domain.Conversation
		match string
	}
	matched := make([]hit, 0, len(all))
	for _, c := range all {
		if c == nil || c.HiddenFromRoomList() {
			continue
		}
		if currentConvID != "" && c.ID == currentConvID {
			continue
		}
		match := ""
		if q != "" {
			switch {
			case strings.Contains(strings.ToLower(c.ID), q):
				match = "id"
			case strings.Contains(strings.ToLower(c.Title), q):
				match = "title"
			case strings.Contains(strings.ToLower(c.Summary), q):
				match = "summary"
			case messageTextMatch(c.Messages, q):
				match = "message"
			default:
				continue
			}
		}
		matched = append(matched, hit{c: c, match: match})
	}

	sort.Slice(matched, func(i, j int) bool {
		return matched[i].c.UpdatedAt.After(matched[j].c.UpdatedAt)
	})

	total := len(matched)
	if offset < 0 {
		offset = 0
	}
	if offset >= total {
		return total, []SummaryDTO{}, nil
	}

	end := offset + limit
	if limit <= 0 {
		end = offset + 20
	}
	if end > total {
		end = total
	}

	paged := matched[offset:end]
	dtos := make([]SummaryDTO, 0, len(paged))
	for _, h := range paged {
		dtos = append(dtos, SummaryDTO{
			ID:        h.c.ID,
			Title:     h.c.Title,
			Summary:   h.c.Summary,
			Status:    h.c.Status,
			UpdatedAt: h.c.UpdatedAt.Format("2006-01-02T15:04:05Z07:00"),
			Match:     h.match,
		})
	}

	return total, dtos, nil
}

// SendPeer delivers a peer_message announcement to another visible room.
func (s *Service) SendPeer(currentConvID, targetConvID, content string) error {
	targetConvID = strings.TrimSpace(targetConvID)
	if targetConvID == "" {
		return fmt.Errorf("target conversation id is required")
	}
	content = strings.TrimSpace(content)
	if content == "" {
		return fmt.Errorf("message content is required")
	}
	if currentConvID != "" && currentConvID == targetConvID {
		return fmt.Errorf("cannot send message to self")
	}
	if s.store == nil {
		return fmt.Errorf("conversation store not available")
	}

	target, err := s.store.Get(targetConvID)
	if err != nil || target == nil {
		return fmt.Errorf("conversation %q not found", targetConvID)
	}
	if target.HiddenFromRoomList() {
		return fmt.Errorf("conversation %q is not a visible agent room", targetConvID)
	}

	if s.announce == nil {
		return fmt.Errorf("conversation announcer not available")
	}
	s.announce(targetConvID, currentConvID, content)
	return nil
}
