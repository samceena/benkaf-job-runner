package job

import (
	"fmt"
	"time"

	"github.com/samceena/benkaf-job-runner/internal/id"
)

type JobState string

const (
	PendingState   JobState = "PENDING"
	ClaimedState   JobState = "CLAIMED"
	RunningState   JobState = "RUNNING"
	CompletedState JobState = "COMPLETED"
	FailedState    JobState = "FAILED"
)

type Job struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Payload     []byte    `json:"payload"`
	State       JobState  `json:"state"`
	WorkerID    string    `json:"worker_id,omitempty"`
	Attempts    int       `json:"attempts"`
	MaxAttempts int       `json:"max_attempts"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// These job state transitions is how the job will be processed
// for an happy path, when a worker gets a request to perform a job
// it will go from Pending -> Claim -> Running -> (Completed of Failed)
// this map also includes unhappy path as well.
// if a worker dies befoe execution, ths state transiton will be Claimed -> Pending , in this case it'll be safe to reassign the job to another worker
var validJobStateTransitions = map[JobState][]JobState{
	PendingState:   {ClaimedState},
	ClaimedState:   {RunningState, PendingState},
	RunningState:   {CompletedState, FailedState, ClaimedState},
	CompletedState: {},
	FailedState:    {},
}

func NewJob(name string, payload []byte) *Job {
	fmt.Println("CReating new job")
	now := time.Now()
	return &Job{
		ID:          id.Generate(),
		Name:        name,
		Payload:     payload,
		State:       PendingState,
		MaxAttempts: 3,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
}

func (j *Job) TransitionTo(next JobState) error {
	for _, valid := range validJobStateTransitions[j.State] {
		if next == valid {
			j.State = next
			j.UpdatedAt = time.Now()
			return nil
		}
	}
	return fmt.Errorf("invalid transitions: %s -> %s", j.State, next)
}
