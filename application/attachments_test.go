package application

import (
	"encoding/base64"
	"strings"
	"testing"

	"nusashell/contracts"
)

func TestTextAttachmentRejectsInvalidUTF8(t *testing.T) {
	_, err := attachmentFromDTO(contracts.AttachmentDTO{
		Type: "text", Name: "note.txt", MediaType: "text/plain",
		Content: string([]byte{0xff, 0xfe}),
	})
	if err == nil {
		t.Fatal("want UTF-8 validation error")
	}
}

func TestImageAttachmentRejectsMismatchedMagicBytes(t *testing.T) {
	payload := base64.StdEncoding.EncodeToString([]byte("not a png"))
	_, err := attachmentFromDTO(contracts.AttachmentDTO{
		Type: "image", Name: "diagram.png", MediaType: "image/png",
		DataURL: "data:image/png;base64," + payload,
	})
	if err == nil {
		t.Fatal("want magic-byte mismatch error")
	}
}

func TestPNGAttachmentAcceptsSignature(t *testing.T) {
	png := []byte{137, 80, 78, 71, 13, 10, 26, 10, 0, 1, 2, 3}
	payload := base64.StdEncoding.EncodeToString(png)
	got, err := attachmentFromDTO(contracts.AttachmentDTO{
		Type: "image", Name: "diagram.png", MediaType: "image/png",
		DataURL: "data:image/png;base64," + payload,
	})
	if err != nil {
		t.Fatalf("valid PNG rejected: %v", err)
	}
	if got.MediaType != "image/png" {
		t.Fatalf("media type = %q", got.MediaType)
	}
}

func TestAttachmentCountLimit(t *testing.T) {
	items := make([]contracts.AttachmentDTO, 5)
	for i := range items {
		items[i] = contracts.AttachmentDTO{Type: "text", Name: "n.txt", MediaType: "text/plain", Content: "x"}
	}
	_, rpcErr := attachmentsFromDTO(items)
	if rpcErr == nil {
		t.Fatal("want validation error for more than 4 attachments")
	}
	if !strings.Contains(rpcErr.Message, "up to 4") {
		t.Fatalf("message = %q", rpcErr.Message)
	}
}
