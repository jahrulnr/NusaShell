package ci

import (
	"fmt"
	"strings"
	"time"

	"nusashell/domain"
	"nusashell/pkg/duration"

	"gopkg.in/yaml.v3"
)

// ParseYAML decodes a NusaShell workflow/pipeline document.
func ParseYAML(raw []byte) (*domain.WorkflowDefinition, error) {
	var doc yamlDoc
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("yaml: %w", err)
	}
	w := &domain.WorkflowDefinition{
		Name:       strings.TrimSpace(doc.Name),
		Version:    doc.Version,
		Enabled:    true,
		Trust:      domain.TrustLevel(doc.Trust),
		Env:        doc.Env,
		WebhookURL: strings.TrimSpace(doc.WebhookURL),
		Source:     domain.WorkflowSource{Kind: "file"},
	}
	if doc.Enabled != nil {
		w.Enabled = *doc.Enabled
	}
	if w.Version == 0 {
		w.Version = 1
	}
	w.Concurrency = domain.Concurrency{
		Key:    doc.Concurrency.Key,
		Policy: domain.ConcurrencyPolicy(doc.Concurrency.Policy),
	}
	w.Missed = domain.MissedRunPolicy(doc.Missed)
	if doc.Defaults.Shell != "" {
		w.Defaults.Shell = doc.Defaults.Shell
	}
	if doc.Defaults.Timeout != "" {
		d, err := duration.Parse(doc.Defaults.Timeout)
		if err != nil {
			return nil, fmt.Errorf("defaults.timeout: %w", err)
		}
		w.Defaults.Timeout = d
	}
	var triggers []yamlTrigger
	switch doc.Triggers.Kind {
	case yaml.SequenceNode:
		_ = doc.Triggers.Decode(&triggers)
	case yaml.MappingNode:
		var m yamlTriggerMap
		_ = doc.Triggers.Decode(&m)
		if m.Manual {
			v := true
			triggers = []yamlTrigger{{Manual: &v}}
		}
	}
	for i, t := range triggers {
		tr, err := parseTrigger(t, i)
		if err != nil {
			return nil, err
		}
		w.Triggers = append(w.Triggers, tr)
	}
	for id, j := range doc.Jobs {
		job, err := parseJob(id, j)
		if err != nil {
			return nil, err
		}
		w.Jobs = append(w.Jobs, job)
	}
	// YAML maps iterate randomly; keep definition order from the node if possible.
	var ordered []string
	var root yaml.Node
	if err := yaml.Unmarshal(raw, &root); err == nil && len(root.Content) > 0 {
		doc := root.Content[0]
		if doc.Kind == yaml.MappingNode {
			for i := 0; i+1 < len(doc.Content); i += 2 {
				if doc.Content[i].Value != "jobs" {
					continue
				}
				jobs := doc.Content[i+1]
				if jobs.Kind == yaml.MappingNode {
					for j := 0; j+1 < len(jobs.Content); j += 2 {
						ordered = append(ordered, jobs.Content[j].Value)
					}
				}
				break
			}
		}
	}
	if len(ordered) == len(w.Jobs) {
		index := map[string]domain.Job{}
		for _, j := range w.Jobs {
			index[j.ID] = j
		}
		var jobs []domain.Job
		for _, id := range ordered {
			if j, ok := index[id]; ok {
				jobs = append(jobs, j)
			}
		}
		if len(jobs) == len(w.Jobs) {
			w.Jobs = jobs
		}
	}
	return w, nil
}

type yamlDoc struct {
	Version     int                `yaml:"version"`
	Name        string             `yaml:"name"`
	Enabled     *bool              `yaml:"enabled"`
	Trust       string             `yaml:"trust"`
	Concurrency yamlConcurrency    `yaml:"concurrency"`
	Missed      string             `yaml:"missed"`
	Triggers    yaml.Node          `yaml:"triggers"`
	Defaults    yamlDefaults       `yaml:"defaults"`
	Env         map[string]string  `yaml:"env"`
	WebhookURL  string             `yaml:"webhook_url"`
	Jobs        map[string]yamlJob `yaml:"jobs"`
}

type yamlTriggerMap struct {
	Manual bool `yaml:"manual"`
}

type yamlConcurrency struct {
	Key    string `yaml:"key"`
	Policy string `yaml:"policy"`
}

type yamlDefaults struct {
	Shell   string `yaml:"shell"`
	Timeout string `yaml:"timeout"`
}

