package domain

import "fmt"

// DAG is the job dependency graph. needs is authoritative; stage names
// are never inferred.
type DAG struct {
	Order []string
	Needs map[string][]string // job -> dependency job ids
	Next  map[string][]string // job -> dependents
}

// BuildDAG validates uniqueness, unknown needs, and acyclicity.
func BuildDAG(jobs []Job) (DAG, []ValidationIssue) {
	d := DAG{
		Needs: map[string][]string{},
		Next:  map[string][]string{},
	}
	var issues []ValidationIssue
	index := map[string]int{}
	for i, j := range jobs {
		if j.ID == "" {
			issues = append(issues, ValidationIssue{
				Path:    fmt.Sprintf("jobs[%d].id", i),
				Code:    "missing_job_id",
				Message: "job id is required",
				Level:   ValidationSyntax,
			})
			continue
		}
		if _, exists := index[j.ID]; exists {
			issues = append(issues, ValidationIssue{
				Path:    fmt.Sprintf("jobs.%s", j.ID),
				Code:    "duplicate_job",
				Message: fmt.Sprintf("job id %q is not unique", j.ID),
				Level:   ValidationSyntax,
			})
			continue
		}
		index[j.ID] = i
		d.Needs[j.ID] = nil
		d.Next[j.ID] = nil
	}
	for _, j := range jobs {
		if j.ID == "" {
			continue
		}
		seen := map[string]struct{}{}
		for ni, n := range j.Needs {
			if n.Job == "" {
				issues = append(issues, ValidationIssue{
					Path:    fmt.Sprintf("jobs.%s.needs[%d]", j.ID, ni),
					Code:    "empty_need",
					Message: "need job id is empty",
					Level:   ValidationSyntax,
				})
				continue
			}
			if _, ok := index[n.Job]; !ok {
				issues = append(issues, ValidationIssue{
					Path:    fmt.Sprintf("jobs.%s.needs[%d]", j.ID, ni),
					Code:    "unknown_job",
					Message: fmt.Sprintf("job %q does not exist", n.Job),
					Level:   ValidationSyntax,
				})
				continue
			}
			if n.Job == j.ID {
				issues = append(issues, ValidationIssue{
					Path:    fmt.Sprintf("jobs.%s.needs[%d]", j.ID, ni),
					Code:    "self_dependency",
					Message: "job cannot depend on itself",
					Level:   ValidationSyntax,
				})
				continue
			}
			if _, ok := seen[n.Job]; ok {
				continue
			}
			seen[n.Job] = struct{}{}
			d.Needs[j.ID] = append(d.Needs[j.ID], n.Job)
			d.Next[n.Job] = append(d.Next[n.Job], j.ID)
		}
	}
	if cycle := findCycle(d.Needs); cycle != "" {
		issues = append(issues, ValidationIssue{
			Path:    "jobs",
			Code:    "cycle",
			Message: "dependency cycle: " + cycle,
			Level:   ValidationSyntax,
		})
	}
	if len(issues) == 0 {
		d.Order = topological(d.Needs)
	}
	return d, issues
}

func findCycle(needs map[string][]string) string {
	const (
		white = 0
		grey  = 1
		black = 2
	)
	color := map[string]int{}
	var path []string
	var cycle string
	var visit func(string) bool
	visit = func(n string) bool {
		color[n] = grey
		path = append(path, n)
		for _, dep := range needs[n] {
			switch color[dep] {
			case grey:
				start := 0
				for i, p := range path {
					if p == dep {
						start = i
						break
					}
				}
				parts := append(append([]string{}, path[start:]...), dep)
				cycle = parts[0]
				for i := 1; i < len(parts); i++ {
					cycle += " -> " + parts[i]
				}
				return true
			case white:
				if visit(dep) {
					return true
				}
			}
		}
		path = path[:len(path)-1]
		color[n] = black
		return false
	}
	for n := range needs {
		if color[n] == white {
			if visit(n) {
				return cycle
			}
		}
	}
	return ""
}

func topological(needs map[string][]string) []string {
	indeg := map[string]int{}
	for n, deps := range needs {
		if _, ok := indeg[n]; !ok {
			indeg[n] = 0
		}
		for range deps {
			indeg[n]++
		}
		for _, d := range deps {
			if _, ok := indeg[d]; !ok {
				indeg[d] = 0
			}
		}
	}
	var queue []string
	for n, d := range indeg {
		if d == 0 {
			queue = append(queue, n)
		}
	}
	// stable-ish: sort queue alphabetically at each step for determinism
	sortStrings(queue)
	var order []string
	for len(queue) > 0 {
		n := queue[0]
		queue = queue[1:]
		order = append(order, n)
		// dependents: jobs that list n as a need
		var unlocked []string
		for job, deps := range needs {
			for _, d := range deps {
				if d == n {
					indeg[job]--
					if indeg[job] == 0 {
						unlocked = append(unlocked, job)
					}
				}
			}
		}
		sortStrings(unlocked)
		queue = append(queue, unlocked...)
	}
	return order
}

func sortStrings(s []string) {
	for i := 0; i < len(s); i++ {
		for j := i + 1; j < len(s); j++ {
			if s[j] < s[i] {
				s[i], s[j] = s[j], s[i]
			}
		}
	}
}

// ReadyJobs returns jobs whose remaining dependencies are all successful
// (or continue_on_error / optional). remaining is mutated by the caller.
func ReadyJobs(d DAG, status map[string]RunStatus, continueOnError map[string]bool) []string {
	var ready []string
	for job, deps := range d.Needs {
		st := status[job]
		if st != StatusQueued && st != StatusPending && st != "" {
			continue
		}
		ok := true
		for _, dep := range deps {
			ds := status[dep]
			switch ds {
			case StatusSuccess:
			case StatusFailed, StatusCancelled, StatusSkipped, StatusExpired:
				if continueOnError[dep] {
					continue
				}
				ok = false
			default:
				ok = false
			}
		}
		if ok {
			ready = append(ready, job)
		}
	}
	sortStrings(ready)
	return ready
}

// BlockedByFailure marks dependents of a failed job as blocked unless
// the failed job has continue_on_error.
func BlockedByFailure(d DAG, failedJob string, continueOnError bool, status map[string]RunStatus) []string {
	if continueOnError {
		return nil
	}
	var blocked []string
	var walk func(string)
	walk = func(job string) {
		for _, next := range d.Next[job] {
			st := status[next]
			if st == StatusSuccess || st == StatusFailed || st == StatusCancelled || st == StatusRunning {
				continue
			}
			if st != StatusBlocked {
				blocked = append(blocked, next)
			}
			walk(next)
		}
	}
	walk(failedJob)
	return blocked
}
