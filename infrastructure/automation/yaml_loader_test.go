package automation

import (
	"testing"
	"time"

	"nusashell/domain"
)

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
