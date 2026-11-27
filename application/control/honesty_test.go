package appcontrol

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	appjob "github.com/jahrulnr/nusashell/application/job"
)

// conversation_list is List, marshaled. A held row carries the quartet.
// An unheld row omits the four keys. retryAt is present only when the
// agent named a delay.
func TestListCarriesTheHoldOnlyWhileItApplies(t *testing.T) {
	t.Cleanup(func() { appjob.ClearAgentHold("c_other") })
	app, _, _ := setup(t)

	out, err := app.List(context.Background(), "c_self", ListInput{})
	if err != nil {
		t.Fatal(err)
	}
	rows := rowsByID(t, out)
	for _, id := range []string{"c_self", "c_other"} {
		assertNoHold(t, rows[id])
	}

	at := time.Now().Add(time.Second).UnixMilli()
	appjob.NoteAgentHold("c_other", appjob.AgentHold{
		Class: "rate_limited", Retry: "none", RetryAt: at, Message: "rate limit exceeded",
	})
	out, err = app.List(context.Background(), "c_self", ListInput{})
	if err != nil {
		t.Fatal(err)
	}
	rows = rowsByID(t, out)
	assertNoHold(t, rows["c_self"])
	assertHold(t, rows["c_other"], at)

	appjob.NoteAgentHold("c_other", appjob.AgentHold{
		Class: "unknown", Retry: "none", Message: "the compiler exploded",
	})
	out, err = app.List(context.Background(), "c_self", ListInput{})
	if err != nil {
		t.Fatal(err)
	}
	row := rowsByID(t, out)["c_other"]
	if _, ok := row["retryAt"]; ok {
		t.Fatalf("a hold with no named delay wrote retryAt: %s", row["retryAt"])
	}
	var class, msg string
	if json.Unmarshal(row["agentClass"], &class) != nil || class != "unknown" {
		t.Fatalf("agentClass = %s", row["agentClass"])
	}
	if json.Unmarshal(row["agentMessage"], &msg) != nil || msg != "the compiler exploded" {
		t.Fatalf("agentMessage = %s", row["agentMessage"])
	}
	assertNoHold(t, rowsByID(t, out)["c_self"])
}

func rowsByID(t *testing.T, out map[string]any) map[string]map[string]json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(out["threads"])
	if err != nil {
		t.Fatal(err)
	}
	var rows []map[string]json.RawMessage
	if err := json.Unmarshal(raw, &rows); err != nil {
		t.Fatal(err)
	}
	byID := map[string]map[string]json.RawMessage{}
	for _, row := range rows {
		var id string
		if json.Unmarshal(row["id"], &id) != nil || id == "" {
			t.Fatalf("row has no id: %s", raw)
		}
		byID[id] = row
	}
	return byID
}

func assertNoHold(t *testing.T, row map[string]json.RawMessage) {
	t.Helper()
	for _, key := range []string{"agentClass", "retry", "retryAt", "agentMessage"} {
		if _, ok := row[key]; ok {
			t.Fatalf("an unheld row wrote %s: %s", key, row[key])
		}
	}
}

func assertHold(t *testing.T, row map[string]json.RawMessage, at int64) {
	t.Helper()
	var class, retry, msg string
	var retryAt int64
	if json.Unmarshal(row["agentClass"], &class) != nil || class != "rate_limited" {
		t.Fatalf("agentClass = %s", row["agentClass"])
	}
	if json.Unmarshal(row["retry"], &retry) != nil || retry != "none" {
		t.Fatalf("retry = %s", row["retry"])
	}
	if json.Unmarshal(row["retryAt"], &retryAt) != nil || retryAt != at {
		t.Fatalf("retryAt = %s, want %d", row["retryAt"], at)
	}
	if json.Unmarshal(row["agentMessage"], &msg) != nil || msg != "rate limit exceeded" {
		t.Fatalf("agentMessage = %s", row["agentMessage"])
	}
	if strings.Contains(msg, "unknown error") || strings.Contains(msg, "agent turn failed") {
		t.Fatalf("message replaced the agent's words: %q", msg)
	}
}
