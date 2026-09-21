// Package fetch consolidates the download mechanism shared by NusaShell's
// installers: GET a URL under a caller context, stream the body to a
// destination file (or hand it back), report byte progress, and verify
// SHA-256 digests. Destination layout, staging, and retry policy stay
// with the caller.
package fetch

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
)

// Doer is the subset of *http.Client a download needs. Both *http.Client
// and installer-scoped client interfaces satisfy it.
type Doer interface {
	Do(req *http.Request) (*http.Response, error)
}

// Progress is one byte-count sample while a response body streams.
type Progress struct {
	BytesFetched int64
	BytesTotal   int64 // response Content-Length; negative when unknown
}

// Options tunes a fetch. Nil selects defaults.
type Options struct {
	// Header is merged into the GET request.
	Header http.Header
	// AcceptStatus decides whether a response status is usable. Nil
	// accepts http.StatusOK only.
	AcceptStatus func(code int) bool
	// Report receives running byte counters per written chunk (File only).
	// Nil disables progress callbacks.
	Report func(Progress)
}

// Body issues GET rawURL and returns the response body when the status is
// acceptable. The caller closes the body.
func Body(ctx context.Context, client Doer, rawURL string, opts *Options) (io.ReadCloser, error) {
	resp, err := get(ctx, client, rawURL, opts)
	if err != nil {
		return nil, err
	}
	return resp.Body, nil
}

// File streams GET rawURL into dst, truncating any existing file. The
// caller owns dst — partial downloads are left for it to clean up. When
// the server sent a Content-Length, a short body fails the download.
func File(ctx context.Context, client Doer, rawURL, dst string, opts *Options) error {
	resp, err := get(ctx, client, rawURL, opts)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	f, err := os.Create(dst)
	if err != nil {
		return err
	}
	var report func(Progress)
	if opts != nil {
		report = opts.Report
	}
	total := resp.ContentLength
	buf := make([]byte, 128*1024)
	var fetched int64
	for {
		n, rerr := resp.Body.Read(buf)
		if n > 0 {
			if _, werr := f.Write(buf[:n]); werr != nil {
				f.Close()
				return fmt.Errorf("fetch: write %s: %w", dst, werr)
			}
			fetched += int64(n)
			if report != nil {
				report(Progress{BytesFetched: fetched, BytesTotal: total})
			}
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			f.Close()
			return fmt.Errorf("fetch: GET %s: %w", rawURL, rerr)
		}
	}
	if err := f.Close(); err != nil {
		return err
	}
	if total > 0 && fetched != total {
		return fmt.Errorf("fetch: GET %s: truncated body: got %d of %d bytes", rawURL, fetched, total)
	}
	return nil
}

func get(ctx context.Context, client Doer, rawURL string, opts *Options) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	if opts != nil {
		for k, vs := range opts.Header {
			for _, v := range vs {
				req.Header.Add(k, v)
			}
		}
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch: GET %s: %w", rawURL, err)
	}
	accept := resp.StatusCode == http.StatusOK
	if opts != nil && opts.AcceptStatus != nil {
		accept = opts.AcceptStatus(resp.StatusCode)
	}
	if !accept {
		resp.Body.Close()
		return nil, fmt.Errorf("fetch: GET %s: HTTP %d", rawURL, resp.StatusCode)
	}
	return resp, nil
}

// MismatchError reports a SHA-256 digest mismatch.
type MismatchError struct {
	Got  string
	Want string
}

func (e *MismatchError) Error() string {
	return fmt.Sprintf("SHA-256 mismatch: got %s want %s", e.Got, e.Want)
}

// FileSHA256 returns the lowercase hex SHA-256 digest of path.
func FileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// VerifyFile checks the SHA-256 digest of path against expected (hex, any
// case). An empty expected digest is an error — callers that allow an
// unverified download must skip VerifyFile themselves. A mismatch returns
// *MismatchError.
func VerifyFile(path, expected string) error {
	if expected == "" {
		return fmt.Errorf("fetch: expected SHA-256 digest is empty")
	}
	got, err := FileSHA256(path)
	if err != nil {
		return err
	}
	if !strings.EqualFold(got, expected) {
		return &MismatchError{Got: got, Want: expected}
	}
	return nil
}
