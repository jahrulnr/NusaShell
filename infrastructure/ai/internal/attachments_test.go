package aiutil

import (
	"encoding/json"
	"strings"
	"testing"

	"nusashell/domain"
)

// TestInputImageBlockIncludesDetailField proves that the Responses API
// input_image block includes the required `detail` field. OpenAI's
// ResponseInputImageParam marks `detail` as Required[ImageDetail] —
// omitting it causes HTTP 400 "Field required" from strict providers
// (e.g. when OpenRouter routes to a Responses-API-compatible backend).
//
// Valid values: "auto", "low", "high", "original". Default: "auto".
func TestInputImageBlockIncludesDetailField(t *testing.T) {
	att := domain.Attachment{
		Type:      "image",
		Name:      "cat.png",
		MediaType: "image/png",
		DataURL:   "data:image/png;base64,iVBORw0KGgo=",
	}
	block := InputImageBlock(att)

	if block["type"] != "input_image" {
		t.Errorf("type = %v, want input_image", block["type"])
	}
	if block["image_url"] != att.DataURL {
		t.Errorf("image_url = %v, want %s", block["image_url"], att.DataURL)
	}
	if block["detail"] != "auto" {
		t.Errorf("detail = %v, want auto (required by Responses API)", block["detail"])
	}
}

// TestInputImageBlockSerializesDetail proves the JSON output contains
// the detail field — a map[string]any with the right key is not enough
// if json.Marshal skips it via omitempty.
func TestInputImageBlockSerializesDetail(t *testing.T) {
	att := domain.Attachment{
		Type:    "image",
		DataURL: "data:image/png;base64,iVBORw0KGgo=",
	}
	b, err := json.Marshal(InputImageBlock(att))
	if err != nil {
		t.Fatal(err)
	}
	body := string(b)
	if !strings.Contains(body, `"detail":"auto"`) {
		t.Fatalf("JSON output missing detail field: %s", body)
	}
	if !strings.Contains(body, `"type":"input_image"`) {
		t.Fatalf("JSON output missing type field: %s", body)
	}
}
