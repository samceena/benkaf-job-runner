package storage

import (
	"context"

	"github.com/samceena/benkaf-job-runner/internal/job"
)

// The purpose of the interface is to provide pluggable storage.
type Store interface {
	// Job
	CreateJob(ctx context.Context, j *job.Job) error
	GetJob(ctx context.Context, id string) (*job.Job, error)
	ListJobsByState(ctx context.Context, state job.JobState) ([]*job.Job, error)
	UpdateJob(ctx context.Context, j *job.Job) error

	// Workers
	RegisterWorker(ctx context.Context, workerId string) error
	ListWorkers(ctx context.Context) ([]string, error)
}
