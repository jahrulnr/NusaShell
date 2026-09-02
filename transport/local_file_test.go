package transport

import (
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLocalFilePDFUsesPDFContentTypeWithoutExtension(t *testing.T) {
	path := filepath.Join(t.TempDir(), "preview.bin")
	if err := os.WriteFile(path, []byte("%PDF-1.7\n1 0 obj\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest("GET", "/local-file?path="+path, nil)
	res := httptest.NewRecorder()
	(&Server{}).handleLocalFile(res, req)

	if res.Code != 200 {
		t.Fatalf("status = %d, want 200", res.Code)
	}
	if got := res.Header().Get("Content-Type"); !strings.HasPrefix(got, "application/pdf") {
		t.Fatalf("Content-Type = %q, want application/pdf", got)
	}
}