type yamlTrigger struct {
	Once      *yamlOnce  `yaml:"once"`
	Every     *yamlEvery `yaml:"every"`
	When      *yamlWhen  `yaml:"when"`
	Manual    *bool      `yaml:"manual"`
	Debounce  string     `yaml:"debounce"`
	AutoStart string     `yaml:"auto_start"`
}

type yamlOnce struct {
	At       string `yaml:"at"`
	Timezone string `yaml:"timezone"`
}

type yamlEvery struct {
	Cron     string `yaml:"cron"`
	Interval string `yaml:"interval"`
	Timezone string `yaml:"timezone"`
}

type yamlWhen struct {
	Event    string         `yaml:"event"`
	Where    map[string]any `yaml:"where"`
	Debounce string         `yaml:"debounce"`
}

type yamlJob struct {
	Name            string            `yaml:"name"`
	Needs           yamlNeeds         `yaml:"needs"`
	If              string            `yaml:"if"`
	RunsOn          []string          `yaml:"runs_on"`
	Env             map[string]string `yaml:"env"`
	Timeout         string            `yaml:"timeout"`
	ContinueOnError bool              `yaml:"continue_on_error"`
	Retry           yamlRetry         `yaml:"retry"`
	Steps           []yamlStep        `yaml:"steps"`
	Artifacts       yamlArtifacts     `yaml:"artifacts"`
	Cache           yamlCache         `yaml:"cache"`
}

type yamlRetry struct {
	MaxAttempts int      `yaml:"max_attempts"`
	On          []string `yaml:"on"`
}

type yamlArtifacts struct {
	Paths     []string `yaml:"paths"`
	Retention string   `yaml:"retention"`
}

type yamlCache struct {
	Namespace string   `yaml:"namespace"`
	Paths     []string `yaml:"paths"`
	Key       []string `yaml:"key"`
}

type yamlStep struct {
	Name      string            `yaml:"name"`
	Run       string            `yaml:"run"`
	Uses      string            `yaml:"uses"`
	With      map[string]any    `yaml:"with"`
	WaitUntil string            `yaml:"wait_until"`
	Agent     *yamlAgent        `yaml:"agent"`
	Shell     string            `yaml:"shell"`
	Env       map[string]string `yaml:"env"`
	Timeout   string            `yaml:"timeout"`
}

type yamlAgent struct {
	Prompt       string         `yaml:"prompt"`
	OutputSchema map[string]any `yaml:"output_schema"`
	Model        string         `yaml:"model"`
}

func parseTrigger(t yamlTrigger, i int) (domain.Trigger, error) {
	id := fmt.Sprintf("trg_%d", i+1)
	debounce := t.Debounce
	auto := domain.AutoStartPolicy(t.AutoStart)
	if t.Once != nil {
		at, err := parseTime(t.Once.At)
		if err != nil {
			return domain.Trigger{}, fmt.Errorf("triggers[%d].once.at: %w", i, err)
		}
		return domain.Trigger{
			ID: id, Kind: domain.TriggerOnce, Family: domain.FamilyOnce,
			At: &at, Timezone: t.Once.Timezone, AutoStart: auto,
		}, nil
	}
	if t.Every != nil {
		if t.Every.Cron != "" && t.Every.Interval != "" {
			return domain.Trigger{}, fmt.Errorf("triggers[%d].every: cron and interval are distinct; specify one", i)
		}
		if t.Every.Interval != "" {
			d, err := duration.Parse(t.Every.Interval)
			if err != nil {
				return domain.Trigger{}, fmt.Errorf("triggers[%d].every.interval: %w", i, err)
			}
			return domain.Trigger{
				ID: id, Kind: domain.TriggerInterval, Family: domain.FamilyEvery,
				Interval: d, Timezone: t.Every.Timezone, AutoStart: auto,
			}, nil
		}
		return domain.Trigger{
			ID: id, Kind: domain.TriggerCron, Family: domain.FamilyEvery,
			Cron: t.Every.Cron, Timezone: t.Every.Timezone, AutoStart: auto,
		}, nil
	}
	if t.When != nil {
		if t.When.Debounce != "" {
			debounce = t.When.Debounce
		}
		var d time.Duration
		if debounce != "" {
			var err error
			d, err = duration.Parse(debounce)
			if err != nil {
				return domain.Trigger{}, fmt.Errorf("triggers[%d].debounce: %w", i, err)
			}
		}
		return domain.Trigger{
			ID: id, Kind: domain.TriggerEvent, Family: domain.FamilyWhen,
			Event: t.When.Event, Where: t.When.Where, Debounce: d, AutoStart: auto,
		}, nil
	}
	if t.Manual != nil && *t.Manual {
		return domain.Trigger{ID: id, Kind: domain.TriggerManual, Family: domain.FamilyManual, Manual: true}, nil
	}
	return domain.Trigger{}, fmt.Errorf("triggers[%d]: specify once, every, when, or manual", i)
}

