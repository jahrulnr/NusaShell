package coreembedding

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/jahrulnr/nusashell/infrastructure/domain/aggregate"
	"github.com/jahrulnr/nusashell/infrastructure/domain/entity"
	"github.com/jahrulnr/nusashell/infrastructure/domain/enum"
	dservice "github.com/jahrulnr/nusashell/infrastructure/domain/service"
	coremedia "github.com/jahrulnr/nusashell/infrastructure/service/core/media"
)

func provider(t *testing.T, endpoint string, style enum.Style) *aggregate.Provider {
	t.Helper()
	p, err := aggregate.NewProvider("p", "", endpoint, style)
	if err != nil {
		t.Fatal(err)
	}
	p.SetApiKey("k")
	return &p
}

func model(id string) entity.Model {
	m, _ := entity.NewModel(id)
	return m
}

// Batches split at maxBatch and answers land by index, whatever order the
// host sends them in. OpenAI itself gets no input_type.
func TestRESTBatchesAndOrdersByIndex(t *testing.T) {
	requests := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		var body struct {
			Input     []string `json:"input"`
			InputType *string  `json:"input_type"`
			Format    string   `json:"encoding_format"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		if r.URL.Path != "/v1/embeddings" || body.InputType != nil || body.Format != "float" || len(body.Input) > maxBatch {
			t.Errorf("%s %+v", r.URL.Path, body)
		}
		var data []string
		for i, v := range slices.Backward(body.Input) { // reversed on purpose
			var n int
			fmt.Sscanf(v, "t%d", &n)
			data = append(data, fmt.Sprintf(`{"index":%d,"embedding":[%d,1]}`, i, n))
		}
		fmt.Fprintf(w, `{"data":[%s]}`, strings.Join(data, ","))
	}))
	defer srv.Close()
	texts := make([]string, maxBatch+3)
	for i := range texts {
		texts[i] = fmt.Sprintf("t%d", i)
	}
	got, err := New(coremedia.New(coremedia.Options{})).Embed(context.Background(), provider(t, srv.URL+"/v1", enum.StyleResponses),
		model("text-embedding-3-small"), texts, dservice.EmbedDocument)
	if err != nil {
		t.Fatal(err)
	}
	if requests != 2 || len(got) != len(texts) {
		t.Fatalf("requests=%d vectors=%d", requests, len(got))
	}
	for i, v := range got {
		if v[0] != float32(i) {
			t.Fatalf("vector %d = %v", i, v)
		}
	}
}

func TestRESTRejectsShortAnswer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"data":[{"index":0,"embedding":[1]}]}`)
	}))
	defer srv.Close()
	_, err := New(coremedia.New(coremedia.Options{})).Embed(context.Background(), provider(t, srv.URL+"/v1", enum.StyleResponses),
		model("e"), []string{"a", "b"}, dservice.EmbedQuery)
	if err == nil {
		t.Fatal("a short answer was accepted")
	}
}

func TestGeminiAsksForTheTask(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Requests []struct {
				Model    string `json:"model"`
				TaskType string `json:"taskType"`
			} `json:"requests"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		if !strings.HasSuffix(r.URL.Path, "/models/gemini-embedding-001:batchEmbedContents") || len(body.Requests) != 1 ||
			body.Requests[0].TaskType != "RETRIEVAL_QUERY" || body.Requests[0].Model != "models/gemini-embedding-001" {
			t.Errorf("%s %+v", r.URL.Path, body)
		}
		fmt.Fprint(w, `{"embeddings":[{"values":[0.5,0.25]}]}`)
	}))
	defer srv.Close()
	got, err := New(coremedia.New(coremedia.Options{})).Embed(context.Background(), provider(t, srv.URL+"/v1beta", enum.StyleGenerateContent),
		model("gemini-embedding-001"), []string{"q"}, dservice.EmbedQuery)
	if err != nil || len(got) != 1 || got[0][1] != 0.25 {
		t.Fatalf("%v %v", got, err)
	}
}
