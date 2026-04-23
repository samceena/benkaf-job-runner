package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/samceena/benkaf-job-runner/internal/job"
	"github.com/samceena/benkaf-job-runner/internal/storage"
)

// newTestServer wires up a controller with an in-memory store and the same
// mux as main(). Returns the server and the underlying controller so tests
// can poke at internal state when they need to.
func newTestServer(t *testing.T) (*httptest.Server, *Controller) {
	t.Helper()

	store := storage.NewMemoryStore()
	ctrl := NewController(store)

	mux := http.NewServeMux()
	mux.HandleFunc("/jobs", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			ctrl.handleSubmitJob(w, r)
		} else {
			ctrl.handleListJobs(w, r)
		}
	})
	mux.HandleFunc("/jobs/", func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		switch {
		case strings.HasSuffix(path, "/start"):
			ctrl.handleStartJob(w, r)
		case strings.HasSuffix(path, "/complete"):
			ctrl.handleCompleteJob(w, r)
		case strings.HasSuffix(path, "/fail"):
			ctrl.handleFailJob(w, r)
		default:
			ctrl.handleGetJob(w, r)
		}
	})
	mux.HandleFunc("/workers/register", ctrl.handleRegisterWorkers)
	mux.HandleFunc("/workers/", ctrl.handleGetWorker)
	mux.HandleFunc("/workers", ctrl.handleListWorkers)

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, ctrl
}

func submitJob(t *testing.T, srv *httptest.Server, name, payload string) *job.Job {
	t.Helper()

	body, _ := json.Marshal(map[string]string{"name": name, "payload": payload})
	resp, err := http.Post(srv.URL+"/jobs", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST /jobs: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("POST /jobs status=%d body=%s", resp.StatusCode, b)
	}

	var j job.Job
	if err := json.NewDecoder(resp.Body).Decode(&j); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	return &j
}

func TestSubmitJob_Created(t *testing.T) {
	srv, _ := newTestServer(t)

	j := submitJob(t, srv, "send-email", "sam@example.com")

	if j.ID == "" {
		t.Errorf("expected job ID to be set")
	}
	if j.State != job.PendingState {
		t.Errorf("expected state %s, got %s", job.PendingState, j.State)
	}
	if j.Name != "send-email" {
		t.Errorf("expected name %q, got %q", "send-email", j.Name)
	}
}

func TestSubmitJob_BadJSON(t *testing.T) {
	srv, _ := newTestServer(t)

	resp, err := http.Post(srv.URL+"/jobs", "application/json", strings.NewReader("{not json"))
	if err != nil {
		t.Fatalf("POST /jobs: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", resp.StatusCode)
	}
}

func TestSubmitJob_WrongMethod(t *testing.T) {
	// The /jobs route dispatches GET to handleListJobs, but PUT should not
	// hit the submit handler. We call the handler directly to check guarding.
	srv, _ := newTestServer(t)

	req, _ := http.NewRequest(http.MethodPut, srv.URL+"/jobs", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("PUT /jobs: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", resp.StatusCode)
	}
}

func TestListJobs_FilterByState(t *testing.T) {
	srv, ctrl := newTestServer(t)
	ctx := context.Background()

	submitJob(t, srv, "a", "")
	submitJob(t, srv, "b", "")

	// Move one job to CLAIMED directly via the store.
	j := submitJob(t, srv, "c", "")
	stored, err := ctrl.store.GetJob(ctx, j.ID)
	if err != nil {
		t.Fatalf("GetJob: %v", err)
	}
	if err := stored.TransitionTo(job.ClaimedState); err != nil {
		t.Fatalf("TransitionTo: %v", err)
	}
	if err := ctrl.store.UpdateJob(ctx, stored); err != nil {
		t.Fatalf("UpdateJob: %v", err)
	}

	resp, err := http.Get(srv.URL + "/jobs?state=pending")
	if err != nil {
		t.Fatalf("GET /jobs: %v", err)
	}
	defer resp.Body.Close()

	var got []*job.Job
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("expected 2 pending, got %d", len(got))
	}
}

func TestListJobs_FilterByWorker(t *testing.T) {
	srv, ctrl := newTestServer(t)
	ctx := context.Background()

	j1 := submitJob(t, srv, "j1", "")
	j2 := submitJob(t, srv, "j2", "")

	// Claim j1 for w1 only.
	stored, _ := ctrl.store.GetJob(ctx, j1.ID)
	stored.TransitionTo(job.ClaimedState)
	stored.WorkerID = "w1"
	if err := ctrl.store.UpdateJob(ctx, stored); err != nil {
		t.Fatalf("UpdateJob: %v", err)
	}

	resp, err := http.Get(srv.URL + "/jobs?state=claimed&worker_id=w1")
	if err != nil {
		t.Fatalf("GET /jobs: %v", err)
	}
	defer resp.Body.Close()

	var got []*job.Job
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 job, got %d", len(got))
	}
	if got[0].ID != j1.ID {
		t.Errorf("expected %s, got %s", j1.ID, got[0].ID)
	}
	_ = j2
}

