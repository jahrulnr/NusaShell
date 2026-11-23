package agent

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/jahrulnr/nusashell/infrastructure/domain/enum"
	"github.com/jahrulnr/nusashell/infrastructure/domain/event"
	"github.com/jahrulnr/nusashell/infrastructure/domain/valueobject"
	"github.com/jahrulnr/nusashell/infrastructure/service/agent/acp"
)

// usageUpdate decodes a usage_update the way the process delivers it.
func usageUpdate(t *testing.T, body string) acp.SessionUpdate {
	t.Helper()
	var u acp.SessionUpdate
	if err := json.Unmarshal([]byte(body), &u); err != nil {
		t.Fatal(err)
	}
	return u
}

func contextReports(events []event.TurnEvent) []valueobject.AgentContext {
	var out []valueobject.AgentContext
	for _, ev := range events {
		if ev.Kind() == enum.TurnEventContext {
			out = append(out, ev.AgentContext())
		}
	}
	return out
}

func TestUsageUpdateReportsTheAgentsContext(t *testing.T) {
	var got []event.TurnEvent
	sink := newTurnSink(context.Background(), func(ev event.TurnEvent) { got = append(got, ev) }, nil)
	sink.update(usageUpdate(t, `{"sessionUpdate":"usage_update","used":53000,"size":200000,"cost":{"amount":0.0421,"currency":"usd"}}`))
	sink.update(usageUpdate(t, `{"sessionUpdate":"usage_update","used":61000,"size":200000}`))

	reports := contextReports(got)
	if len(reports) != 2 {
		t.Fatalf("context events = %d, want one per update", len(reports))
	}
	if r := reports[0]; r.Used() != 53000 || r.Size() != 200000 {
		t.Fatalf("first report = %d/%d", r.Used(), r.Size())
	}
	if amount, currency, ok := reports[0].Cost(); !ok || amount != 0.0421 || currency != "USD" {
		t.Fatalf("cost = %v %q %v", amount, currency, ok)
	}
	if _, _, ok := reports[1].Cost(); ok {
		t.Fatal("a report without a cost must carry none")
	}
}

func TestUsageUpdateWithoutAWindowSaysNothing(t *testing.T) {
	var got []event.TurnEvent
	sink := newTurnSink(context.Background(), func(ev event.TurnEvent) { got = append(got, ev) }, nil)
	sink.update(usageUpdate(t, `{"sessionUpdate":"usage_update","used":500}`))
	sink.update(usageUpdate(t, `{"sessionUpdate":"usage_update","used":-1,"size":100}`))
	if len(got) != 0 {
		t.Fatalf("unusable reports produced %d events", len(got))
	}
}

func TestUsageUpdateWaitsOutASteer(t *testing.T) {
	var got []event.TurnEvent
	sink := newTurnSink(context.Background(), func(ev event.TurnEvent) { got = append(got, ev) }, nil)
	if !sink.gate() {
		t.Fatal("gate refused")
	}
	sink.update(usageUpdate(t, `{"sessionUpdate":"usage_update","used":10,"size":100}`))
	if len(got) != 0 {
		t.Fatal("an update went out while a steer was in flight")
	}
	sink.release(valueobject.UserInput{Text: "more"}, true, nil)
	// The owner's input lands first, then what the agent said meanwhile.
	if len(got) != 2 || got[0].Kind() != enum.TurnEventSteer || got[1].Kind() != enum.TurnEventContext {
		t.Fatalf("events after release = %v", got)
	}
}
