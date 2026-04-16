package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"math/rand"
	"net/http"
	"os"
	"time"

	"github.com/samceena/benkaf-job-runner/internal/job"
)

type Worker struct {
	id            string
	controllerURL string
	client        *http.Client
}

func NewWorker(id, controllerURL string) *Worker {

	return &Worker{
		id:            id,
		controllerURL: controllerURL,
		client:        &http.Client{Timeout: 5 * time.Second},
	}
}

// this function tells the controller that this worker exists
func (w Worker) register() error {

	body, _ := json.Marshal(map[string][]string{
		"worker_ids": {w.id},
	})

	url := fmt.Sprintf("%s/workers/register", w.controllerURL)

	resp, err := w.client.Post(url, "application/json", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("failed to register: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("register returned status %v", resp.StatusCode)
	}

	log.Printf("Registered with controller as %s", w.id)
	return nil
}

// fetchClaimedJobs gets jobs assigned to this worker in CLAIMED state
func (w Worker) fetchClaimedJobs() ([]*job.Job, error) {
	url := fmt.Sprintf("%s/jobs?state=claimed&worker_id=%s", w.controllerURL, w.id)

	resp, err := w.client.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var jobs []*job.Job
	if err := json.NewDecoder(resp.Body).Decode(&jobs); err != nil {
		return nil, fmt.Errorf("failed to decode jobs: %w", err)
	}
	return jobs, nil
}

// postJobAction send POST /jobs/{id}/start, /Complete, or fail
func (w Worker) postJobAction(jobId, action string) error {
	url := fmt.Sprintf("%s/jobs/%s/%s", w.controllerURL, jobId, action)

	resp, err := w.client.Post(url, "application/json", nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s returned status %d", action, resp.StatusCode)
	}
	return nil
}

// executes a job
func (w Worker) execute(j *job.Job) error {
	duration := time.Duration(1+rand.Intn(3)) * time.Second
	log.Printf("Executing job %s (will take %s)", j.ID, duration)
	time.Sleep(duration)

	if rand.Float64() < 0.1 {
		return fmt.Errorf("simulated failure")
	}
	return nil
}

func (w *Worker) pollAndExecute() {
	jobs, err := w.fetchClaimedJobs()
	if err != nil {
		log.Printf("Error fetching jobs: %v", err)
		return
	}

	for _, j := range jobs {
		log.Printf("Picked up job %s (%s)", j.ID, j.Name)

		// 1. Tell the controller we're starting meaning transition the state from CLAIMED to RUNNING
		if err := w.postJobAction(j.ID, "start"); err != nil {
			log.Printf("Failed to start job %s: %v", j.ID, err)
			continue
		}

		// 2. execute the job
		err := w.execute(j)

		// 3. report result, this should trnaisiton the state as follows: RUNNING -> COMPLETE or FAILED
		if err != nil {
			log.Printf("Job %s failed: %v", j.ID, err)
			if reportError := w.postJobAction(j.ID, "fail"); reportError != nil {
				log.Printf("Failed to repor failure for job %s: %v", j.ID, reportError)
			}
		} else {
			log.Printf("Job %s completed", j.ID)
			if reportError := w.postJobAction(j.ID, "complete"); reportError != nil {
				log.Printf("Failed to repor completion for job %s: %v", j.ID, reportError)
			}

		}
	}
}

func main() {
	workerId := os.Getenv("WORKER_ID")
	if workerId == "" {
		workerId = "worker-1"
	}

	controllerUrl := os.Getenv("CONTROLLER_URL")
	if controllerUrl == "" {
		controllerUrl = "http://localhost:8080"
	}

	w := NewWorker(workerId, controllerUrl)

	if err := w.register(); err != nil {
		log.Fatalf("Failed to register: %v", err)
	}

	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()

	log.Printf("Worker %s polling for jobs...", w.id)
	for range ticker.C {
		w.pollAndExecute()
	}
}