func TestListJobs_MissingStateParam(t *testing.T) {
	srv, _ := newTestServer(t)

	resp, err := http.Get(srv.URL + "/jobs")
	if err != nil {
		t.Fatalf("GET /jobs: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", resp.StatusCode)
	}
}

func TestGetJob_Found(t *testing.T) {
	srv, _ := newTestServer(t)
	j := submitJob(t, srv, "hello", "")

	resp, err := http.Get(srv.URL + "/jobs/" + j.ID)
	if err != nil {
		t.Fatalf("GET /jobs/{id}: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var got job.Job
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.ID != j.ID {
		t.Errorf("expected id %s, got %s", j.ID, got.ID)
	}
}

func TestGetJob_NotFound(t *testing.T) {
	srv, _ := newTestServer(t)

	resp, err := http.Get(srv.URL + "/jobs/does-not-exist")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("expected 404, got %d", resp.StatusCode)
	}
}

func TestStartJob_HappyPath(t *testing.T) {
	srv, ctrl := newTestServer(t)
	ctx := context.Background()

	j := submitJob(t, srv, "run-me", "")

	// Move to CLAIMED first so /start (CLAIMED->RUNNING) is valid.
	stored, _ := ctrl.store.GetJob(ctx, j.ID)
	stored.TransitionTo(job.ClaimedState)
	stored.WorkerID = "w1"
	if err := ctrl.store.UpdateJob(ctx, stored); err != nil {
		t.Fatalf("UpdateJob: %v", err)
	}

	resp, err := http.Post(srv.URL+"/jobs/"+j.ID+"/start", "application/json", nil)
	if err != nil {
		t.Fatalf("POST /start: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, b)
	}

	final, err := ctrl.store.GetJob(ctx, j.ID)
	if err != nil {
		t.Fatalf("GetJob: %v", err)
	}
	if final.State != job.RunningState {
		t.Errorf("expected %s, got %s", job.RunningState, final.State)
	}
}

func TestStartJob_InvalidTransition(t *testing.T) {
	srv, _ := newTestServer(t)
	j := submitJob(t, srv, "run-me", "")

	// PENDING -> RUNNING is not allowed; should be 409.
	resp, err := http.Post(srv.URL+"/jobs/"+j.ID+"/start", "application/json", nil)
	if err != nil {
		t.Fatalf("POST /start: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusConflict {
		t.Errorf("expected 409, got %d", resp.StatusCode)
	}
}

func TestStartJob_NotFound(t *testing.T) {
	srv, _ := newTestServer(t)

	resp, err := http.Post(srv.URL+"/jobs/missing/start", "application/json", nil)
	if err != nil {
		t.Fatalf("POST /start: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("expected 404, got %d", resp.StatusCode)
	}
}

func TestCompleteJob_HappyPath(t *testing.T) {
	srv, ctrl := newTestServer(t)
	ctx := context.Background()

	j := submitJob(t, srv, "ok", "")
	stored, _ := ctrl.store.GetJob(ctx, j.ID)
	stored.TransitionTo(job.ClaimedState)
	stored.TransitionTo(job.RunningState)
	if err := ctrl.store.UpdateJob(ctx, stored); err != nil {
		t.Fatalf("UpdateJob: %v", err)
	}

	resp, err := http.Post(srv.URL+"/jobs/"+j.ID+"/complete", "application/json", nil)
	if err != nil {
		t.Fatalf("POST /complete: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, b)
	}

	final, _ := ctrl.store.GetJob(ctx, j.ID)
	if final.State != job.CompletedState {
		t.Errorf("expected %s, got %s", job.CompletedState, final.State)
	}
}

func TestCompleteJob_InvalidTransition(t *testing.T) {
	srv, _ := newTestServer(t)
	j := submitJob(t, srv, "ok", "")

	// PENDING -> COMPLETED is not allowed.
	resp, err := http.Post(srv.URL+"/jobs/"+j.ID+"/complete", "application/json", nil)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusConflict {
		t.Errorf("expected 409, got %d", resp.StatusCode)
	}
}

func TestFailJob_HappyPath(t *testing.T) {
	srv, ctrl := newTestServer(t)
	ctx := context.Background()

	j := submitJob(t, srv, "ok", "")
	stored, _ := ctrl.store.GetJob(ctx, j.ID)
	stored.TransitionTo(job.ClaimedState)
	stored.TransitionTo(job.RunningState)
	if err := ctrl.store.UpdateJob(ctx, stored); err != nil {
		t.Fatalf("UpdateJob: %v", err)
	}

	resp, err := http.Post(srv.URL+"/jobs/"+j.ID+"/fail", "application/json", nil)
	if err != nil {
		t.Fatalf("POST /fail: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}

	final, _ := ctrl.store.GetJob(ctx, j.ID)
	if final.State != job.FailedState {
		t.Errorf("expected %s, got %s", job.FailedState, final.State)
	}
}

func TestRegisterWorkers_BulkAndIdempotent(t *testing.T) {
	srv, ctrl := newTestServer(t)
	ctx := context.Background()

	body, _ := json.Marshal(map[string][]string{
		"worker_ids": {"w1", "w2"},
	})
	resp, err := http.Post(srv.URL+"/workers/register", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("first register: expected 200, got %d", resp.StatusCode)
	}

	// Register again, overlapping + new. Total should stay at 3.
	body2, _ := json.Marshal(map[string][]string{
		"worker_ids": {"w2", "w3"},
	})
	resp2, err := http.Post(srv.URL+"/workers/register", "application/json", bytes.NewReader(body2))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	resp2.Body.Close()

	workers, err := ctrl.store.ListWorkers(ctx)
	if err != nil {
		t.Fatalf("ListWorkers: %v", err)
	}
	if len(workers) != 3 {
		t.Errorf("expected 3 workers, got %d (%v)", len(workers), workers)
	}
}

func TestRegisterWorkers_BadJSON(t *testing.T) {
	srv, _ := newTestServer(t)

	resp, err := http.Post(srv.URL+"/workers/register", "application/json", strings.NewReader("nope"))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", resp.StatusCode)
	}
}

func TestGetWorker_FoundAndNotFound(t *testing.T) {
	srv, ctrl := newTestServer(t)
	if err := ctrl.store.RegisterWorkers(context.Background(), []string{"w1"}); err != nil {
		t.Fatalf("RegisterWorkers: %v", err)
	}

	resp, err := http.Get(srv.URL + "/workers/w1")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200 for existing worker, got %d", resp.StatusCode)
	}

	resp2, err := http.Get(srv.URL + "/workers/nope")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	resp2.Body.Close()
	if resp2.StatusCode != http.StatusNotFound {
		t.Errorf("expected 404 for missing worker, got %d", resp2.StatusCode)
	}
}

func TestListWorkers_Populated(t *testing.T) {
	srv, ctrl := newTestServer(t)
	if err := ctrl.store.RegisterWorkers(context.Background(), []string{"w1", "w2"}); err != nil {
		t.Fatalf("RegisterWorkers: %v", err)
	}

	resp, err := http.Get(srv.URL + "/workers")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()

	var workers []string
	if err := json.NewDecoder(resp.Body).Decode(&workers); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(workers) != 2 {
		t.Errorf("expected 2 workers, got %d", len(workers))
	}
}

func TestAssignPendingJobs_RoundRobin(t *testing.T) {
	// Register 2 workers, submit 4 jobs, run one pass of the assignment
	// loop and assert each worker gets 2 jobs.
	_, ctrl := newTestServer(t)
	ctx := context.Background()

	if err := ctrl.store.RegisterWorkers(ctx, []string{"w1", "w2"}); err != nil {
		t.Fatalf("RegisterWorkers: %v", err)
	}

	for i := 0; i < 4; i++ {
		j := job.NewJob("job", nil)
		if err := ctrl.store.CreateJob(ctx, j); err != nil {
			t.Fatalf("CreateJob: %v", err)
		}
	}

	ctrl.assignPendingJobs(ctx)

	w1Jobs, _ := ctrl.store.ListJobsByWorkerAndState(ctx, "w1", job.ClaimedState)
	w2Jobs, _ := ctrl.store.ListJobsByWorkerAndState(ctx, "w2", job.ClaimedState)

	if len(w1Jobs)+len(w2Jobs) != 4 {
		t.Fatalf("expected 4 total claimed, got %d+%d", len(w1Jobs), len(w2Jobs))
	}
	// ListWorkers iterates a map so ordering isn't guaranteed — we only
	// care that the split is even.
	if len(w1Jobs) != 2 || len(w2Jobs) != 2 {
		t.Errorf("uneven assignment: w1=%d w2=%d", len(w1Jobs), len(w2Jobs))
	}
}

func TestAssignPendingJobs_NoWorkers(t *testing.T) {
	_, ctrl := newTestServer(t)
	ctx := context.Background()

	j := job.NewJob("lonely", nil)
	if err := ctrl.store.CreateJob(ctx, j); err != nil {
		t.Fatalf("CreateJob: %v", err)
	}

	ctrl.assignPendingJobs(ctx)

	stored, _ := ctrl.store.GetJob(ctx, j.ID)
	if stored.State != job.PendingState {
		t.Errorf("expected job to stay PENDING, got %s", stored.State)
	}
}

func TestAssignPendingJobs_NoPending(t *testing.T) {
	_, ctrl := newTestServer(t)
	ctx := context.Background()

	if err := ctrl.store.RegisterWorkers(ctx, []string{"w1"}); err != nil {
		t.Fatalf("RegisterWorkers: %v", err)
	}

	// Should be a no-op; just make sure nothing panics.
	ctrl.assignPendingJobs(ctx)
}
