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
