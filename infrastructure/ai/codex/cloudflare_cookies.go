package codex

import (
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"sync"
)

// cloudflareCookieAllowlist mirrors the codex CLI allowlist in
// codex-rs/http-client/src/chatgpt_cloudflare_cookies.rs. Only Cloudflare
// infrastructure cookies are persisted; ChatGPT account/session/auth cookies
// are never stored in the shared jar.
//
// Reference:
// https://developers.cloudflare.com/fundamentals/reference/policies-compliances/cloudflare-cookies/
var cloudflareCookieAllowlist = map[string]bool{
	"__cf_bm":         true,
	"__cflb":          true,
	"__cfruid":        true,
	"__cfseq":         true,
	"__cfwaitingroom": true,
	"_cfuvid":         true,
	"cf_clearance":    true,
	"cf_ob_info":      true,
	"cf_use_ob":       true,
}

// chatgptHosts are hosts that receive Cloudflare cookies. Requests to any
// other host get no cookies and store none.
var chatgptHosts = map[string]bool{
	"chatgpt.com":         true,
	"chat.openai.com":     true,
	"auth.openai.com":     true,
	"ab.chatgpt.com":      true,
	"cdn.auth.openai.com": true,
}

// CloudflareCookieJar is a process-global cookie jar that only persists
// Cloudflare infrastructure cookies for ChatGPT hosts. It mirrors the
// codex CLI's SHARED_CHATGPT_CLOUDFLARE_COOKIE_STORE: the jar is shared
// across all Codex requests in the process so a single Cloudflare
// challenge solution (cf_clearance, __cf_bm) is reused by both the chat
// (Responses API) and image (/images/generations, /images/edits) clients.
//
// Auth stays in the Authorization header (Bearer token), never in cookies.
type CloudflareCookieJar struct {
	jar http.CookieJar
}

var (
	sharedCloudflareJar  *CloudflareCookieJar
	sharedCloudflareOnce sync.Once
)

// SharedCloudflareCookieJar returns the process-global Cloudflare cookie
// jar. All Codex HTTP clients (chat + images) share the same jar so a
// Cloudflare challenge solved by one client is reused by the other.
func SharedCloudflareCookieJar() *CloudflareCookieJar {
	sharedCloudflareOnce.Do(func() {
		inner, err := cookiejar.New(nil)
		if err != nil {
			// cookiejar.New never returns an error for nil options.
			panic("cookiejar.New failed: " + err.Error())
		}
		sharedCloudflareJar = &CloudflareCookieJar{jar: inner}
	})
	return sharedCloudflareJar
}

// isChatGPTHost returns true for ChatGPT-related hosts that should
// send/receive Cloudflare cookies.
func isChatGPTHost(host string) bool {
	host = strings.ToLower(strings.TrimSpace(host))
	if host == "" {
		return false
	}
	if chatgptHosts[host] {
		return true
	}
	// Allow subdomains of chatgpt.com (e.g. api.chatgpt.com).
	return strings.HasSuffix(host, ".chatgpt.com")
}

// isAllowedCloudflareCookie returns true for Cloudflare infrastructure
// cookie names.
func isAllowedCloudflareCookie(name string) bool {
	return cloudflareCookieAllowlist[strings.TrimSpace(name)]
}

// SetCookies implements http.CookieJar. Only Cloudflare cookies for
// ChatGPT hosts are persisted; all others are silently dropped.
func (j *CloudflareCookieJar) SetCookies(u *url.URL, cookies []*http.Cookie) {
	if j == nil || u == nil {
		return
	}
	if !isChatGPTHost(u.Hostname()) {
		return
	}
	filtered := cookies[:0]
	for _, c := range cookies {
		if isAllowedCloudflareCookie(c.Name) {
			filtered = append(filtered, c)
		}
	}
	if len(filtered) > 0 {
		j.jar.SetCookies(u, filtered)
	}
}

// Cookies implements http.CookieJar. Returns Cloudflare cookies for
// ChatGPT hosts, nothing for other hosts.
func (j *CloudflareCookieJar) Cookies(u *url.URL) []*http.Cookie {
	if j == nil || u == nil {
		return nil
	}
	if !isChatGPTHost(u.Hostname()) {
		return nil
	}
	all := j.jar.Cookies(u)
	out := all[:0]
	for _, c := range all {
		if isAllowedCloudflareCookie(c.Name) {
			out = append(out, c)
		}
	}
	return out
}
