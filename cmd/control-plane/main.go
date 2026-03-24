package main

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/samceena/benkaf-job-runner/internal/job"
	"github.com/samceena/benkaf-job-runner/internal/storage"
)

type Controller struct {
	store      storage.Store
	mu         sync.Mutex
	nextWorker int
}

func NewController(store storage.Store) *Controller {
	return &Controller{store: store}
}

// this is for for posting a new job POST /job
func (c *Controller) handleSubmitJob(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Name    string `json:"name"`
		Payload string `json:"payload"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	j := job.NewJob(req.Name, []byte(req.Payload))
	if err := c.store.CreateJob(r.Context(), j); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(j)
}

func (c *Controller) handleListWorkers(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	workers, err := c.store.ListWorkers(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(workers)
}

func (c *Controller) handleListJobs(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	stateParam := r.URL.Query().Get("state")
	if stateParam == "" {
		http.Error(w, "state param query required", http.StatusBadRequest)
		return
	}

	state := job.JobState(strings.ToUpper(stateParam))
	jobs, err := c.store.ListJobsByState(r.Context(), state)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(jobs)
}

func (c *Controller) handleGetJob(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	id := strings.TrimPrefix(r.URL.Path, "/jobs/")
	if id == "" {
		http.Error(w, "job id required", http.StatusBadRequest)
		return
	}

	job, err := c.store.GetJob(r.Context(), id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(job)
}

func (c *Controller) handleGetWorker(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	workerId := strings.TrimPrefix(r.URL.Path, "/workers/")
	if workerId == "" {
		http.Error(w, "Worker id is required", http.StatusBadRequest)
		return
	}

	worker, err := c.store.GetWorker(r.Context(), workerId)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(worker)
}

// This function can accept a single worker or workers to register
func (c *Controller) handleRegisterWorkers(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		WorkerIds []string `json:"worker_ids"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	log.Printf("Registering worker with ids %v", req.WorkerIds)

	if err := c.store.RegisterWorkers(r.Context(), req.WorkerIds); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"status":"registered"}`))
}

func (c *Controller) jobAssignmentLoop(ctx context.Context) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			c.assignPendingJobs(ctx)
		}
	}
}

// assignPendingJobs uses round-robin to select which worker gets each pending job.
// nextWorker is a counter that increments with each assignment.
// The Modulo % wraps it around the workers slice so it cycles: 0, 1, 2, ..., n-1, 0, 1, 2, ...
// For example with 3 workers: 0%3=0, 1%3=1, 2%3=2, 3%3=0 (wraps back).
//
// Trade-offs of this round-robin:
// - No load awareness: if one worker gets a long-running job, it still receives the next
//   job in rotation even though it's busy. Other workers that are idle will just sit waiting for their turn.
// - No job-cost awareness: all jobs are treated as equal, but in practice some jobs take
//   seconds and others take minutes.
// - Not optimal for weighted scenarios or heterogeneous workers (different CPU/memory).
// - Simple and fair enough for Phase 1.
//
// Alternatives to consider in later phases:
// - Work-stealing: workers pull jobs when idle (naturally balances load)
// - Least-loaded: assign to the worker with the fewest active jobs
// - Weighted round-robin: give more turns to stronger workers
// - Consistent hashing: hash job to a specific worker (good for cache affinity).
//   Useful when a worker maintains cached state for faster execution. For example,
//   if worker-3 always handles emails for user-2, it can cache auth tokens or user
//   settings so subsequent jobs for that user are faster than on a cold worker.
// - Random: simple but can be uneven under low volume

func (c *Controller) assignPendingJobs(ctx context.Context) {
	workers, err := c.store.ListWorkers(ctx)
	if err != nil || len(workers) == 0 {
		log.Printf("No worker found, stopping job assignment loop")
		return
	}

	pending, err := c.store.ListJobsByState(ctx, job.PendingState)
	if err != nil || len(pending) == 0 {
		return
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	for _, j := range pending {
		log.Printf("Searching for jobs...")

		workerIndexToUse := c.nextWorker % len(workers)
		worker := workers[workerIndexToUse]
		c.nextWorker++

		if err := j.TransitionTo(job.ClaimedState); err != nil {
			log.Printf("failed to claim job %s: %v", j.ID, err)
		}
		j.WorkerID = worker

		if err := c.store.UpdateJob(ctx, j); err != nil {
			log.Printf("failed to update job %s: %v", j.ID, err)
		}
		log.Printf("Assigned job %s to worker %s", j.ID, worker)

	}
}

func main() {
	log.Printf("Initializing MemoryStore")
	store := storage.NewMemoryStore()

	log.Printf("Initializing Controller")
	ctrl := NewController(store)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go ctrl.jobAssignmentLoop(ctx)

	mux := http.NewServeMux()

	// Jobs
	mux.HandleFunc("/jobs", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			ctrl.handleSubmitJob(w, r)
		} else {
			ctrl.handleListJobs(w, r)
		}
	})
	mux.HandleFunc("/jobs/", ctrl.handleGetJob)

	// Workers
	mux.HandleFunc("/workers/register", ctrl.handleRegisterWorkers)
	mux.HandleFunc("/workers/", ctrl.handleGetWorker)
	mux.HandleFunc("/workers", ctrl.handleListWorkers)

	port := ":8080"
	log.Println("Controller started. Listening on port ", port)
	log.Fatal(http.ListenAndServe(port, mux))
}
