package application

import (
	"fmt"
	"sort"
	"strings"

	"nusashell/domain"
)

// ListConversations returns visible conversation rooms (excluding self and pipeline rooms),
// sorted by UpdatedAt descending, with pagination support.
func (a *App) ListConversations(currentConvID string, limit, offset int) (int, []ConversationSummaryDTO, error) {
	if a.Conversations == nil {
		return 0, nil, fmt.Errorf("conversation store not available")
	}

	all := a.Conversations.List()
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
		return total, []ConversationSummaryDTO{}, nil
	}

	end := offset + limit
	if limit <= 0 {
		end = offset + 20
	}
	if end > total {
		end = total
	}

	paged := visible[offset:end]
	dtos := make([]ConversationSummaryDTO, 0, len(paged))
	for _, c := range paged {
		dtos = append(dtos, ConversationSummaryDTO{
			ID:        c.ID,
			Title:     c.Title,
			Summary:   c.Summary,
			Status:    c.Status,
			UpdatedAt: c.UpdatedAt.Format("2006-01-02T15:04:05Z07:00"),
		})
	}

	return total, dtos, nil
}

// SearchConversations searches visible conversation rooms by title or summary
// (case-insensitive substring match), sorted by UpdatedAt descending, with pagination support.
func (a *App) SearchConversations(currentConvID, query string, limit, offset int) (int, []ConversationSummaryDTO, error) {
	if a.Conversations == nil {
		return 0, nil, fmt.Errorf("conversation store not available")
	}

	q := strings.ToLower(strings.TrimSpace(query))
	all := a.Conversations.List()
	matched := make([]*domain.Conversation, 0, len(all))
	for _, c := range all {
		if c == nil || c.HiddenFromRoomList() {
			continue
		}
		if currentConvID != "" && c.ID == currentConvID {
			continue
		}
		if q != "" {
			titleMatch := strings.Contains(strings.ToLower(c.Title), q)
			summaryMatch := strings.Contains(strings.ToLower(c.Summary), q)
			if !titleMatch && !summaryMatch {
				continue
			}
		}
		matched = append(matched, c)
	}

	sort.Slice(matched, func(i, j int) bool {
		return matched[i].UpdatedAt.After(matched[j].UpdatedAt)
	})

	total := len(matched)
	if offset < 0 {
		offset = 0
	}
	if offset >= total {
		return total, []ConversationSummaryDTO{}, nil
	}

	end := offset + limit
	if limit <= 0 {
		end = offset + 20
	}
	if end > total {
		end = total
	}

	paged := matched[offset:end]
	dtos := make([]ConversationSummaryDTO, 0, len(paged))
	for _, c := range paged {
		dtos = append(dtos, ConversationSummaryDTO{
			ID:        c.ID,
			Title:     c.Title,
			Summary:   c.Summary,
			Status:    c.Status,
			UpdatedAt: c.UpdatedAt.Format("2006-01-02T15:04:05Z07:00"),
		})
	}

	return total, dtos, nil
}

// SendConversationMessage sends a message to another conversation room via announcement.
func (a *App) SendConversationMessage(currentConvID, targetConvID, content string) error {
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
	if a.Conversations == nil {
		return fmt.Errorf("conversation store not available")
	}

	target, err := a.Conversations.Get(targetConvID)
	if err != nil || target == nil {
		return fmt.Errorf("conversation %q not found", targetConvID)
	}
	if target.HiddenFromRoomList() {
		return fmt.Errorf("conversation %q is not a visible agent room", targetConvID)
	}

	ann := newAnnouncement(
		"peer_message",
		domain.AnnouncementPeerMessageArgs(currentConvID),
		domain.AnnouncementPeerMessageMessage(currentConvID, content),
	)
	a.publishAnnouncement(targetConvID, ann)

	return nil
}

// List implements ConversationMessenger.
func (a *App) List(currentConvID string, limit, offset int) (int, []ConversationSummaryDTO, error) {
	return a.ListConversations(currentConvID, limit, offset)
}

// Search implements ConversationMessenger.
func (a *App) Search(currentConvID, query string, limit, offset int) (int, []ConversationSummaryDTO, error) {
	return a.SearchConversations(currentConvID, query, limit, offset)
}

// Send implements ConversationMessenger.
func (a *App) Send(currentConvID, targetConvID, content string) error {
	return a.SendConversationMessage(currentConvID, targetConvID, content)
}
