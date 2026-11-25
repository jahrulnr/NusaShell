package agent

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"strings"
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

// keptMedia is one payload the sink handed the store.
type keptMedia struct {
	name      string
	mediaType string
	raw       []byte
}

// mediaSink builds a sink whose saved media land in the returned slice, and
// feeds it one content block the way the agent's turn would.
func mediaSink(t *testing.T) (*turnSink, *[]keptMedia) {
	t.Helper()
	kept := []keptMedia{}
	sink := newTurnSink(context.Background(), func(event.TurnEvent) {}, nil)
	sink.save = func(_ context.Context, name, mediaType string, raw []byte) (valueobject.Attachment, error) {
		kept = append(kept, keptMedia{name: name, mediaType: mediaType, raw: raw})
		return valueobject.NewAttachment("file:///tmp/kept/"+name, mediaType)
	}
	return sink, &kept
}

func (s *turnSink) addMediaLocked(b acp.ContentBlock) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.addMedia(b)
}

// blob wraps payload bytes the way the agent sends them.
func blob(raw []byte) string { return base64.StdEncoding.EncodeToString(raw) }

// An agent that sends a voice note without a media type is answered with the
// file's own name: the bytes alone read as video, and only the extension says
// it is audio.
func TestKeptMediaSniffsWithTheFilesName(t *testing.T) {
	sink, kept := mediaSink(t)
	sink.addMediaLocked(acp.ContentBlock{Type: "resource", Name: "voice.m4a",
		Resource: &acp.EmbeddedResource{MIMEType: "application/octet-stream",
			Blob: blob([]byte("\x00\x00\x00\x18ftypM4A \x00\x00\x00\x00"))}})
	if len(*kept) != 1 || (*kept)[0].mediaType != "audio/mp4" {
		t.Fatalf("kept = %+v, want audio/mp4", *kept)
	}
}

// A payload with no name at all is still named by its bytes — the sniffer's
// extension tie-break simply has nothing to add.
func TestKeptMediaWithoutANameFallsBackToItsBytes(t *testing.T) {
	sink, kept := mediaSink(t)
	sink.addMediaLocked(acp.ContentBlock{Type: "resource",
		Resource: &acp.EmbeddedResource{MIMEType: "application/octet-stream",
			Blob: blob([]byte("OggS\x00\x02\x00\x00\x00\x00\x00\x00\x00\x00"))}})
	if len(*kept) != 1 || (*kept)[0].mediaType != "audio/ogg" {
		t.Fatalf("kept = %+v, want audio/ogg", *kept)
	}
	if (*kept)[0].name == "" {
		t.Fatal("the store still needs a name for a payload that arrived without one")
	}
}

// A media type the agent states is its own answer and is kept as given: these
// are Ogg bytes, but the agent says wav, and the sink must not overrule it.
func TestKeptMediaKeepsTheDeclaredType(t *testing.T) {
	sink, kept := mediaSink(t)
	sink.addMediaLocked(acp.ContentBlock{Type: "audio", Name: "voice.wav", MIMEType: "audio/wav",
		Data: blob([]byte("OggS\x00\x02\x00\x00\x00\x00\x00\x00\x00\x00"))})
	if len(*kept) != 1 || (*kept)[0].mediaType != "audio/wav" {
		t.Fatalf("kept = %+v, want the declared audio/wav", *kept)
	}
}

// One sniffer names payloads in this package: no caller sniffs content types
// itself, where the extension tie-break would be lost.
func TestNoRawContentTypeSniffingInThePackage(t *testing.T) {
	needle := "Detect" + "ContentType" // split so this file never matches itself
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		raw, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(raw), needle) {
			t.Fatalf("%s sniffs content types itself; call dservice.SniffMediaType instead", name)
		}
	}
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
