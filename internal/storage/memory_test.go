package storage

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/samceena/benkaf-job-runner/internal/job"
)

func TestCreateJob_CopySemantics(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()

	j := job.NewJob("send-email", []byte("sam@example.com"))
	if err := store.CreateJob(ctx, j); err != nil {
		t.Fatalf("CreateJob failed: %v", err)
	}

	j.Name = "mutated-after-create"

	got, err := store.GetJob(ctx, j.ID)
	if err != nil {
		t.Fatalf("GetJob failed: %v", err)
	}
	if got.Name != "send-email" {
		t.Errorf("store mutated by caller: got name %q, want %q", got.Name, "send-email")
	}
}

func TestCreateJob_DuplicateID(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()

	j := job.NewJob("send-email", []byte("sam@example.com"))
	if err := store.CreateJob(ctx, j); err != nil {
		t.Fatalf("first CreateJob failed: %v", err)
	}

	if err := store.CreateJob(ctx, j); err == nil {
		t.Fatalf("expected error on duplicate ID, got nil")
	}
}

func TestGetJob_NotFound(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()

	if _, err := store.GetJob(ctx, "does-not-exist"); err == nil {
		t.Fatalf("expected error for missing job, got nil")
	}
}

func TestUpdateJob_RoundTrip(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()

	j := job.NewJob("send-email", []byte("sam@example.com"))
	if err := store.CreateJob(ctx, j); err != nil {
		t.Fatalf("CreateJob failed: %v", err)
	}

	if err := j.TransitionTo(job.ClaimedState); err != nil {
		t.Fatalf("TransitionTo failed: %v", err)
	}
	j.WorkerID = "w1"

	if err := store.UpdateJob(ctx, j); err != nil {
		t.Fatalf("UpdateJob failed: %v", err)
	}

	got, err := store.GetJob(ctx, j.ID)
	if err != nil {
		t.Fatalf("GetJob failed: %v", err)
	}
	if got.State != job.ClaimedState {
		t.Errorf("expected state %s, got %s", job.ClaimedState, got.State)
	}
	if got.WorkerID != "w1" {
		t.Errorf("expected worker_id %q, got %q", "w1", got.WorkerID)
	}
}

func TestUpdateJob_NotFound(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()

	j := job.NewJob("send-email", []byte("sam@example.com"))

	if err := store.UpdateJob(ctx, j); err == nil {
		t.Fatalf("expected error updating non-existent job, got nil")
	}
}

