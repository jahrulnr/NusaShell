package domain

// ClaimJobs transitions queued or pending jobs in a run to the running state
// and returns the IDs of the jobs that were claimed.
func ClaimJobs(run *WorkflowRun, ready []string) []string {
	var claimed []string
	for _, jobID := range ready {
		jr := run.JobRunByID(jobID)
		if jr == nil {
			continue
		}
		if jr.Status == StatusQueued || jr.Status == StatusPending {
			jr.Status = StatusRunning
			claimed = append(claimed, jobID)
		}
	}
	return claimed
}

// MergeRun merges the state of src into dst, preferring the more progressed
// status and copying through timing and blocking metadata from src.
func MergeRun(dst, src *WorkflowRun) {
	dst.Status = MergeStatus(dst.Status, src.Status)
	if src.BlockedReason != "" {
		dst.BlockedReason = src.BlockedReason
	}
	if !src.StartedAt.IsZero() {
		dst.StartedAt = src.StartedAt
	}
	if !src.FinishedAt.IsZero() {
		dst.FinishedAt = src.FinishedAt
	}
	if src.Status == StatusWaiting {
		dst.WakeAt = src.WakeAt
	} else if dst.Status != StatusWaiting {
		dst.WakeAt = src.WakeAt
	}
	for i := range src.Jobs {
		sj := src.Jobs[i]
		found := false
		for j := range dst.Jobs {
			if dst.Jobs[j].JobID == sj.JobID {
				dst.Jobs[j] = MergeJobRun(dst.Jobs[j], sj)
				found = true
				break
			}
		}
		if !found {
			dst.Jobs = append(dst.Jobs, sj)
		}
	}
}

// MergeStatus combines two run statuses, preferring the more progressed one.
// A waiting status yields to active statuses.
func MergeStatus(dst, src RunStatus) RunStatus {
	if dst == StatusWaiting && (src == StatusQueued || src == StatusPending || src == StatusRunning) {
		return src
	}
	if StatusRank(src) >= StatusRank(dst) {
		return src
	}
	return dst
}

// MergeJobRun combines two job runs for the same job, preferring the more
// progressed one, using finished step count to break ties.
func MergeJobRun(dst, src JobRun) JobRun {
	if dst.Status == StatusWaiting && (src.Status == StatusQueued || src.Status == StatusPending || src.Status == StatusRunning) {
		return src
	}
	sr, dr := StatusRank(src.Status), StatusRank(dst.Status)
	if sr > dr {
		return src
	}
	if sr < dr {
		return dst
	}
	if FinishedSteps(src) >= FinishedSteps(dst) {
		return src
	}
	return dst
}

// StatusRank orders run statuses from least to most progressed so that merge
// logic can compare them numerically.
func StatusRank(s RunStatus) int {
	switch s {
	case StatusPending, StatusQueued:
		return 1
	case StatusRunning:
		return 2
	case StatusWaiting:
		return 3
	case StatusBlocked, StatusSkipped:
		return 4
	case StatusSuccess, StatusFailed, StatusCancelled, StatusExpired:
		return 5
	default:
		return 0
	}
}

// FinishedSteps counts the number of terminal steps in a job run.
func FinishedSteps(j JobRun) int {
	n := 0
	for _, s := range j.Steps {
		if s.Status.IsTerminal() {
			n++
		}
	}
	return n
}

// BuildConditionEnv derives a ConditionEnv from a workflow run, exposing job
// state and outputs for condition evaluation. The name avoids a collision with
// the ConditionEnv type in this package.
func BuildConditionEnv(run *WorkflowRun) ConditionEnv {
	jobs := map[string]JobRun{}
	outputs := map[string]any{}
	for _, j := range run.Jobs {
		jobs[j.JobID] = j
		for k, v := range j.Outputs {
			outputs[j.JobID+"."+k] = v
		}
	}
	return ConditionEnv{Jobs: jobs, Outputs: outputs}
}
