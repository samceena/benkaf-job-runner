package job

import "testing"

func TestNewJob_StartsPending(t *testing.T) {
	j := NewJob("send-email", []byte("jane@example.com"))

	if j.State != PendingState {
		t.Errorf("expected state %s, got %s", PendingState, j.State)
	}
}

func TestTransition_PendingToClaimed(t *testing.T) {
	j := NewJob("send-email", []byte("jane@example.com"))

	err := j.TransitionTo(ClaimedState)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if j.State != ClaimedState {
		t.Errorf("expected state %s, got %s", ClaimedState, j.State)
	}
}

func TestTransition_HappyPath(t *testing.T) {
	j := NewJob("send-email", []byte("sam@example.com"))

	if err := j.TransitionTo(ClaimedState); err != nil {
		t.Fatalf("PENDING -> CLAIMED failed: %v", err)
	}

	if err := j.TransitionTo(RunningState); err != nil {
		t.Fatalf("CLAIMED -> RUNNING failed: %v", err)
	}

	if err := j.TransitionTo(CompletedState); err != nil {
		t.Fatalf("RUNNING -> COMPLETED failed: %v", err)
	}

	if j.State != CompletedState {
		t.Errorf("expected state %s, got %s", CompletedState, j.State)
	}

}

func TestTransition_InvalidPendingToRunning(t *testing.T) {
	j := NewJob("send-email", []byte("sam@exmaple.com"))

	err := j.TransitionTo(RunningState)
	if err == nil {
		t.Fatalf("expected error for PENDING -> RUNNING, got nil")
	}
}

func TestTransition_CompletedIsTerminal(t *testing.T) {
	j := NewJob("send-email", []byte("sam@exmaple.com"))
	j.TransitionTo(ClaimedState)
	j.TransitionTo(RunningState)
	j.TransitionTo(CompletedState)

	states := []JobState{PendingState, ClaimedState, RunningState, FailedState}
	for _, s := range states {
		err := j.TransitionTo(s)
		if err == nil {
			t.Errorf("expected error for COMPLETED -> %s, got nil", s)
		}
	}
}

func TestTransition_FailedIsTerminal(t *testing.T) {
	j := NewJob("send-email", []byte("sam@exmaple.com"))
	j.TransitionTo(ClaimedState)
	j.TransitionTo(RunningState)
	j.TransitionTo(FailedState)

	states := []JobState{PendingState, ClaimedState, RunningState, CompletedState}
	for _, s := range states {
		err := j.TransitionTo(s)
		if err == nil {
			t.Errorf("expected error for FAILED -> %s, got nil", s)
		}
	}
}

func TestTransition_ClaimedBackToPending(t *testing.T) {
	j := NewJob("send-email", []byte("sam@exmaple.com"))
	j.TransitionTo(ClaimedState)

	if err := j.TransitionTo(PendingState); err != nil {
		t.Fatalf("CLAIMED -> PENDING failed: %v", err)
	}
}

func TestTransition_RunningToFailed(t *testing.T) {
	j := NewJob("send-email", []byte("sam@exmaple.com"))
	j.TransitionTo(ClaimedState)
	j.TransitionTo(RunningState)

	if err := j.TransitionTo(FailedState); err != nil {
		t.Fatalf("RUNNING -> FAILED failed: %v", err)
	}
}

func TestTransition_RunningToClaimed(t *testing.T) {
	j := NewJob("send-email", []byte("sam@example.com"))
	j.TransitionTo(ClaimedState)
	j.TransitionTo(RunningState)

	if err := j.TransitionTo(ClaimedState); err != nil {
		t.Fatalf("RUNNING -> CLAIMED (reassignment) failed: %v", err)
	}
}
