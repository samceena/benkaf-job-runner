package id

import (
	"strings"
	"testing"
)

func TestGenerate_HasPrefix(t *testing.T) {
	generate := Generate()
	if !strings.HasPrefix(generate, "job_") {
		t.Errorf("expected prefix %q, got %q", "job_", generate)
	}
}

func TestGenerate_Length(t *testing.T) {
	generate := Generate()
	if len(generate) != 20 {
		t.Errorf("Expected length 20, got %d (%q)", len(generate), generate)
	}
}

func TestGenerate_Unique(t *testing.T) {
	const n = 100
	checked := make(map[string]struct{}, n)
	for i := 0; i < n; i++ {
		id := Generate()
		if _, duplicate := checked[id]; duplicate {
			t.Fatalf("collision after %d ids: %s", i, id)
		}
	}

}
