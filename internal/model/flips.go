package model


// AttemptJob is a job of one attempt of a run, with the path of its log, or "".
type AttemptJob struct {
	RunID   int64
	Attempt int
	Kind    JobKind
	Job     Job
	Log     string
}

// Outcome is how a job name, or a step of it, concluded in an attempt.
type Outcome struct {
	Attempt    int
	Conclusion string
}

// Flip is a job name of a run, or a step of it when Step is not "", that
// failed in one attempt and succeeded in another.
type Flip struct {
	RunID        int64
	Job, Step    string
	Outcomes     []Outcome
	FailingSteps []string
	Logs         []string
}

func RerunFlips(jobs []AttemptJob) []Flip { return nil }