func TestListJobsByState_Filters(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()

	pending1 := job.NewJob("a", nil)
	pending2 := job.NewJob("b", nil)
	claimed := job.NewJob("c", nil)
	if err := claimed.TransitionTo(job.ClaimedState); err != nil {
		t.Fatalf("TransitionTo failed: %v", err)
	}

	for _, j := range []*job.Job{pending1, pending2, claimed} {
		if err := store.CreateJob(ctx, j); err != nil {
			t.Fatalf("CreateJob failed: %v", err)
		}
	}

	got, err := store.ListJobsByState(ctx, job.PendingState)
	if err != nil {
		t.Fatalf("ListJobsByState failed: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("expected 2 pending jobs, got %d", len(got))
	}
	for _, j := range got {
		if j.State != job.PendingState {
			t.Errorf("expected state %s, got %s", job.PendingState, j.State)
		}
	}
}

func TestListJobsByState_EmptyIsNonNil(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()

	got, err := store.ListJobsByState(ctx, job.PendingState)
	if err != nil {
		t.Fatalf("ListJobsByState failed: %v", err)
	}
	if got == nil {
		t.Errorf("expected non-nil empty slice (so JSON marshals to [], not null), got nil")
	}
	if len(got) != 0 {
		t.Errorf("expected empty slice, got %d items", len(got))
	}
}

func TestListJobsByWorkerAndState(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()

	mine := job.NewJob("mine", nil)
	if err := mine.TransitionTo(job.ClaimedState); err != nil {
		t.Fatalf("TransitionTo failed: %v", err)
	}
	mine.WorkerID = "w1"

	otherWorker := job.NewJob("other-worker", nil)
	if err := otherWorker.TransitionTo(job.ClaimedState); err != nil {
		t.Fatalf("TransitionTo failed: %v", err)
	}
	otherWorker.WorkerID = "w2"

	otherState := job.NewJob("other-state", nil)
	otherState.WorkerID = "w1"

	for _, j := range []*job.Job{mine, otherWorker, otherState} {
		if err := store.CreateJob(ctx, j); err != nil {
			t.Fatalf("CreateJob failed: %v", err)
		}
	}

	got, err := store.ListJobsByWorkerAndState(ctx, "w1", job.ClaimedState)
	if err != nil {
		t.Fatalf("ListJobsByWorkerAndState failed: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 job, got %d", len(got))
	}
	if got[0].Name != "mine" {
		t.Errorf("expected job %q, got %q", "mine", got[0].Name)
	}
}

func TestRegisterWorkers_IdempotentAndBulk(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()

	if err := store.RegisterWorkers(ctx, []string{"w1", "w2"}); err != nil {
		t.Fatalf("RegisterWorkers failed: %v", err)
	}
	if err := store.RegisterWorkers(ctx, []string{"w2", "w3"}); err != nil {
		t.Fatalf("RegisterWorkers (second call) failed: %v", err)
	}

	workers, err := store.ListWorkers(ctx)
	if err != nil {
		t.Fatalf("ListWorkers failed: %v", err)
	}
	if len(workers) != 3 {
		t.Errorf("expected 3 unique workers, got %d: %v", len(workers), workers)
	}

	seen := map[string]bool{}
	for _, id := range workers {
		seen[id] = true
	}
	for _, want := range []string{"w1", "w2", "w3"} {
		if !seen[want] {
			t.Errorf("expected worker %q registered, missing", want)
		}
	}
}

func TestGetWorker_NotFound(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()

	if _, err := store.GetWorker(ctx, "nobody"); err == nil {
		t.Fatalf("expected error for missing worker, got nil")
	}
}

func TestMemoryStore_Concurrent_CreateAndGet(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()

	const n = 20
	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func(i int) {
			defer wg.Done()
			j := job.NewJob(fmt.Sprintf("job-%d", i), nil)
			if err := store.CreateJob(ctx, j); err != nil {
				t.Errorf("CreateJob: %v", err)
				return
			}
			if _, err := store.GetJob(ctx, j.ID); err != nil {
				t.Errorf("GetJob: %v", err)
			}
		}(i)
	}
	wg.Wait()

	pending, err := store.ListJobsByState(ctx, job.PendingState)
	if err != nil {
		t.Fatalf("ListJobsByState: %v", err)
	}
	if len(pending) != n {
		t.Errorf("expected %d pending jobs, got %d", n, len(pending))
	}
}

func TestMemoryStore_Concurrent_RegisterWorkers(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()

	const goroutinesCount = 50
	var wg sync.WaitGroup
	wg.Add(goroutinesCount)

	for i := 0; i < goroutinesCount; i++ {
		go func(i int) {
			defer wg.Done()
			ids := []string{
				fmt.Sprintf("w-%d", i), "shared",
			}
			if err := store.RegisterWorkers(ctx, ids); err != nil {
				t.Errorf("RegisterWorjers: %v", err)
			}
		}(i)
	}
	wg.Wait()

	workers, err := store.ListWorkers(ctx)
	if err != nil {
		t.Fatalf("ListWorkers: %v", err)
	}
	if len(workers) != goroutinesCount+1 {
		t.Errorf("expected %d workers, got %d", goroutinesCount+1, len(workers))
	}
}

func TestMemoryStore_Concurrent_ListDuringWrites(t *testing.T) {

	ctx := context.Background()
	store := NewMemoryStore()

	stop := make(chan struct{})
	var wg sync.WaitGroup

	wg.Add(1)
	go func() {
		defer wg.Done()
		i := 0
		for {
			select {
			case <-stop:
				return
			default:
				j := job.NewJob(fmt.Sprintf("j-%d", i), nil)
				_ = store.CreateJob(ctx, j)
				i++
			}
		}
	}()

	for i := 0; i < 1000; i++ {
		if _, err := store.ListJobsByState(ctx, job.PendingState); err != nil {
			t.Errorf("ListJobsByState: %v", err)
			break
		}
	}
	close(stop)
	wg.Wait()
}
