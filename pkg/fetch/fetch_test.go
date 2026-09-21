package fetch

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFileDownloadsAndReports(t *testing.T) {
	body := []byte(strings.Repeat("payload-", 1024))
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", fmt.Sprint(len(body)))
		_, _ = w.Write(body)
	}))
	defer srv.Close()

	dst := filepath.Join(t.TempDir(), "out.bin")
	var samples []Progress
	if err := File(context.Background(), srv.Client(), srv.URL, dst, &Options{
		Report: func(p Progress) { samples = append(samples, p) },
	}); err != nil {
		t.Fatalf("File: %v", err)
	}
	got, err := os.ReadFile(dst)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(body) {
		t.Fatal("downloaded bytes differ")
	}
	if len(samples) == 0 {
		t.Fatal("no progress reported")
	}
	var last int64
	for _, p := range samples {
		if p.BytesTotal != int64(len(body)) {
			t.Fatalf("BytesTotal = %d, want %d", p.BytesTotal, len(body))
		}
		if p.BytesFetched < last {
			t.Fatal("byte counter went backwards")
		}
		last = p.BytesFetched
	}
	if last != int64(len(body)) {
		t.Fatalf("final fetched = %d, want %d", last, len(body))
	}
}

func TestFileRejectsNonOKByDefault(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()
	dst := filepath.Join(t.TempDir(), "out.bin")
	err := File(context.Background(), srv.Client(), srv.URL, dst, nil)
	if err == nil || !strings.Contains(err.Error(), "HTTP 204") {
		t.Fatalf("expected HTTP 204 rejection, got %v", err)
	}
}

func TestFileAcceptStatusOverride(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte("ok"))
	}))
	defer srv.Close()
	dst := filepath.Join(t.TempDir(), "out.bin")
	err := File(context.Background(), srv.Client(), srv.URL, dst, &Options{
		AcceptStatus: func(code int) bool { return code/100 == 2 },
	})
	if err != nil {
		t.Fatalf("File: %v", err)
	}
}

func TestFileSendsHeader(t *testing.T) {
	var accept string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		accept = r.Header.Get("Accept")
		_, _ = w.Write([]byte("x"))
	}))
	defer srv.Close()
	dst := filepath.Join(t.TempDir(), "out.bin")
	err := File(context.Background(), srv.Client(), srv.URL, dst, &Options{
		Header: http.Header{"Accept": []string{"application/octet-stream"}},
	})
	if err != nil {
		t.Fatalf("File: %v", err)
	}
	if accept != "application/octet-stream" {
		t.Fatalf("Accept = %q", accept)
	}
}

func TestFileCancellation(t *testing.T) {
	block := make(chan struct{})
	defer close(block)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-block
	}))
	defer srv.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := File(ctx, srv.Client(), srv.URL, filepath.Join(t.TempDir(), "out.bin"), nil)
	if err == nil {
		t.Fatal("cancelled download must fail")
	}
}

func TestFileTruncatedBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "100")
		_, _ = w.Write([]byte("short"))
	}))
	defer srv.Close()
	dst := filepath.Join(t.TempDir(), "out.bin")
	if err := File(context.Background(), srv.Client(), srv.URL, dst, nil); err == nil {
		t.Fatal("truncated body must fail")
	}
}

func TestBodyReturnsStream(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("hello"))
	}))
	defer srv.Close()
	rc, err := Body(context.Background(), srv.Client(), srv.URL, nil)
	if err != nil {
		t.Fatalf("Body: %v", err)
	}
	defer rc.Close()
	data, err := io.ReadAll(rc)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "hello" {
		t.Fatalf("body = %q", data)
	}
}

func TestVerifyFile(t *testing.T) {
	dst := filepath.Join(t.TempDir(), "f.bin")
	content := []byte("verify me")
	if err := os.WriteFile(dst, content, 0o644); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(content)
	good := hex.EncodeToString(sum[:])
	if err := VerifyFile(dst, strings.ToUpper(good)); err != nil {
		t.Fatalf("VerifyFile good digest: %v", err)
	}
	err := VerifyFile(dst, strings.Repeat("0", 64))
	var mm *MismatchError
	if !errors.As(err, &mm) {
		t.Fatalf("expected *MismatchError, got %v", err)
	}
	if mm.Want != strings.Repeat("0", 64) {
		t.Fatalf("Want = %q", mm.Want)
	}
	if !strings.Contains(mm.Error(), "SHA-256 mismatch") {
		t.Fatalf("message = %q", mm.Error())
	}
	if err := VerifyFile(dst, ""); err == nil {
		t.Fatal("empty expected digest must be an error")
	}
	if err := VerifyFile(filepath.Join(t.TempDir(), "missing"), good); err == nil {
		t.Fatal("missing file must fail")
	}
}