func parseJob(id string, j yamlJob) (domain.Job, error) {
	job := domain.Job{
		ID:              id,
		Name:            j.Name,
		If:              j.If,
		RunsOn:          j.RunsOn,
		Env:             j.Env,
		ContinueOnError: j.ContinueOnError,
		Retry:           domain.RetryPolicy{MaxAttempts: j.Retry.MaxAttempts, On: j.Retry.On},
	}
	if job.Name == "" {
		job.Name = id
	}
	if j.Timeout != "" {
		d, err := duration.Parse(j.Timeout)
		if err != nil {
			return job, fmt.Errorf("jobs.%s.timeout: %w", id, err)
		}
		job.Timeout = d
	}
	needs, err := j.Needs.parse()
	if err != nil {
		return job, fmt.Errorf("jobs.%s.needs: %w", id, err)
	}
	job.Needs = needs
	if len(j.Artifacts.Paths) > 0 {
		job.Artifacts.Paths = j.Artifacts.Paths
		if j.Artifacts.Retention != "" {
			d, err := duration.Parse(j.Artifacts.Retention)
			if err != nil {
				return job, fmt.Errorf("jobs.%s.artifacts.retention: %w", id, err)
			}
			job.Artifacts.Retention = d
		}
	}
	job.Cache = domain.CacheSpec{Namespace: j.Cache.Namespace, Paths: j.Cache.Paths, KeyParts: j.Cache.Key}
	for si, s := range j.Steps {
		step, err := parseStep(s)
		if err != nil {
			return job, fmt.Errorf("jobs.%s.steps[%d]: %w", id, si, err)
		}
		if step.ID == "" {
			step.ID = fmt.Sprintf("step_%d", si+1)
		}
		job.Steps = append(job.Steps, step)
	}
	return job, nil
}

func parseStep(s yamlStep) (domain.Step, error) {
	step := domain.Step{
		Name:  s.Name,
		Run:   s.Run,
		Uses:  s.Uses,
		With:  s.With,
		Shell: s.Shell,
		Env:   s.Env,
	}
	if s.WaitUntil != "" {
		tm, err := parseTime(s.WaitUntil)
		if err != nil {
			return step, err
		}
		step.WaitUntil = &tm
	}
	if s.Agent != nil {
		step.Agent = &domain.AgentStep{Prompt: s.Agent.Prompt, OutputSchema: s.Agent.OutputSchema, Model: s.Agent.Model}
	}
	if s.Timeout != "" {
		d, err := duration.Parse(s.Timeout)
		if err != nil {
			return step, err
		}
		step.Timeout = d
	}
	return step, nil
}

func parseTime(s string) (time.Time, error) {
	s = strings.TrimSpace(s)
	formats := []string{
		time.RFC3339,
		"2006-01-02T15:04:05Z07:00",
		"2006-01-02 15:04:05",
		"2006-01-02T15:04:05",
	}
	var last error
	for _, f := range formats {
		tm, err := time.Parse(f, s)
		if err == nil {
			return tm, nil
		}
		last = err
	}
	return time.Time{}, last
}

// yamlNeeds accepts either ["a","b"] or [{job: a, artifacts: true}].
type yamlNeeds struct {
	ids []domain.JobNeed
}

func (n *yamlNeeds) UnmarshalYAML(value *yaml.Node) error {
	if value.Kind == yaml.ScalarNode && value.Value == "" {
		return nil
	}
	var ids []string
	if err := value.Decode(&ids); err == nil {
		for _, id := range ids {
			n.ids = append(n.ids, domain.JobNeed{Job: id})
		}
		return nil
	}
	var objs []struct {
		Job       string `yaml:"job"`
		Artifacts bool   `yaml:"artifacts"`
	}
	if err := value.Decode(&objs); err != nil {
		return err
	}
	for _, o := range objs {
		n.ids = append(n.ids, domain.JobNeed{Job: o.Job, Artifacts: o.Artifacts})
	}
	return nil
}

func (n yamlNeeds) parse() ([]domain.JobNeed, error) {
	return n.ids, nil
}
