package corevideo

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jahrulnr/nusashell/infrastructure/domain/aggregate"
	"github.com/jahrulnr/nusashell/infrastructure/domain/entity"
	"github.com/jahrulnr/nusashell/infrastructure/domain/enum"
	dservice "github.com/jahrulnr/nusashell/infrastructure/domain/service"
	coremedia "github.com/jahrulnr/nusashell/infrastructure/service/core/media"
)

var mp4 = []byte("\x00\x00\x00\x18ftypmp42\x00\x00\x00\x00mp42isom")

type redirect struct{ srv *httptest.Server }

func (r redirect) RoundTrip(req *http.Request) (*http.Response, error) {
	u, _ := url.Parse(r.srv.URL)
	req.URL.Scheme, req.URL.Host = u.Scheme, u.Host
	return http.DefaultTransport.RoundTrip(req)
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

// OpenRouter: submit the JSON job with frames and an audio reference,
// poll until completed, then fetch the unsigned URL with the key.
func TestOpenRouterVideoJob(t *testing.T) {
	var polls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer k" {
			t.Errorf("%s without key", r.URL.Path)
		}
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/videos":
			var body struct {
				Model    string `json:"model"`
				Duration int    `json:"duration"`
				Frames   []struct {
					Type      string `json:"type"`
					FrameType string `json:"frame_type"`
				} `json:"frame_images"`
				Refs []struct {
					Type     string `json:"type"`
					AudioURL struct {
						URL string `json:"url"`
					} `json:"audio_url"`
				} `json:"input_references"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body.Model != "google/veo-3.1" || body.Duration != 8 || len(body.Frames) != 1 || body.Frames[0].FrameType != "first_frame" {
				t.Errorf("body = %+v", body)
			}
			if len(body.Refs) != 1 || body.Refs[0].Type != "audio_url" || !strings.HasPrefix(body.Refs[0].AudioURL.URL, "data:audio/mpeg;base64,") {
				t.Errorf("refs = %+v", body.Refs)
			}
			w.WriteHeader(http.StatusAccepted)
			_, _ = io.WriteString(w, `{"id":"job1","polling_url":"x","status":"pending"}`)
		case r.URL.Path == "/api/v1/videos/job1":
			if polls.Add(1) < 3 {
				_, _ = io.WriteString(w, `{"id":"job1","status":"in_progress","progress":40}`)
				return
			}
			_, _ = io.WriteString(w, `{"id":"job1","status":"completed","unsigned_urls":["/api/v1/videos/job1/content?index=0"]}`)
		case r.URL.Path == "/api/v1/videos/job1/content":
			w.Header().Set("Content-Type", "video/mp4")
			_, _ = w.Write(mp4)
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
	}))
	defer srv.Close()
	s := New(coremedia.New(coremedia.Options{Client: &http.Client{Transport: redirect{srv}}}), time.Millisecond)
	var seen []string
	got, err := s.GenerateVideo(context.Background(), provider(t, "https://openrouter.ai/api/v1", enum.StyleResponses), model("google/veo-3.1"),
		dservice.VideoRequest{
			Prompt: "waves", Duration: 8,
			FirstFrame: &dservice.Media{MediaType: "image/png", Data: []byte("png")},
			References: []dservice.Media{{MediaType: "audio/mpeg", Data: []byte("mp3")}},
		}, func(s string) { seen = append(seen, s) })
	if err != nil {
		t.Fatal(err)
	}
	if got.MediaType != "video/mp4" || string(got.Data) != string(mp4) {
		t.Fatalf("got %s %q", got.MediaType, got.Data)
	}
	if len(seen) < 3 || seen[0] != "submitted" || seen[1] != "in_progress 40%" || seen[len(seen)-1] != "completed" {
		t.Fatalf("progress = %q", seen)
	}
}

func TestVideoFailureAndWait(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost:
			_, _ = io.WriteString(w, `{"id":"j","status":"pending"}`)
		case strings.HasSuffix(r.URL.Path, "/videos/j"):
			_, _ = io.WriteString(w, `{"id":"j","status":"failed","error":"content policy"}`)
		case strings.HasSuffix(r.URL.Path, "/videos/slow"):
			_, _ = io.WriteString(w, `{"id":"slow","status":"in_progress"}`)
		}
	}))
	defer srv.Close()
	s := New(coremedia.New(coremedia.Options{}), time.Millisecond)
	_, err := s.GenerateVideo(context.Background(), provider(t, srv.URL+"/v1", enum.StyleCompletion), model("m"), dservice.VideoRequest{Prompt: "p"}, nil)
	if err == nil || !strings.Contains(err.Error(), "content policy") {
		t.Fatalf("err = %v", err)
	}

	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			_, _ = io.WriteString(w, `{"id":"slow"}`)
			return
		}
		_, _ = io.WriteString(w, `{"id":"slow","status":"in_progress"}`)
	}))
	defer slow.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	_, err = s.GenerateVideo(ctx, provider(t, slow.URL+"/v1", enum.StyleCompletion), model("m"), dservice.VideoRequest{Prompt: "p"}, nil)
	if err == nil || !strings.Contains(err.Error(), "still rendering") || !strings.Contains(err.Error(), "slow") {
		t.Fatalf("err = %v", err)
	}
}

// OpenAI's dialect: multipart with seconds and a pixel size; the file is
// served at videos/{id}/content.
func TestOpenAIVideo(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost:
			if err := r.ParseMultipartForm(1 << 20); err != nil {
				t.Fatal(err)
			}
			if r.FormValue("seconds") != "4" || r.FormValue("size") != "720x1280" || len(r.MultipartForm.File["input_reference"]) != 1 {
				t.Errorf("form = %v", r.MultipartForm.Value)
			}
			_, _ = io.WriteString(w, `{"id":"video_1","status":"queued"}`)
		case r.URL.Path == "/v1/videos/video_1":
			_, _ = io.WriteString(w, `{"id":"video_1","status":"completed"}`)
		case r.URL.Path == "/v1/videos/video_1/content":
			_, _ = w.Write(mp4)
		}
	}))
	defer srv.Close()
	s := New(coremedia.New(coremedia.Options{Client: &http.Client{Transport: redirect{srv}}}), time.Millisecond)
	got, err := s.GenerateVideo(context.Background(), provider(t, "https://api.openai.com/v1", enum.StyleResponses), model("sora-2"),
		dservice.VideoRequest{Prompt: "p", Duration: 4, AspectRatio: "9:16", FirstFrame: &dservice.Media{MediaType: "image/png", Data: []byte("x")}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got.MediaType != "video/mp4" {
		t.Fatalf("type = %s", got.MediaType)
	}
}

// Gemini: predictLongRunning, poll the operation, fetch the sample URI.
func TestGeminiVideo(t *testing.T) {
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("x-goog-api-key") != "k" {
			t.Errorf("%s without key", r.URL.Path)
		}
		switch r.URL.Path {
		case "/v1beta/models/veo-3.1:predictLongRunning":
			raw, _ := io.ReadAll(r.Body)
			if !strings.Contains(string(raw), `"aspectRatio":"16:9"`) || !strings.Contains(string(raw), `"bytesBase64Encoded"`) {
				t.Errorf("body = %s", raw)
			}
			_, _ = io.WriteString(w, `{"name":"models/veo-3.1/operations/op1"}`)
		case "/v1beta/models/veo-3.1/operations/op1":
			_, _ = io.WriteString(w, `{"done":true,"response":{"generateVideoResponse":{"generatedSamples":[{"video":{"uri":"`+srv.URL+`/v1beta/files/f:download?alt=media"}}]}}}`)
		case "/v1beta/files/f:download":
			_, _ = w.Write(mp4)
		default:
			t.Errorf("unexpected %s", r.URL.Path)
		}
	}))
	defer srv.Close()
	s := New(coremedia.New(coremedia.Options{}), time.Millisecond)
	got, err := s.GenerateVideo(context.Background(), provider(t, srv.URL+"/v1beta", enum.StyleGenerateContent), model("veo-3.1"),
		dservice.VideoRequest{Prompt: "p", AspectRatio: "16:9", FirstFrame: &dservice.Media{MediaType: "image/png", Data: []byte("x")}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if string(got.Data) != string(mp4) {
		t.Fatalf("data = %q", got.Data)
	}
}
