package config

import (
	"errors"
	"strings"
	"testing"
)

func TestWebSearchDefaults(t *testing.T) {
	c := NewConfig().WebSearch
	if !c.On() || c.Backend != "auto" || c.Brave.Enabled != "on" || c.Serper.Enabled != "off" || c.Tavily.Enabled != "off" ||
		c.GitHub.Enabled != "on" || c.GitHub.SearchIssues != "on" || c.Limit != 0 || c.Timeout != 0 {
		t.Fatalf("defaults: %+v", c)
	}
	if err := c.validate(); err != nil {
		t.Fatal(err)
	}
}

func TestWebSearchValidate(t *testing.T) {
	cases := map[string]string{
		`{"websearch":{"limit":51}}`:                          "websearch.limit",
		`{"websearch":{"timeout":"500ms"}}`:                   "websearch.timeout",
		`{"websearch":{"max_response_bytes":10}}`:             "max_response_bytes",
		`{"websearch":{"max_raw_bytes":1024}}`:                "max_raw_bytes",
		`{"websearch":{"backend":"google"}}`:                  "websearch.backend",
		`{"websearch":{"backend":"provider"}}`:                "websearch.provider",
		`{"websearch":{"brave":{"key_ref":"BSA123literal"}}}`: "vault",
		`{"websearch":{"github":{"enabled":"yes"}}}`:          "on or off",
		`{"websearch":{"user_agent":"a\nb"}}`:                 "one line",
	}
	for doc, want := range cases {
		if _, err := Validate([]byte(doc)); err == nil || !strings.Contains(errors.Unwrap(err).Error(), want) {
			t.Errorf("%s: %v, want %q", doc, err, want)
		}
	}
	ok := `{"websearch":{"limit":20,"timeout":"15s","backend":"provider","provider":"omniroute","brave":{"enabled":"off","key_ref":"secret:websearch/brave/api_key"},"github":{"token_ref":"env:GITHUB_TOKEN","search_issues":"off"}}}`
	c, err := Validate([]byte(ok))
	if err != nil {
		t.Fatal(err)
	}
	if c.WebSearch.Brave.Enabled != "off" || c.WebSearch.GitHub.SearchIssues != "off" || c.WebSearch.Serper.Enabled != "off" {
		t.Fatalf("%+v", c.WebSearch)
	}
}
