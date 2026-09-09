package automation

import (
	"testing"
	"time"

	"nusashell/domain"
)

func TestParseYAMLRejectsUnknownFields(t *testing.T) {
	_, err := ParseYAML([]byte(`
name: x
nmae: typo
jobs:
  build:
    steps:
      - run: echo
`))
	if err == nil {
		t.Fatal("expected unknown root field to be rejected")
	}
}

func TestParseYAMLRejectsMalformedTriggerTypes(t *testing.T) {
	_, err := ParseYAML([]byte(`
name: x
triggers:
  - when:
      event: email.received
      where: invalid
jobs:
  build:
    steps:
      - run: echo
`))
	if err == nil {
		t.Fatal("expected malformed trigger field to be rejected")
	}
}

func TestParseYAMLRejectsMultipleDocuments(t *testing.T) {
	_, err := ParseYAML([]byte(`
name: first
jobs:
  build:
    steps:
      - run: echo
---
name: second
jobs:
  build:
    steps:
      - run: echo
`))
	if err == nil {
		t.Fatal("expected multiple YAML documents to be rejected")
	}
}

func TestParseYAMLBasicDAG(t *testing.T) {
	raw := []byte(`
version: 1
name: NusaShell verification
triggers:
  manual: true
jobs:
  frontend:
    name: Frontend tests
    steps:
      - name: Test
        run: node --test
  backend:
    name: Backend tests
    steps:
      - name: Vet
        run: go vet ./...
  build:
    name: Build
    needs: [frontend, backend]
    steps:
      - name: Build
        run: go build ./...
    artifacts:
      paths: [dist/]
      retention: 7d
`)
	w, err := ParseYAML(raw)
	if err != nil {
		t.Fatal(err)
	}
	if w.Name != "NusaShell verification" {
		t.Fatalf("name = %q", w.Name)
	}
	if len(w.Triggers) != 1 || w.Triggers[0].Kind != domain.TriggerManual {
		t.Fatalf("triggers = %+v", w.Triggers)
	}
	if got := w.JobIDs(); len(got) != 3 || got[0] != "frontend" || got[1] != "backend" || got[2] != "build" {
		t.Fatalf("job order = %v", got)
	}
	build := w.JobByID("build")
	if build == nil || len(build.Needs) != 2 {
		t.Fatalf("build needs = %+v", build)
	}
	r := domain.ValidateSyntax(w)
	if r.Verdict() != "VALID" {
		t.Fatalf("%+v", r.Issues)
	}
}

func TestParseYAMLOnceEveryWhen(t *testing.T) {
	raw := []byte(`
name: Invoice processor
triggers:
  - once:
      at: 2026-08-18T09:00:00+07:00
      timezone: Asia/Jakarta
  - every:
      cron: "0 12 * * *"
      timezone: Asia/Jakarta
  - every:
      interval: 1h
  - when:
      event: email.received
      where:
        mailbox: finance
        subject_contains: invoice
      debounce: 30s
jobs:
  inspect:
    steps:
      - uses: email.read
      - wait_until: 2026-08-18T09:00:00+07:00
`)
	w, err := ParseYAML(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(w.Triggers) != 4 {
		t.Fatalf("triggers = %d", len(w.Triggers))
	}
	if w.Triggers[0].Kind != domain.TriggerOnce || w.Triggers[0].At == nil {
		t.Fatalf("once = %+v", w.Triggers[0])
	}
	if w.Triggers[1].Kind != domain.TriggerCron || w.Triggers[1].Cron != "0 12 * * *" {
		t.Fatalf("cron = %+v", w.Triggers[1])
	}
	if w.Triggers[2].Kind != domain.TriggerInterval || w.Triggers[2].Interval != time.Hour {
		t.Fatalf("interval = %+v", w.Triggers[2])
	}
	if w.Triggers[3].Kind != domain.TriggerEvent || w.Triggers[3].Event != "email.received" {
		t.Fatalf("when = %+v", w.Triggers[3])
	}
	if w.Triggers[3].Debounce != 30*time.Second {
		t.Fatalf("debounce = %s", w.Triggers[3].Debounce)
	}
	inspect := w.JobByID("inspect")
	if inspect == nil || inspect.Steps[0].Uses != "email.read" || inspect.Steps[1].WaitUntil == nil {
		t.Fatalf("steps = %+v", inspect)
	}
}

func TestParseYAMLRejectsAmbiguousTriggerKind(t *testing.T) {
	_, err := ParseYAML([]byte(`
name: x
triggers:
  - once:
      at: 2026-08-18T09:00:00Z
    when:
      event: email.received
jobs:
  build:
    steps:
      - run: echo
`))
	if err == nil {
		t.Fatal("expected trigger with multiple kinds to be rejected")
	}
}

func TestParseYAMLRejectsFalseManualTrigger(t *testing.T) {
	for _, triggers := range []string{
		"- manual: false",
		"manual: false",
	} {
		raw := []byte("name: x\ntriggers:\n  " + triggers + "\njobs:\n  build:\n    steps:\n      - run: echo\n")
		if _, err := ParseYAML(raw); err == nil {
			t.Fatalf("triggers %q: expected manual:false to be rejected", triggers)
		}
	}
}

func TestParseYAMLRejectsCronPlusInterval(t *testing.T) {
	_, err := ParseYAML([]byte(`
name: x
triggers:
  - every:
      cron: "0 12 * * *"
      interval: 1h
jobs:
  a:
    steps:
      - run: echo
`))
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestParseYAMLArtifactNeedsObject(t *testing.T) {
	w, err := ParseYAML([]byte(`
name: x
jobs:
  build:
    steps:
      - run: echo dist
    artifacts:
      paths: [dist/]
  test:
    needs:
      - job: build
        artifacts: true
    steps:
      - run: echo test
`))
	if err != nil {
		t.Fatal(err)
	}
	n := w.JobByID("test").Needs[0]
	if n.Job != "build" || !n.Artifacts {
		t.Fatalf("%+v", n)
	}
}

func TestParseYAMLNotify(t *testing.T) {
	w, err := ParseYAML([]byte(`
name: notify-demo
trust: trusted
notify:
  plugin: nusashell.telegram
  detail: all
  chat_id: "${event.chat_id}"
jobs:
  j:
    steps:
      - run: echo hi
`))
	if err != nil {
		t.Fatal(err)
	}
	if w.Notify == nil || w.Notify.Plugin != "nusashell.telegram" {
		t.Fatalf("notify = %+v", w.Notify)
	}
	if w.Notify.Detail != domain.NotifyDetailAll || w.Notify.ChatID != "${event.chat_id}" {
		t.Fatalf("notify fields = %+v", w.Notify)
	}
	if _, err := ParseYAML([]byte(`
name: bad
notify:
  plugin: tg
  unknown_key: x
jobs:
  j:
    steps:
      - run: echo
`)); err == nil {
		t.Fatal("expected unknown notify key to be rejected")
	}
}
