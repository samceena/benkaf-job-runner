package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/samceena/benkaf-job-runner/internal/job"
)

// fakeController is a small httptest server that records the requests the
// worker makes. Each test mounts its own handler for whichever endpoint it
// cares about and inspects the recorded state afterwards.
type fakeController struct {
	mu       sync.Mutex
	requests []recordedReq
}

type recordedReq struct {
	method string
	path   string
	body   string
}

func (f *fakeController) record(r *http.Request) {
	body := ""
	if r.Body != nil {
		b := make([]byte, 1024)
		n, _ := r.Body.Read(b)
		body = string(b[:n])
	}
	f.mu.Lock()
	f.requests = append(f.requests, recordedReq{method: r.Method, path: r.URL.Path + "?" + r.URL.RawQuery, body: body})
	f.mu.Unlock()
}

func (f *fakeController) count(pathSuffix string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, r := range f.requests {
		if strings.Contains(r.path, pathSuffix) {
			n++
		}
	}
	return n
}

func TestWorker_Register_Success(t *testing.T) {
	fake := &fakeController{}

	mux := http.NewServeMux()
	mux.HandleFunc("/workers/register", func(w http.ResponseWriter, r *http.Request) {
		fake.record(r)
		w.WriteHeader(http.StatusOK)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	w := NewWorker("worker-abc", srv.URL)
	if err := w.register(); err != nil {
		t.Fatalf("register: %v", err)
	}

	if got := fake.count("/workers/register"); got != 1 {
		t.Errorf("expected 1 register call, got %d", got)
	}
	if !strings.Contains(fake.requests[0].body, "worker-abc") {
		t.Errorf("expected body to contain worker id, got %q", fake.requests[0].body)
	}
}

func TestWorker_Register_NonOK(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "down", http.StatusInternalServerError)
	}))
	defer srv.Close()

	w := NewWorker("worker-1", srv.URL)
	if err := w.register(); err == nil {
		t.Fatalf("expected error on 500, got nil")
	}
}

func TestWorker_FetchClaimedJobs(t *testing.T) {
	expected := []*job.Job{
		{ID: "job_1", Name: "send", State: job.ClaimedState, WorkerID: "w1"},
		{ID: "job_2", Name: "send", State: job.ClaimedState, WorkerID: "w1"},
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("state") != "claimed" {
			t.Errorf("expected state=claimed, got %q", r.URL.Query().Get("state"))
		}
		if r.URL.Query().Get("worker_id") != "w1" {
			t.Errorf("expected worker_id=w1, got %q", r.URL.Query().Get("worker_id"))
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(expected)
	}))
	defer srv.Close()

	wk := NewWorker("w1", srv.URL)
	got, err := wk.fetchClaimedJobs()
	if err != nil {
		t.Fatalf("fetchClaimedJobs: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 jobs, got %d", len(got))
	}
	if got[0].ID != "job_1" || got[1].ID != "job_2" {
		t.Errorf("unexpected jobs: %+v", got)
	}
}

func TestWorker_PostJobAction_Success(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	wk := NewWorker("w1", srv.URL)
	if err := wk.postJobAction("job_123", "start"); err != nil {
		t.Fatalf("postJobAction: %v", err)
	}

	if gotPath != "/jobs/job_123/start" {
		t.Errorf("expected path /jobs/job_123/start, got %s", gotPath)
	}
}

func TestWorker_PostJobAction_NonOK(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "conflict", http.StatusConflict)
	}))
	defer srv.Close()

	wk := NewWorker("w1", srv.URL)
	if err := wk.postJobAction("job_x", "complete"); err == nil {
		t.Fatalf("expected error for 409 response, got nil")
	}
}

func TestWorker_PollAndExecute_CallsStartThenResult(t *testing.T) {
	// Controller returns one claimed job. We expect the worker to POST
	// /start, run the job, then POST either /complete or /fail.
	var (
		startCount    int32
		completeCount int32
		failCount     int32
	)

	j := &job.Job{ID: "job_poll", Name: "noop", State: job.ClaimedState, WorkerID: "w1"}

	mux := http.NewServeMux()
	mux.HandleFunc("/jobs", func(w http.ResponseWriter, r *http.Request) {
		// fetch claimed jobs
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode([]*job.Job{j})
	})
	mux.HandleFunc("/jobs/", func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/start"):
			atomic.AddInt32(&startCount, 1)
		case strings.HasSuffix(r.URL.Path, "/complete"):
			atomic.AddInt32(&completeCount, 1)
		case strings.HasSuffix(r.URL.Path, "/fail"):
			atomic.AddInt32(&failCount, 1)
		}
		w.WriteHeader(http.StatusOK)
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()

	wk := NewWorker("w1", srv.URL)
	wk.pollAndExecute()

	if got := atomic.LoadInt32(&startCount); got != 1 {
		t.Errorf("expected 1 /start call, got %d", got)
	}
	total := atomic.LoadInt32(&completeCount) + atomic.LoadInt32(&failCount)
	if total != 1 {
		t.Errorf("expected exactly 1 /complete or /fail call, got %d (complete=%d fail=%d)",
			total, completeCount, failCount)
	}
}

func TestWorker_PollAndExecute_NoJobs(t *testing.T) {
	var called int32
	mux := http.NewServeMux()
	mux.HandleFunc("/jobs", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode([]*job.Job{})
	})
	mux.HandleFunc("/jobs/", func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&called, 1)
		w.WriteHeader(http.StatusOK)
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()

	wk := NewWorker("w1", srv.URL)
	wk.pollAndExecute()

	if got := atomic.LoadInt32(&called); got != 0 {
		t.Errorf("expected no sub-resource calls when no jobs, got %d", got)
	}
}

func TestWorker_PollAndExecute_FetchError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Return a 500 so fetchClaimedJobs sees something, but also a
		// malformed body so decoding fails.
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("not-json"))
	}))
	defer srv.Close()

	wk := NewWorker("w1", srv.URL)
	// Should log and return — no panic.
	wk.pollAndExecute()
}
