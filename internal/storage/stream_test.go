package storage

import (
	"testing"
	"time"

	"github.com/yowainwright/diu/internal/core"
)

func TestExecutionMinHeapMethods(t *testing.T) {
	earlier := &core.ExecutionRecord{Timestamp: time.Unix(1, 0)}
	later := &core.ExecutionRecord{Timestamp: time.Unix(2, 0)}
	records := executionMinHeap{}
	records.Push(later)
	records.Push(earlier)
	lengthMatches := records.Len() == 2
	orderingMatches := !records.Less(0, 1)
	if !lengthMatches || !orderingMatches {
		t.Fatalf("unexpected heap ordering: %#v", records)
	}
	records.Swap(0, 1)
	got := records.Pop()
	popMatches := got == later
	remainingMatches := records.Len() == 1
	if !popMatches || !remainingMatches {
		t.Fatalf("Pop = %#v, heap = %#v", got, records)
	}
}
