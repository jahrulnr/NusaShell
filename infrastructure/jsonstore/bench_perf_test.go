package jsonstore

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"nusashell/domain"
)

func benchConv(id string, msgCount, msgSize int) *domain.Conversation {
	c := &domain.Conversation{ID: id, Title: "bench", Status: "idle", CreatedAt: time.Now(), UpdatedAt: time.Now()}
	payload := strings.Repeat("x", msgSize)
	for i := 0; i < msgCount; i++ {
		role := domain.RoleUser
		if i%2 == 1 {
			role = domain.RoleAssistant
		}
		c.Messages = append(c.Messages, domain.Message{
			ID: fmt.Sprintf("m%d", i), Role: role, Content: payload, CreatedAt: time.Now(),
		})
	}
	return c
}

// ~40MB transcript: 20k messages x 2KB
func BenchmarkSaveLargeTranscript(b *testing.B) {
	s, err := New(b.TempDir())
	if err != nil {
		b.Fatal(err)
	}
	c := benchConv("conv_bench", 20000, 2048)
	if err := s.Save(c); err != nil {
		b.Fatal(err)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// simulate a tool-result patch + metadata touch per round
		last := len(c.Messages) - 1
		c.Messages[last].Reasoning = fmt.Sprintf("r%d", i)
		c.Touch()
		if err := s.Save(c); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkListAllConversations(b *testing.B) {
	s, err := New(b.TempDir())
	if err != nil {
		b.Fatal(err)
	}
	// 524 convs, avg ~1MB each (500 msgs x 2KB)
	for i := 0; i < 524; i++ {
		c := benchConv(fmt.Sprintf("conv_%d", i), 500, 2048)
		if err := s.Save(c); err != nil {
			b.Fatal(err)
		}
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = s.List()
	}
}
