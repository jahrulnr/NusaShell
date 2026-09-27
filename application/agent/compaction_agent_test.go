package agent

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"

	"nusashell/domain"
)

type recordingLog struct {
	mu    sync.Mutex
	lines []string
}

func (r *recordingLog) log(level, source, format string, args ...any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.lines = append(r.lines, level+" "+source+" "+fmt.Sprintf(format, args...))
}

func (r *recordingLog) count(substr string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := 0
	for _, l := range r.lines {
		if strings.Contains(l, substr) {
			n++
		}
	}
	return n
}

func newTestCompactionPass(summaries []string, logs *recordingLog) (*compactionPass, *compactionChatAdapter) {
	adapter := &compactionChatAdapter{toolCallSummaries: summaries}
	svc := New(Deps{Log: logs.log})
	pc := NewProviderContext(&domain.Provider{Models: []domain.Model{{ID: "model", Context: 200000}}}, adapter)
	pc.Kind = domain.ProviderChat
	return &compactionPass{
		svc: svc, adapter: pc,
		request: ChatRequest{Model: "model", Messages: []ChatMessage{{Role: "user", Content: "summarize"}}},
		budget:  1000, maxBudget: 128000, minChars: 200, convID: "conv-pass",
	}, adapter
}

func TestCompactionPassAcceptsLongSummaryWithoutShortWarning(t *testing.T) {
	logs := &recordingLog{}
	pass, adapter := newTestCompactionPass([]string{compactionTestSummary}, logs)

	if !pass.run(context.Background()) {
		t.Fatalf("pass rejected a valid summary (lastLen=%d, lastErr=%v)", pass.lastLen, pass.lastErr)
	}
	if adapter.calls != 1 {
		t.Fatalf("provider calls = %d, want 1", adapter.calls)
	}
	if pass.summary != compactionTestSummary {
		t.Fatalf("summary = %q, want the provider summary", pass.summary)
	}
	if n := logs.count("short summary"); n != 0 {
		t.Fatalf("logged %d short-summary warnings on success, want 0: %v", n, logs.lines)
	}
	if pass.lastErr != nil {
		t.Fatalf("lastErr = %v, want nil after a successful pass", pass.lastErr)
	}
	if pass.budget != 1000 {
		t.Fatalf("budget = %d, want unchanged 1000", pass.budget)
	}
}

func TestCompactionPassRetriesShortSummaryWithDoubledBudget(t *testing.T) {
	logs := &recordingLog{}
	pass, adapter := newTestCompactionPass([]string{"too short", compactionTestSummary}, logs)

	if !pass.run(context.Background()) {
		t.Fatalf("pass failed after a valid retry (lastLen=%d, lastErr=%v)", pass.lastLen, pass.lastErr)
	}
	if adapter.calls != 2 {
		t.Fatalf("provider calls = %d, want 2", adapter.calls)
	}
	if pass.summary != compactionTestSummary {
		t.Fatalf("summary = %q, want the retried summary", pass.summary)
	}
	if n := logs.count("compaction pass 1 produced short summary (9 chars, min 200) for conv-pass, retrying with budget 2000"); n != 1 {
		t.Fatalf("short-summary warnings for pass 1 = %d, want 1: %v", n, logs.lines)
	}
	if n := logs.count("short summary"); n != 1 {
		t.Fatalf("short-summary warnings = %d, want exactly 1: %v", n, logs.lines)
	}
	if pass.budget != 2000 {
		t.Fatalf("budget = %d, want doubled 2000", pass.budget)
	}
}
