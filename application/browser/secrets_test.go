package appbrowser

import (
	"testing"

	apperror "github.com/jahrulnr/nusashell/infrastructure/error"
)

func TestResolveSecrets(t *testing.T) {
	values := map[string]string{"TOKEN": "s3cret", "USER": "ada"}
	out, b, err := resolveSecrets(`login [[env:USER]] / [[env:TOKEN]] \[[env:TOKEN]] [[other]]`, values)
	if err != nil {
		t.Fatal(err)
	}
	if out != `login ada / s3cret [[env:TOKEN]] [[other]]` || b["s3cret"] != "[[env:TOKEN]]" || len(b) != 2 {
		t.Fatalf("resolved %q, bindings %v", out, b)
	}
	if _, _, err := resolveSecrets("[[env:NOPE]]", values); apperror.As(err).StatusCode() != 400 {
		t.Fatalf("unknown reference = %v", err)
	}
	if _, _, err := resolveSecrets("[[env:bad name]]", values); apperror.As(err).StatusCode() != 400 {
		t.Fatalf("bad reference = %v", err)
	}
	// A value holding a tag is typed as is, never expanded again.
	out, _, _ = resolveSecrets("[[env:X]]", map[string]string{"X": "[[env:TOKEN]]"})
	if out != "[[env:TOKEN]]" {
		t.Fatalf("nested = %q", out)
	}
	if hasSecretRef(`\[[env:X]]`) || !hasSecretRef(`a [[env:X]]`) {
		t.Fatal("escape detection")
	}
}

func TestMask(t *testing.T) {
	a := New(nil, nil, nil, nil)
	a.remember("c1", map[string]string{"pass": "[[env:P]]", "password1": "[[env:LONG]]"})
	if got := a.Mask("c1", "value=password1 and pass"); got != "value=[[env:LONG]] and [[env:P]]" {
		t.Fatalf("masked = %q", got)
	}
	if got := a.Mask("c2", "password1"); got != "password1" {
		t.Fatalf("another thread's text = %q", got)
	}
}
