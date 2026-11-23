package appbrowser

import (
	"context"
	"regexp"
	"sort"
	"strings"

	dservice "github.com/jahrulnr/nusashell/infrastructure/domain/service"
	apperror "github.com/jahrulnr/nusashell/infrastructure/error"
)

// [[env:NAME]] references let a tool type a secret without seeing it: the
// value comes from the thread's agent environment, is typed through real
// text input, and is masked back to its tag in everything the tools return.
// Only the exact [[env: prefix is reserved; a backslash before [[ escapes
// the opener (\[[env:X]] types [[env:X]]). Resolution is one pass over the
// original text, so a value that contains a tag is never expanded.

var envRefName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// nonTextFields cannot take a secret: their values are not typed.
var nonTextFields = map[string]bool{"checkbox": true, "radio": true, "combobox": true}

// hasSecretRef reports an unescaped [[env: opener.
func hasSecretRef(text string) bool {
	for i := 0; i+6 <= len(text); i++ {
		if text[i:i+6] == "[[env:" && (i == 0 || text[i-1] != '\\') {
			return true
		}
	}
	return false
}

// resolveSecrets expands the references against values; bindings maps
// each raw value to its tag for masking.
func resolveSecrets(text string, values map[string]string) (string, map[string]string, error) {
	bindings := map[string]string{}
	var out strings.Builder
	for i := 0; i < len(text); {
		if text[i] == '\\' && i+2 < len(text) && text[i+1] == '[' && text[i+2] == '[' {
			out.WriteString("[[")
			i += 3
			continue
		}
		if strings.HasPrefix(text[i:], "[[env:") {
			rest := text[i+6:]
			end := strings.Index(rest, "]]")
			if end < 0 || !envRefName.MatchString(rest[:end]) {
				return "", bindings, apperror.New(400, "a secret reference is [[env:NAME]]", nil)
			}
			name := rest[:end]
			value := values[name]
			if value == "" {
				return "", bindings, apperror.New(400, "no saved environment variable "+name+" for this thread's agent", nil)
			}
			out.WriteString(value)
			tag := "[[env:" + name + "]]"
			if old, ok := bindings[value]; !ok || tag < old {
				bindings[value] = tag
			}
			i += 6 + end + 2
			continue
		}
		out.WriteByte(text[i])
		i++
	}
	return out.String(), bindings, nil
}

// secretValues loads the thread's secrets only when text references one.
func (a *App) secretValues(ctx context.Context, convID string, texts ...string) (map[string]string, error) {
	need := false
	for _, t := range texts {
		need = need || hasSecretRef(t)
	}
	if !need {
		return nil, nil
	}
	if a.secrets == nil {
		return nil, apperror.New(400, "secret references need an agent with saved environment variables", nil)
	}
	return a.secrets(ctx, convID)
}

// remember adds raw → tag bindings to the thread's mask.
func (a *App) remember(convID string, bindings map[string]string) {
	if len(bindings) == 0 {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	tags := a.masks[convID]
	if tags == nil {
		tags = map[string]string{}
		a.masks[convID] = tags
	}
	for raw, tag := range bindings {
		if old, ok := tags[raw]; !ok || tag < old {
			tags[raw] = tag
		}
	}
}

// Mask replaces every secret this thread typed with its tag. Longer values
// go first through a non-recursive replacer, so overlapping secrets cannot
// leak a part.
func (a *App) Mask(convID, text string) string {
	a.mu.Lock()
	tags := a.masks[convID]
	raws := make([]string, 0, len(tags))
	for raw := range tags {
		raws = append(raws, raw)
	}
	a.mu.Unlock()
	if len(raws) == 0 || text == "" {
		return text
	}
	sort.Slice(raws, func(i, j int) bool {
		if len(raws[i]) != len(raws[j]) {
			return len(raws[i]) > len(raws[j])
		}
		return raws[i] < raws[j]
	})
	pairs := make([]string, 0, 2*len(raws))
	for _, raw := range raws {
		pairs = append(pairs, raw, tags[raw])
	}
	return strings.NewReplacer(pairs...).Replace(text)
}

// fillSecrets fills fields in order: plain values through the native-setter
// batch, values with references through secret typing. Every binding is
// remembered before anything is typed.
func (a *App) fillSecrets(ctx context.Context, convID string, s dservice.BrowserSession, fields []dservice.BrowserField, values map[string]string) (string, error) {
	resolved := make([]dservice.BrowserField, len(fields))
	secret := make([]bool, len(fields))
	for i, f := range fields {
		if hasSecretRef(f.Value) && nonTextFields[strings.ToLower(f.Type)] {
			return "", apperror.New(400, "secret references go only into text fields", nil)
		}
		v, b, err := resolveSecrets(f.Value, values)
		a.remember(convID, b)
		if err != nil {
			return "", err
		}
		resolved[i], secret[i] = f, len(b) > 0
		resolved[i].Value = v
	}
	var details []string
	var run []dservice.BrowserField
	flush := func() error {
		if len(run) == 0 {
			return nil
		}
		out, err := s.Fill(ctx, run)
		run = nil
		if out != "" {
			details = append(details, out)
		}
		return err
	}
	for i, f := range resolved {
		if !secret[i] {
			run = append(run, f)
			continue
		}
		if err := flush(); err != nil {
			return "", err
		}
		if err := s.TypeSecret(ctx, f.Ref, f.Value, true); err != nil {
			return "", err
		}
		details = append(details, f.Ref+": filled")
	}
	if err := flush(); err != nil {
		return "", err
	}
	return strings.Join(details, "; "), nil
}
