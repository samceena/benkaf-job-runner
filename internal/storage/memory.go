package storage

import (
	"context"
	"fmt"
	"sync"

	"github.com/samceena/benkaf-job-runner/internal/job"
)

type MemoryStore struct {
	mu      sync.Mutex
	jobs    map[string]*job.Job
	workers map[string]bool
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		jobs:    make(map[string]*job.Job),
		workers: make(map[string]bool),
	}
}

func (m *MemoryStore) CreateJob(ctx context.Context, j *job.Job) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, exists := m.jobs[j.ID]; exists {
		return fmt.Errorf("job %s already exists", j.ID)
	}

	copied := *j
	m.jobs[j.ID] = &copied
	return nil
}

func (m *MemoryStore) GetJob(ctx context.Context, id string) (*job.Job, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	j, exists := m.jobs[id]
	if !exists {
		return nil, fmt.Errorf("job %s not found", id)
	}

	copied := *j
	return &copied, nil
}

func (m *MemoryStore) ListJobsByState(ctx context.Context, state job.JobState) ([]*job.Job, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	result := make([]*job.Job, 0)
	for _, j := range m.jobs {
		if j.State == state {
			copied := *j
			result = append(result, &copied)
		}
	}
	return result, nil
}

func (m *MemoryStore) UpdateJob(ctx context.Context, j *job.Job) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, exists := m.jobs[j.ID]; !exists {
		return fmt.Errorf("job %s not found", j.ID)
	}

	copied := *j
	m.jobs[j.ID] = &copied
	return nil
}

func (m *MemoryStore) ListJobsByWorkerAndState(ctx context.Context, workerId string, jobState job.JobState) ([]*job.Job, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	result := make([]*job.Job, 0)
	for _, j := range m.jobs {
		if j.WorkerID == workerId && j.State == jobState {
			jobData := *j
			result = append(result, &jobData)
		}
	}
	return result, nil
}

func (m *MemoryStore) RegisterWorkers(ctx context.Context, workerIds []string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	for _, workerId := range workerIds {
		m.workers[workerId] = true
	}
	return nil
}

func (m *MemoryStore) ListWorkers(ctx context.Context) ([]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	result := make([]string, 0)
	for id := range m.workers {
		result = append(result, id)
	}
	return result, nil
}

func (m *MemoryStore) GetWorker(ctx context.Context, workerId string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	workerExists := m.workers[workerId]
	if workerExists {
		return workerId, nil
	}
	return "", fmt.Errorf("worker %s not found", workerId)

}
