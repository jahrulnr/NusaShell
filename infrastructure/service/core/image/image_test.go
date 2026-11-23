package coreimage

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/jahrulnr/nusashell/infrastructure/domain/aggregate"
	"github.com/jahrulnr/nusashell/infrastructure/domain/entity"
	"github.com/jahrulnr/nusashell/infrastructure/domain/enum"
	dservice "github.com/jahrulnr/nusashell/infrastructure/domain/service"
	apperror "github.com/jahrulnr/nusashell/infrastructure/error"
	coremedia "github.com/jahrulnr/nusashell/infrastructure/service/core/media"
)

var png = []byte("\x89PNG\r\n\x1a\n0000IHDR")

// redirect sends every request to srv, keeping the path, so a provider
// keyed off its real host (openrouter.ai) can be served locally.
type redirect struct{ srv *httptest.Server }

func (r redirect) RoundTrip(req *http.Request) (*http.Response, error) {
	u, _ := url.Parse(r.srv.URL)
	req.URL.Scheme, req.URL.Host = u.Scheme, u.Host
	return http.DefaultTransport.RoundTrip(req)
}

func service(srv *httptest.Server) *Service {
	return New(coremedia.New(coremedia.Options{Client: &http.Client{Transport: redirect{srv}}}))
}

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

func TestOpenRouterImages(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/images" || r.Header.Get("Authorization") != "Bearer k" {
			t.Errorf("request %s auth %q", r.URL.Path, r.Header.Get("Authorization"))
		}
		var body struct {
			Model          string `json:"model"`
			Prompt         string `json:"prompt"`
			N              int    `json:"n"`
			AspectRatio    string `json:"aspect_ratio"`
			ResponseFormat string `json:"response_format"`
			Refs           []struct {
				Type     string `json:"type"`
				ImageURL struct {
					URL string `json:"url"`
				} `json:"image_url"`
			} `json:"input_references"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body.Model != "recraft/x" || body.Prompt != "a fox" || body.N != 2 || body.AspectRatio != "16:9" || body.ResponseFormat != "b64_json" {
			t.Errorf("body = %+v", body)
		}
		if len(body.Refs) != 1 || body.Refs[0].Type != "image_url" || !strings.HasPrefix(body.Refs[0].ImageURL.URL, "data:image/png;base64,") {
			t.Errorf("refs = %+v", body.Refs)
		}
		b64 := base64.StdEncoding.EncodeToString(png)
		_, _ = io.WriteString(w, `{"created":1,"data":[{"b64_json":"`+b64+`","media_type":"image/png"},{"b64_json":"`+b64+`"}]}`)
	}))
	defer srv.Close()
	got, err := service(srv).GenerateImage(context.Background(), provider(t, "https://openrouter.ai/api/v1", enum.StyleResponses), model("recraft/x"),
		dservice.ImageRequest{Prompt: "a fox", AspectRatio: "16:9", Count: 2, References: []dservice.Media{{MediaType: "image/png", Data: png}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].MediaType != "image/png" || got[1].MediaType != "image/png" || string(got[1].Data) != string(png) {
		t.Fatalf("got %+v", got)
	}
}

// The compatible long tail answers /images/generations, sometimes with a
// URL; the adapter fetches it, with the key only on the provider's host.
func TestCompatibleGenerationsDownloadsURL(t *testing.T) {
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/images/generations":
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body["aspect_ratio"] != "1:1" || body["model"] != "flux" {
				t.Errorf("body = %v", body)
			}
			_, _ = io.WriteString(w, `{"data":[{"url":"`+srv.URL+`/files/a.png"}]}`)
		case "/files/a.png":
			if r.Header.Get("Authorization") != "Bearer k" {
				t.Errorf("same-host download lost its key")
			}
			_, _ = w.Write(png)
		default:
			t.Errorf("unexpected %s", r.URL.Path)
		}
	}))
	defer srv.Close()
	s := New(coremedia.New(coremedia.Options{}))
	got, err := s.GenerateImage(context.Background(), provider(t, srv.URL+"/v1", enum.StyleCompletion), model("flux"),
		dservice.ImageRequest{Prompt: "p", AspectRatio: "1:1"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].MediaType != "image/png" {
		t.Fatalf("got %+v", got)
	}
}

func TestOpenAIEditsSendFilesAndMapAspect(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/images/edits" {
			t.Errorf("path = %s", r.URL.Path)
		}
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Fatal(err)
		}
		if r.FormValue("size") != "1024x1536" || r.FormValue("model") != "gpt-image-1" || len(r.MultipartForm.File["image[]"]) != 2 {
			t.Errorf("form = %v files %v", r.MultipartForm.Value, r.MultipartForm.File)
		}
		_, _ = io.WriteString(w, `{"data":[{"b64_json":"`+base64.StdEncoding.EncodeToString(png)+`"}]}`)
	}))
	defer srv.Close()
	ref := dservice.Media{MediaType: "image/png", Data: png}
	_, err := service(srv).GenerateImage(context.Background(), provider(t, "https://api.openai.com/v1", enum.StyleResponses), model("gpt-image-1"),
		dservice.ImageRequest{Prompt: "p", AspectRatio: "9:16", References: []dservice.Media{ref, ref}})
	if err != nil {
		t.Fatal(err)
	}
}

func TestGeminiImage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1beta/models/gemini-3.1-flash-image:generateContent" || r.Header.Get("x-goog-api-key") != "k" {
			t.Errorf("request %s key %q", r.URL.Path, r.Header.Get("x-goog-api-key"))
		}
		raw, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(raw), `"responseModalities":["TEXT","IMAGE"]`) || !strings.Contains(string(raw), `"aspectRatio":"4:3"`) {
			t.Errorf("body = %s", raw)
		}
		_, _ = io.WriteString(w, `{"candidates":[{"content":{"parts":[{"text":"here"},{"inlineData":{"mimeType":"image/png","data":"`+base64.StdEncoding.EncodeToString(png)+`"}}]}}]}`)
	}))
	defer srv.Close()
	got, err := New(coremedia.New(coremedia.Options{})).GenerateImage(context.Background(),
		provider(t, srv.URL+"/v1beta", enum.StyleGenerateContent), model("google/gemini-3.1-flash-image"),
		dservice.ImageRequest{Prompt: "p", AspectRatio: "4:3"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || string(got[0].Data) != string(png) {
		t.Fatalf("got %+v", got)
	}
}

func TestImageRejectionKeepsProviderMessage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "3")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = io.WriteString(w, `{"error":{"message":"slow down","code":429}}`)
	}))
	defer srv.Close()
	_, err := service(srv).GenerateImage(context.Background(), provider(t, "https://openrouter.ai/api/v1", enum.StyleResponses), model("m"),
		dservice.ImageRequest{Prompt: "p"})
	var ae *apperror.AppError
	if !errors.As(err, &ae) || ae.StatusCode() != 429 || ae.Message != "slow down" || ae.RetryAfter() == 0 {
		t.Fatalf("err = %#v", err)
	}
	if _, err := service(srv).GenerateImage(context.Background(), provider(t, "https://api.anthropic.com/v1", enum.StyleMessages), model("m"),
		dservice.ImageRequest{Prompt: "p"}); err == nil {
		t.Fatal("messages surface accepted an image request")
	}
}
