package core

import (
	"path/filepath"
	"testing"
)

func TestRecorderSlotPathUsesStableSlotName(t *testing.T) {
	dir := t.TempDir()
	want := filepath.Join(dir, "recorder-3.lock")
	if got := RecorderSlotPath(dir, 3); got != want {
		t.Fatalf("recorder slot path = %q, want %q", got, want)
	}
}
