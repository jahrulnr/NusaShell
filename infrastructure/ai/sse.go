// Package ai implements the application AI provider port for Anthropic and
// OpenAI-compatible chat endpoints, including SSE streaming, tool calling,
// and Anthropic prompt caching.
package ai

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"nusashell/application"
)

// sseEvent is one Server-Sent Event frame.
type sseEvent struct {
	Event string
	Data  string
}

// readSSE streams SSE frames from r, calling fn for each frame until EOF.
func readSSE(ctx context.Context, r io.Reader, fn func(ev sseEvent) error) error {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	var ev sseEvent
	flush := func() error {
		if ev.Data != "" || ev.Event != "" {
			if err := fn(ev); err != nil {
				return err
			}
		}
		ev = sseEvent{}
		return nil
	}
	for scanner.Scan() {
		if err := ctx.Err(); err != nil {
			return err
		}
		line := scanner.Text()
		if line == "" {
			if err := flush(); err != nil {
				return err
			}
			continue
		}
		if strings.HasPrefix(line, ":") {
			continue
		}
		key, val, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		val = strings.TrimPrefix(val, " ")
		switch key {
		case "event":
			ev.Event = val
		case "data":
			ev.Data += val
		}
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	return flush()
}

// decodeData unmarshals an SSE data payload.
func decodeData[T any](ev sseEvent, out *T) error {
	if ev.Data == "" {
		return fmt.Errorf("empty SSE data frame")
	}
	return json.Unmarshal([]byte(ev.Data), out)
}

// joinEndpoint appends the operation path to the configured base URL
// verbatim. The base URL is the API root the user chose — it already carries
// whatever version segment the endpoint uses (v1, v4, …) — so no version is
// ever injected. A base that already ends with the operation path is used
// as-is, letting users paste a full endpoint.
func joinEndpoint(base, op string) string {
	base = strings.TrimRight(base, "/")
	if strings.HasSuffix(base, op) {
		return base
	}
	return base + op
}

// jsonReq builds an HTTP request with a JSON body.
func jsonReq(ctx context.Context, method, url string, headers map[string]string, body any) (*http.Request, error) {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		rd = strings.NewReader(string(b))
	}
	req, err := http.NewRequestWithContext(ctx, method, url, rd)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	return req, nil
}

// openSSE creates an SSE request and validates the HTTP response before a
// provider-specific stream decoder reads its frames. Wire decoding stays in
// each adapter because the event shapes differ by provider protocol.
func openSSE(ctx context.Context, client *http.Client, url string, headers map[string]string, body any) (*http.Response, error) {
	req, err := jsonReq(ctx, http.MethodPost, url, headers, body)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, &application.UpstreamError{Temporary: true, Err: err}
	}
	if resp.StatusCode < 400 {
		return resp, nil
	}
	defer resp.Body.Close()
	message, _ := readAllLimit(resp.Body, 4096)
	return nil, &application.UpstreamError{
		StatusCode: resp.StatusCode,
		RetryAfter: parseRetryAfter(resp.Header.Get("Retry-After"), time.Now()),
		Err:        fmt.Errorf("provider returned HTTP %d: %s", resp.StatusCode, strings.TrimSpace(message)),
	}
}

func readAllLimit(r io.Reader, n int64) (string, error) {
	b, err := io.ReadAll(io.LimitReader(r, n))
	return string(b), err
}

// doJSON performs a request and decodes the JSON response into out.
func doJSON(ctx context.Context, client *http.Client, method, url string, headers map[string]string, body any, out any) error {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rd = strings.NewReader(string(b))
	}
	req, err := http.NewRequestWithContext(ctx, method, url, rd)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := client.Do(req)
	if err != nil {
		return &application.UpstreamError{Temporary: true, Err: err}
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return &application.UpstreamError{
			StatusCode: resp.StatusCode,
			RetryAfter: parseRetryAfter(resp.Header.Get("Retry-After"), time.Now()),
			Err:        fmt.Errorf("provider returned HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(msg))),
		}
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func parseRetryAfter(value string, now time.Time) time.Duration {
	value = strings.TrimSpace(value)
	if seconds, err := strconv.Atoi(value); err == nil && seconds >= 0 {
		return time.Duration(seconds) * time.Second
	}
	if when, err := http.ParseTime(value); err == nil && when.After(now) {
		return when.Sub(now)
	}
	return 0
}

func incompleteSSEError() error {
	return &application.UpstreamError{Temporary: true, Err: io.ErrUnexpectedEOF}
}

func retryableSSEReadError(err error) error {
	if err == nil {
		return nil
	}
	var networkErr net.Error
	if errors.Is(err, io.ErrUnexpectedEOF) || errors.As(err, &networkErr) {
		return &application.UpstreamError{Temporary: true, Err: err}
	}
	return err
}
